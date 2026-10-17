package herdr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestMachineCommandsDropLocalSelection(t *testing.T) {
	variables := []string{
		"HERDR_SESSION",
		"HERDR_SOCKET_PATH",
		"HERDR_WORKSPACE_ID",
		"HERDR_TAB_ID",
		"HERDR_PANE_ID",
	}
	script := "#!/bin/sh\n"
	for _, variable := range variables {
		t.Setenv(variable, "local-selection")
		script += "if [ \"${" + variable + "+set}\" = set ]; then printf 'inherited=" + variable + "\\n'; fi\n"
	}
	directory := t.TempDir()
	executable := filepath.Join(directory, "herdr")
	script += "printf 'argument=%s\\n' \"$@\"\n"
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(executable).ForMachine("baker")
	if err != nil {
		t.Fatal(err)
	}
	output, _, err := client.Runner.Run(context.Background(), "pane", "list")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(output), "inherited=") {
		t.Fatalf("inherited local selection: %s", output)
	}
	if !strings.HasSuffix(
		string(output),
		"argument=--machine\nargument=baker\nargument=pane\nargument=list\n",
	) {
		t.Fatalf("missing machine routing: %s", output)
	}
}

func TestMachineBindingValidation(t *testing.T) {
	for _, label := range []string{"", "--session", "baker other", "baker;true", "baker/other"} {
		t.Run(label, func(t *testing.T) {
			if _, err := NewClient("herdr").ForMachine(label); err == nil {
				t.Fatal("accepted invalid machine label")
			}
		})
	}
}

type machineCall struct {
	machine string
	args    []string
}

type scriptedMachineRunner struct {
	responses []healthResponse
	calls     []machineCall
}

func (runner *scriptedMachineRunner) RunMachine(
	_ context.Context,
	machine string,
	args ...string,
) ([]byte, []byte, error) {
	runner.calls = append(runner.calls, machineCall{machine, args})
	if len(runner.responses) == 0 {
		panic("unexpected machine command")
	}
	response := runner.responses[0]
	runner.responses = runner.responses[1:]
	return []byte(response.out), []byte(response.stderr), response.err
}

type forbiddenLocalProcesses struct{ testing *testing.T }

func (processes forbiddenLocalProcesses) Snapshot(context.Context) ([]Process, error) {
	processes.testing.Fatal("remote health inspected the primary process table")
	return nil, nil
}

func TestRemoteHealthUsesMachineProcessInfo(t *testing.T) {
	const pane = `{"result":{"pane":{"pane_id":"pane-1"}}}`
	const agent = `{"result":{"agent":{"pane_id":"pane-1","agent_status":"working"}}}`
	for _, test := range []struct {
		name, process string
		want          EndpointHealth
	}{
		{"agent", `{"result":{"type":"pane_process_info","process_info":{"pane_id":"pane-1","shell_pid":100,"foreground_processes":[{"pid":101,"name":"codex"}]}}}`, Alive},
		{"shell only", `{"result":{"type":"pane_process_info","process_info":{"pane_id":"pane-1","shell_pid":100,"foreground_processes":[{"pid":100,"name":"zsh"}]}}}`, Unreadable},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &scriptedMachineRunner{responses: []healthResponse{
				{out: pane}, {out: agent}, {out: test.process},
			}}
			client := Client{MachineRunner: runner, Processes: forbiddenLocalProcesses{t}}
			target, err := ParseMachineTarget("baker", "pane-1")
			if err != nil {
				t.Fatal(err)
			}
			if status := client.Health(context.Background(), target); status.Endpoint != test.want {
				t.Fatalf("health = %+v, want %s", status, test.want)
			}
			want := []machineCall{
				{"baker", []string{"pane", "get", "pane-1"}},
				{"baker", []string{"agent", "get", "pane-1"}},
				{"baker", []string{"pane", "process-info", "--pane", "pane-1"}},
			}
			if !reflect.DeepEqual(runner.calls, want) {
				t.Fatalf("calls = %+v, want %+v", runner.calls, want)
			}
		})
	}
}

func TestRemoteProbeFailureDoesNotClassifyWorkerAbsent(t *testing.T) {
	runner := &scriptedMachineRunner{responses: []healthResponse{
		{err: errors.New("SSH connection failed")},
		{out: `{"server":{"running":false}}`},
	}}
	target, err := ParseMachineTarget("baker", "pane-1")
	if err != nil {
		t.Fatal(err)
	}
	status := (Client{MachineRunner: runner}).Health(context.Background(), target)
	if status.Endpoint != Unreadable {
		t.Fatalf("health = %+v; an unreachable machine must preserve the worker", status)
	}
}
