package hostservice

import (
	"context"
	"errors"
	"net"
	"sync"

	"github.com/roman/concierge/pkg/stateversion"
	"github.com/roman/concierge/pkg/workerservice"
)

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
