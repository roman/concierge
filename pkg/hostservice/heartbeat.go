package hostservice

import (
	"context"
	"time"

	"github.com/roman/concierge/pkg/heartbeat"
)

func (server *Server) runPrimaryScheduler(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	lastError := ""
	for {
		state, err := heartbeat.Tick(
			ctx,
			server.config.Root,
			server.config.MessageRoot,
			server.config.Recipient,
			server.config.HerdrExecutable,
			time.Now(),
		)
		if err == nil && state.Enabled && state.Pending != nil {
			delivery, cancel := context.WithTimeout(ctx, 10*time.Second)
			err = (heartbeat.Store{Root: server.config.Root}).DeliverCodex(
				delivery,
				server.config.WorkerCommands.Concierge,
				server.config.MessageRoot,
				server.config.Recipient.String(),
				time.Now(),
			)
			cancel()
		}
		if err != nil && ctx.Err() == nil && err.Error() != lastError {
			server.report(err)
			lastError = err.Error()
		}
		if err == nil {
			lastError = ""
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
