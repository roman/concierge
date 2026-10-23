package hostservice

import (
	"context"
	"sync"
)

type initialRecovery struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (registry *controllerRegistry) beginInitialRecovery(
	ctx context.Context,
	key controllerKey,
) (context.Context, func(), error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.closed || registry.ctx.Err() != nil {
		return nil, nil, context.Canceled
	}
	if registry.allRecoverySuspensions != 0 || registry.recoverySuspensions[key] != 0 ||
		registry.initialRecoveries[key] != nil {
		return nil, nil, errControllerBusy
	}
	attempt, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(registry.ctx, cancel)
	recovery := &initialRecovery{cancel: cancel, done: make(chan struct{})}
	registry.initialRecoveries[key] = recovery
	return attempt, func() {
		cancel()
		stop()
		registry.mu.Lock()
		defer registry.mu.Unlock()
		delete(registry.initialRecoveries, key)
		close(recovery.done)
	}, nil
}

// An empty selection suspends all background launch recovery. Keep the
// suspension until resource inspection and the requested operation finish.
func (registry *controllerRegistry) suspendInitialRecovery(
	ctx context.Context,
	keys ...controllerKey,
) (func(), error) {
	registry.mu.Lock()
	var attempts []*initialRecovery
	if len(keys) == 0 {
		registry.allRecoverySuspensions++
		for _, attempt := range registry.initialRecoveries {
			attempts = append(attempts, attempt)
		}
	} else {
		for _, key := range keys {
			registry.recoverySuspensions[key]++
			if attempt := registry.initialRecoveries[key]; attempt != nil {
				attempts = append(attempts, attempt)
			}
		}
	}
	registry.mu.Unlock()
	var once sync.Once
	resume := func() {
		once.Do(func() {
			registry.mu.Lock()
			defer registry.mu.Unlock()
			if len(keys) == 0 {
				registry.allRecoverySuspensions--
				return
			}
			for _, key := range keys {
				registry.recoverySuspensions[key]--
				if registry.recoverySuspensions[key] == 0 {
					delete(registry.recoverySuspensions, key)
				}
			}
		})
	}
	for _, attempt := range attempts {
		attempt.cancel()
	}
	for _, attempt := range attempts {
		select {
		case <-attempt.done:
		case <-ctx.Done():
			resume()
			return nil, ctx.Err()
		}
	}
	return resume, nil
}
