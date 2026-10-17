package taskstate

import "time"

type ConnectionChannel string

const (
	HerdrChannel  ConnectionChannel = "herdr"
	TunnelChannel ConnectionChannel = "tunnel"
)

type ConnectionStatus string

const (
	ConnectionReachable   ConnectionStatus = "reachable"
	ConnectionUnreachable ConnectionStatus = "unreachable"
)

// ConnectionState preserves independent probe history and retry timing.
type ConnectionState struct {
	Status      ConnectionStatus `json:"status,omitempty"`
	LastAttempt time.Time        `json:"last_attempt,omitzero"`
	LastSuccess time.Time        `json:"last_success,omitzero"`
	LastError   string           `json:"last_error,omitempty"`
	NextRetry   time.Time        `json:"next_retry,omitzero"`
	Failures    uint32           `json:"failures,omitempty"`
}

type Connections struct {
	Herdr  ConnectionState `json:"herdr"`
	Tunnel ConnectionState `json:"tunnel"`
}

type ConnectionObservation struct {
	At    time.Time
	Error string
}

func (state ConnectionState) Waiting(now time.Time) bool {
	return state.Status == ConnectionUnreachable && now.Before(state.NextRetry)
}

func connectionBackoff(failures uint32) time.Duration {
	return min(time.Minute, time.Second<<min(failures-1, 6))
}

func validConnection(state ConnectionState) bool {
	if state.Status == "" {
		return state == (ConnectionState{})
	}
	if state.LastAttempt.IsZero() || state.LastSuccess.After(state.LastAttempt) {
		return false
	}
	switch state.Status {
	case ConnectionReachable:
		return state.LastSuccess.Equal(state.LastAttempt) && state.LastError == "" &&
			state.Failures == 0 &&
			state.NextRetry.IsZero()
	case ConnectionUnreachable:
		return singleLine(state.LastError) && len(state.LastError) <= 4096 && state.Failures > 0 &&
			state.Failures <= 31 &&
			state.NextRetry.Equal(state.LastAttempt.Add(connectionBackoff(state.Failures)))
	default:
		return false
	}
}

// RecordConnection changes only one current remote generation's probe state.
func (task Task) RecordConnection(
	generation string,
	channel ConnectionChannel,
	observation ConnectionObservation,
) (Task, error) {
	snapshot := task.Snapshot()
	worker := snapshot.Worker
	if snapshot.State != InFlight || snapshot.Completion != nil || worker == nil ||
		worker.Machine == "" || worker.Generation != generation || observation.At.IsZero() {
		return Task{}, ErrInvalidTransition
	}
	if worker.Connections == nil {
		worker.Connections = &Connections{}
	}
	var state *ConnectionState
	switch channel {
	case HerdrChannel:
		state = &worker.Connections.Herdr
	case TunnelChannel:
		state = &worker.Connections.Tunnel
	default:
		return Task{}, ErrInvalidTransition
	}
	if observation.At.Before(state.LastAttempt) {
		return Task{}, ErrInvalidTransition
	}
	state.LastAttempt = observation.At.UTC()
	state.LastError = observation.Error
	if observation.Error == "" {
		state.Status, state.LastSuccess = ConnectionReachable, state.LastAttempt
		state.Failures, state.NextRetry = 0, time.Time{}
	} else {
		state.Status = ConnectionUnreachable
		state.Failures = min(state.Failures+1, 31)
		state.NextRetry = state.LastAttempt.Add(connectionBackoff(state.Failures))
	}
	return newTransition(snapshot)
}
