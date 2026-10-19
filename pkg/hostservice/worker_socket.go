package hostservice

import (
	"context"
	"errors"
	"net"
	"sync"

	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/stateversion"
	"github.com/roman/concierge/pkg/workerservice"
)

type taskWorkerSocket struct {
	listener *net.UnixListener
	cancel   context.CancelFunc
}

func (server *Server) listenWorkerSocket(version *stateversion.Guard) (*net.UnixListener, error) {
	selected, err := version.Version()
	if err != nil || selected < 3 {
		return nil, err
	}
	worker, err := workerservice.New(workerservice.Config{
		Root: server.config.Root, MessageRoot: server.config.MessageRoot,
		Recipient: server.config.Recipient, ReadSettings: server.config.WorkerSettings,
	})
	if err != nil {
		return nil, err
	}
	listener, err := workerservice.Listen(server.config.Root)
	if err != nil {
		return nil, err
	}
	server.workerServer, server.workerListener = worker, listener
	return listener, nil
}

func (server *Server) startWorkerSocket(
	ctx context.Context,
	cancel context.CancelFunc,
	running *sync.WaitGroup,
) <-chan error {
	done := make(chan error, 1)
	if server.workerListener == nil {
		close(done)
		return done
	}
	server.workerTaskMu.Lock()
	server.workerTaskContext, server.workerTaskCancel = ctx, cancel
	server.workerTaskListeners = make(map[string]taskWorkerSocket)
	server.workerTaskMu.Unlock()
	running.Add(1)
	go func() {
		defer running.Done()
		err := server.workerServer.Serve(ctx, server.workerListener)
		done <- err
		if err != nil {
			cancel()
		}
	}()
	return done
}

func (server *Server) ensureTaskWorkerSocket(task string) error {
	if err := remoteworker.ValidatePrimaryWorkerSocketPath(server.config.Root, task); err != nil {
		return err
	}
	server.workerTaskMu.Lock()
	defer server.workerTaskMu.Unlock()
	if server.workerServer == nil {
		return nil
	}
	if server.workerTaskContext == nil || server.workerTaskContext.Err() != nil {
		return remoteworker.ErrTunnelUnreachable
	}
	if server.workerTaskListeners[task].listener != nil {
		return nil
	}
	listener, err := workerservice.ListenPath(
		remoteworker.PrimaryWorkerSocketPath(server.config.Root, task),
	)
	if err != nil {
		return err
	}
	ctx, stop := context.WithCancel(server.workerTaskContext)
	server.workerTaskListeners[task] = taskWorkerSocket{listener: listener, cancel: stop}
	server.workerTaskRunning.Add(1)
	cancel := server.workerTaskCancel
	go func() {
		defer server.workerTaskRunning.Done()
		if err := server.workerServer.ServeTask(ctx, listener, task); err != nil {
			server.report(err)
			cancel()
		}
	}()
	return nil
}

func (server *Server) closeTaskWorkerSockets() {
	server.workerTaskMu.Lock()
	server.workerTaskContext = nil
	for _, socket := range server.workerTaskListeners {
		socket.cancel()
		_ = socket.listener.Close()
	}
	server.workerTaskMu.Unlock()
	server.workerTaskRunning.Wait()
}

func (server *Server) closeTaskWorkerSocket(task string) {
	server.workerTaskMu.Lock()
	defer server.workerTaskMu.Unlock()
	if socket, found := server.workerTaskListeners[task]; found {
		socket.cancel()
		_ = socket.listener.Close()
		delete(server.workerTaskListeners, task)
	}
}

func closeUnixListener(listener *net.UnixListener) error {
	if listener == nil {
		return nil
	}
	err := listener.Close()
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}
