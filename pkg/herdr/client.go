// Package herdr adapts Herdr's agent commands to typed operations.
package herdr

import (
	"context"
	"errors"
	"fmt"

	base "github.com/roman/tincan/pkg/herdr"
)

type AgentState string

const (
	Idle    AgentState = "idle"
	Done    AgentState = "done"
	Working AgentState = "working"
	Blocked AgentState = "blocked"
	Unknown AgentState = "unknown"
)

// SessionReference preserves Herdr's optional native identity metadata.
// The reference alone does not establish a live binding or a fresh launch.
type SessionReference struct {
	Source string `json:"source"`
	Agent  string `json:"agent"`
	Kind   string `json:"kind"`
	Value  string `json:"value"`
}

type Agent struct {
	PaneID           string
	Status           AgentState
	Kind             string
	InteractiveReady bool
	Session          *SessionReference `json:"-"`
}

// IsReadyWithUnknownActivity preserves Codex's native interactive readiness
// when Herdr cannot classify its screen activity.
func (agent Agent) IsReadyWithUnknownActivity() bool {
	return agent.Kind == "codex" && agent.Status == Unknown && agent.InteractiveReady
}

var (
	ErrWorkspaceNotFound = base.ErrWorkspaceNotFound
	ErrNotFound          = base.ErrNotFound
	ErrCommand           = base.ErrCommand
	ErrResponse          = base.ErrResponse
	ErrAgentNotReady     = base.ErrAgentNotReady
	ErrAgentPresent      = errors.New("herdr agent already present")
)

type Runner = base.Runner

type Client struct {
	machine        string
	session        string
	Runner         Runner
	SessionRunner  SessionRunner
	MachineRunner  MachineRunner
	RemoteChecks   RemoteChecks
	Processes      ProcessInspector
	NixDevelopArgs []string
}

func NewClient(executable string) Client {
	if executable == "" {
		executable = "herdr"
	}
	shared := base.NewClient(executable)
	return Client{
		Runner:        shared.Runner,
		SessionRunner: shared.SessionRunner,
		MachineRunner: machineCommandRunner{executable: executable},
		RemoteChecks: SSHRemoteChecks{
			Machines: MachineCatalog{Runner: shared.Runner},
			Runner:   systemSSHCheckRunner{},
		},
		Processes: psInspector{},
	}
}

func (c Client) shared() base.Client {
	return base.Client{Runner: c.Runner, SessionRunner: c.SessionRunner}
}

func (c Client) Get(ctx context.Context, target string) (Agent, error) {
	if target == "" {
		return Agent{}, fmt.Errorf("%w: empty target", ErrResponse)
	}
	stdout, err := c.shared().Run(ctx, "agent", "get", target)
	if err != nil {
		return Agent{}, err
	}
	response, err := base.DecodeResponse[struct {
		Agent struct {
			PaneID           string            `json:"pane_id"`
			Status           AgentState        `json:"agent_status"`
			Kind             string            `json:"agent"`
			InteractiveReady bool              `json:"interactive_ready"`
			Session          *SessionReference `json:"agent_session"`
		} `json:"agent"`
	}](stdout)
	if err != nil {
		return Agent{}, err
	}
	agent := Agent{
		PaneID: response.Agent.PaneID, Status: response.Agent.Status,
		Kind: response.Agent.Kind, InteractiveReady: response.Agent.InteractiveReady,
		Session: response.Agent.Session,
	}
	if agent.PaneID == "" || !validState(agent.Status) {
		return Agent{}, fmt.Errorf("%w: missing pane or state", ErrResponse)
	}
	return agent, nil
}

func validState(state AgentState) bool {
	switch state {
	case Idle, Done, Working, Blocked, Unknown:
		return true
	default:
		return false
	}
}

// Start launches only after Herdr reports no agent in the pane. Herdr verifies
// that the pane is at an interactive shell prompt before launching.
func (c Client) Start(ctx context.Context, paneID, name, kind string) error {
	return c.StartWithArgs(ctx, paneID, name, kind, nil)
}

func (c Client) StartWithArgs(
	ctx context.Context,
	paneID, name, kind string,
	agentArgs []string,
) error {
	return c.startWithEnvironment(ctx, paneID, name, kind, agentArgs, nil)
}

func (c Client) startWithEnvironment(
	ctx context.Context,
	paneID, name, kind string,
	agentArgs, prefix []string,
) error {
	if paneID == "" || name == "" || kind == "" {
		return fmt.Errorf("%w: missing start argument", ErrResponse)
	}
	_, err := c.Get(ctx, paneID)
	if err == nil {
		return ErrAgentPresent
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	if err := c.prepareEnvironment(ctx, paneID, kind, prefix); err != nil {
		return err
	}
	args := []string{"agent", "start", name, "--kind", kind, "--pane", paneID}
	if len(prefix) > 0 {
		args = append(args, "--timeout", fmt.Sprint(nativeStartupTimeout.Milliseconds()))
	}
	if len(agentArgs) > 0 {
		args = append(append(args, "--"), agentArgs...)
	}
	if err := c.runNativeStart(ctx, paneID, args); err != nil {
		return err
	}
	agent, err := c.Get(ctx, name)
	if err != nil {
		return fmt.Errorf("start verification: %w", err)
	}
	// Herdr can mark Codex interactive before it can classify the screen. Its startup
	// accepts that unknown state only for Codex; other agents need a known state.
	if agent.PaneID != paneID || (agent.Status == Unknown && !agent.IsReadyWithUnknownActivity()) {
		return fmt.Errorf("start verification: %w", ErrResponse)
	}
	if agent.Kind != kind {
		return fmt.Errorf("start verification: %w: kind %s", ErrResponse, agent.Kind)
	}
	return nil
}

func (c Client) SendKeys(ctx context.Context, target string, keys ...string) error {
	if target == "" || len(keys) == 0 {
		return fmt.Errorf("%w: missing key target or keys", ErrResponse)
	}
	args := append([]string{"agent", "send-keys", target}, keys...)
	_, err := c.shared().Run(ctx, args...)
	return err
}

func (c Client) ReportPaneTitle(ctx context.Context, paneID, title string) error {
	if !healthAtom(paneID, true) || !healthLabel(title) {
		return fmt.Errorf("%w: invalid pane title or target", ErrResponse)
	}
	_, err := c.shared().
		Run(ctx, "pane", "report-metadata", paneID, "--source", "concierge", "--title", title)
	return err
}

func (c Client) WaitSettled(ctx context.Context, target string) (Agent, error) {
	if target == "" {
		return Agent{}, fmt.Errorf("%w: empty target", ErrResponse)
	}
	if _, err := c.shared().Run(ctx, "agent", "wait", target, "--timeout", "10000"); err != nil {
		return Agent{}, err
	}
	agent, err := c.Get(ctx, target)
	if err != nil {
		return Agent{}, err
	}
	if agent.Status != Idle && agent.Status != Done && agent.Status != Blocked {
		return Agent{}, fmt.Errorf("%w: agent did not settle", ErrResponse)
	}
	return agent, nil
}

// Prompt submits a short notification. Submission does not confirm that the
// agent read it; callers must use their own acknowledgement record.
func (c Client) Prompt(ctx context.Context, target, message string) error {
	return c.shared().Prompt(ctx, target, message)
}
