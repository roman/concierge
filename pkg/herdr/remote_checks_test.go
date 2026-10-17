package herdr

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type fixedMachineResolver struct{ machine SavedMachine }

func (resolver fixedMachineResolver) Resolve(context.Context, string) (SavedMachine, error) {
	return resolver.machine, nil
}

type remoteCheckRunner struct {
	response runResponse
	args     []string
	deadline time.Time
}

func (runner *remoteCheckRunner) Run(ctx context.Context, args ...string) ([]byte, []byte, error) {
	runner.args = args
	runner.deadline, _ = ctx.Deadline()
	return runner.response.stdout, runner.response.stderr, runner.response.err
}

func TestSSHRemoteChecksUseFixedCommandsAndDeadline(t *testing.T) {
	for _, test := range []struct {
		check   RemoteCheck
		command string
	}{
		{SessionInventory, "'herdr' '--session' 'work' 'session' 'list' '--json'"},
		{ForegroundViewer, "'herdr' '--session' 'work' 'terminal' 'title' 'clear'"},
	} {
		t.Run(string(test.check), func(t *testing.T) {
			runner := &remoteCheckRunner{response: runResponse{stdout: []byte("response")}}
			checks := SSHRemoteChecks{
				Machines: fixedMachineResolver{
					SavedMachine{
						ID:      "machine-1",
						Target:  "user@baker",
						Session: "work",
						Enabled: true,
					},
				},
				Runner: runner,
			}
			stdout, _, err := checks.RunCheck(context.Background(), "machine-1", "work", test.check)
			if err != nil || string(stdout) != "response" {
				t.Fatalf("check: %q %v", stdout, err)
			}
			want := []string{
				"-o",
				"StrictHostKeyChecking=yes",
				"-o",
				"UpdateHostKeys=no",
				"-S",
				"none",
				"-o",
				"BatchMode=yes",
				"-o",
				"ExitOnForwardFailure=yes",
				"-o",
				"ForwardAgent=no",
				"-o",
				"ClearAllForwardings=yes",
				"-o",
				"ForwardX11=no",
				"-o",
				"ForwardX11Trusted=no",
				"-o",
				"SetEnv=CONCIERGE_SSH_TRANSPORT=1",
				"--",
				"user@baker",
				test.command,
			}
			if !reflect.DeepEqual(runner.args, want) {
				t.Fatalf("arguments = %q, want %q", runner.args, want)
			}
			if runner.deadline.IsZero() || time.Until(runner.deadline) > 15*time.Second {
				t.Fatalf("missing bounded deadline: %v", runner.deadline)
			}
		})
	}
}

func TestSSHRemoteCheckFailureIsUnreachable(t *testing.T) {
	for _, failure := range []error{errors.New("exit status 255"), context.DeadlineExceeded} {
		runner := &remoteCheckRunner{
			response: runResponse{
				stdout: []byte(`{"result":{"reason":"no_foreground_client"}}`),
				err:    failure,
			},
		}
		checks := SSHRemoteChecks{
			Machines: fixedMachineResolver{
				SavedMachine{Target: "baker", Session: "work", Enabled: true},
			},
			Runner: runner,
		}
		stdout, _, err := checks.RunCheck(context.Background(), "baker", "work", ForegroundViewer)
		if !errors.Is(err, ErrUnreachable) || len(stdout) != 0 {
			t.Fatalf("ambiguous response accepted: %s, %v", stdout, err)
		}
	}
}

func TestSSHRemoteChecksRejectChangedSessionAndUnknownOperation(t *testing.T) {
	for _, test := range []struct {
		session string
		check   RemoteCheck
	}{
		{"other", SessionInventory}, {"work", RemoteCheck("pane-close")},
	} {
		runner := &remoteCheckRunner{}
		checks := SSHRemoteChecks{
			Machines: fixedMachineResolver{
				SavedMachine{Target: "baker", Session: "work", Enabled: true},
			},
			Runner: runner,
		}
		if _, _, err := checks.RunCheck(context.Background(), "baker", test.session, test.check); err == nil ||
			runner.args != nil {
			t.Fatalf("invalid check executed: %q %v", runner.args, err)
		}
	}
}
