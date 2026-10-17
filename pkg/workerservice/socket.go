package workerservice

import (
	"context"
	"encoding/json"
	"net"
	"sync"
	"time"
)

const workerConnections = 8
const workerIOTimeout = 5 * time.Second
const workerOperationTimeout = 30 * time.Second
const workerBurst = 32
const workerRequestsPerSecond = 8

type admissionLimiter struct {
	mu     sync.Mutex
	now    func() time.Time
	last   time.Time
	tokens float64
}

func (limiter *admissionLimiter) allow() bool {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	now := time.Now()
	if limiter.now != nil {
		now = limiter.now()
	}
	if limiter.last.IsZero() {
		limiter.last, limiter.tokens = now, workerBurst
	}
	elapsed := max(0, now.Sub(limiter.last).Seconds())
	limiter.tokens = min(workerBurst, limiter.tokens+elapsed*workerRequestsPerSecond)
	limiter.last = now
	if limiter.tokens < 1 {
		return false
	}
	limiter.tokens--
	return true
}

// Serve accepts only the worker protocol. It never applies host caller rules.
func (server *Server) Serve(ctx context.Context, listener *net.UnixListener) error {
	lifetime, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(lifetime, func() { _ = listener.Close() })
	defer stop()
	defer listener.Close()
	var running sync.WaitGroup
	defer running.Wait()
	slots := make(chan struct{}, workerConnections)
	for {
		connection, err := listener.AcceptUnix()
		if err != nil {
			cancel()
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case slots <- struct{}{}:
			running.Add(1)
			go func() {
				defer running.Done()
				defer func() { <-slots }()
				defer connection.Close()
				stop := context.AfterFunc(lifetime, func() { _ = connection.Close() })
				defer stop()
				server.handle(lifetime, connection)
			}()
		default:
			_ = connection.Close()
		}
	}
}

func (server *Server) handle(ctx context.Context, connection *net.UnixConn) {
	admitted := server.admission.allow()
	_ = connection.SetReadDeadline(time.Now().Add(workerIOTimeout))
	var request Request
	err := decodeBounded(connection, &request)
	if !admitted {
		server.respond(
			connection,
			Response{Outcome: Busy, Error: "worker request rate limit reached; retry later"},
		)
		return
	}
	if err != nil {
		server.respond(connection, Response{Outcome: Invalid, Error: ErrInvalidRequest.Error()})
		return
	}
	operation, cancel := context.WithTimeout(ctx, workerOperationTimeout)
	defer cancel()
	if err := operation.Err(); err != nil {
		return
	}
	server.respond(connection, server.perform(operation, request))
}

func (server *Server) respond(connection *net.UnixConn, response Response) {
	data, err := json.Marshal(response)
	if err != nil || len(data) > maxWireBytes {
		data, _ = json.Marshal(
			Response{Outcome: Failed, Error: "worker response exceeds the size limit"},
		)
	}
	_ = connection.SetWriteDeadline(time.Now().Add(workerIOTimeout))
	_, _ = connection.Write(data)
}
