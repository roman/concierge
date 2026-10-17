package hostservice

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/machineprep"
)

type preparationCatalog struct{}

func (preparationCatalog) Resolve(context.Context, string) (herdr.SavedMachine, error) {
	return herdr.SavedMachine{
		ID:      "saved-id",
		Label:   "baker",
		Target:  "baker",
		Session: "default",
		Enabled: true,
	}, nil
}

type preparationSSH struct {
	loginFailure error
	mutations    int
	bundled      bool
}

func (runner *preparationSSH) Run(
	_ context.Context,
	arguments []string,
	input io.Reader,
) ([]byte, []byte, error) {
	command := arguments[len(arguments)-1]
	if strings.Contains(command, "concierge-client-build") {
		data, err := io.ReadAll(input)
		runner.bundled = strings.HasPrefix(string(data), "# v2 git bundle") ||
			strings.HasPrefix(string(data), "# v3 git bundle")
		runner.mutations++
		return []byte("status: client-ready"), nil, err
	}
	if strings.Contains(command, "'sh' '-s'") {
		runner.mutations++
		return []byte("status: repositories-ready"), nil, nil
	}
	if strings.Contains(command, "'uname' '-s'") {
		return []byte("Darwin"), nil, nil
	}
	if strings.Contains(command, "'uname' '-m'") {
		return []byte("arm64"), nil, nil
	}
	if strings.Contains(command, "'login' 'status'") {
		return nil, nil, runner.loginFailure
	}
	return []byte("version"), nil, nil
}

func TestMachinePreparationChecksCredentialsBeforeMutationAndBuildsExactSource(t *testing.T) {
	for _, kind := range []string{"ready", "missing login"} {
		t.Run(kind, func(t *testing.T) {
			server := workerHost(t, 3)
			server.preparationMachines = preparationCatalog{}
			runner := &preparationSSH{}
			if kind == "missing login" {
				runner.loginFailure = errors.New("not logged in")
			}
			server.preparationSSH = runner
			if err := os.MkdirAll(filepath.Join(server.config.Root, "config"), 0700); err != nil {
				t.Fatal(err)
			}
			configuration := `{"saved-id":{"target":"baker","base":"/remote/base","agent":"codex","repositories":[{"name":"concierge","url":"ssh://git.test/concierge.git"}]}}`
			if err := os.WriteFile(filepath.Join(server.config.Root, "config", "machines.json"), []byte(configuration), 0600); err != nil {
				t.Fatal(err)
			}
			source := t.TempDir()
			runGit := func(arguments ...string) string {
				t.Helper()
				output, err := exec.Command("git", append([]string{"-C", source}, arguments...)...).
					CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %s %v", arguments, output, err)
				}
				return strings.TrimSpace(string(output))
			}
			runGit("init", "-b", "main")
			runGit("remote", "add", "origin", "ssh://git.test/concierge.git")
			runGit(
				"-c",
				"user.name=Test",
				"-c",
				"user.email=test@example.invalid",
				"commit",
				"--allow-empty",
				"-m",
				"Client",
			)
			response, err := server.prepareMachine(
				context.Background(),
				MachinePreparationRequest{Label: "baker", ClientSource: source},
			)
			if kind == "missing login" {
				if err == nil || !strings.Contains(err.Error(), "codex login") ||
					runner.mutations != 0 {
					t.Fatalf("missing login mutated machine: %+v %v", response, err)
				}
				return
			}
			if err != nil || response.PreparedMachine == nil ||
				response.PreparedMachine.Client.Commit != runGit("rev-parse", "HEAD") ||
				runner.mutations != 2 ||
				!runner.bundled {
				t.Fatalf("incomplete preparation: %+v %v", response, err)
			}
		})
	}
}

func TestMachinePreparationRequiresPrimaryAuthority(t *testing.T) {
	_, _, socket := fixture(t, nil)
	response, err := Call(
		context.Background(),
		socket,
		Request{
			Operation: PrepareMachine,
			MachinePreparation: &MachinePreparationRequest{
				Label:        "baker",
				ClientSource: "/primary/concierge",
			},
		},
	)
	if err != nil || response.Outcome != Unauthorized {
		t.Fatalf("response=%+v error=%v", response, err)
	}
}

func TestMachinePreparationRejectsUnrelatedPayloadsAndResults(t *testing.T) {
	request := Request{
		Operation: PrepareMachine,
		MachinePreparation: &MachinePreparationRequest{
			Label:        "baker",
			ClientSource: "/primary/concierge",
		},
	}
	if err := validateRequest(request); err != nil {
		t.Fatal(err)
	}
	response := Response{
		Outcome: Completed,
		PreparedMachine: &PreparedMachineResult{
			MachineID: "saved-id",
			Target:    "baker",
			Client: machineprep.Client{
				Commit:     strings.Repeat("a", 40),
				Executable: "/remote/base/clients/client/bin/concierge",
			},
		},
	}
	if err := validateResponse(response, request); err != nil {
		t.Fatal(err)
	}
	response.PreparedMachine.Client.Commit = "HEAD"
	if err := validateResponse(response, request); err == nil {
		t.Fatal("accepted mutable prepared client identity")
	}
	response.PreparedMachine.Client.Commit = strings.Repeat("a", 40)
	response.Control = &ControlResult{}
	if err := validateResponse(response, request); err == nil {
		t.Fatal("accepted unrelated operation result")
	}
	request.Intake = intakeRequest().Intake
	if err := validateRequest(request); err == nil {
		t.Fatal("accepted unrelated operation payload")
	}
	request.Intake = nil
	request.MachinePreparation.ClientSource = "relative"
	if err := validateRequest(request); err == nil {
		t.Fatal("accepted relative primary client source")
	}
}
