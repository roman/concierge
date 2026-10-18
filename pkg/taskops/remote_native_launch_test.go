package taskops

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/sshcommand"
)

func TestRemoteNativeLaunchExposesOnlySelectedAgentSocket(t *testing.T) {
	for _, mode := range []sshcommand.Authentication{"", sshcommand.RemoteCredentials, sshcommand.ForwardedAgent} {
		environment := remoteLaunchEnvironment{
			Task:           "task",
			Generation:     "generation",
			Directory:      "/remote/task",
			Authentication: mode,
		}
		variables, err := environment.variables()
		if err != nil {
			t.Fatal(err)
		}
		if mode != sshcommand.ForwardedAgent {
			if _, found := variables["SSH_AUTH_SOCK"]; found {
				t.Fatal("remote credentials selected a forwarded socket")
			}
			continue
		}
		if variables["SSH_AUTH_SOCK"] != "/remote/task/agent.sock" {
			t.Fatalf("wrong agent socket: %v", variables)
		}
		for _, harness := range []string{"codex", "claude"} {
			arguments, err := remoteNativeLaunchArguments(harness, nil, "protocol", environment)
			if err != nil || !strings.Contains(strings.Join(arguments, " "), "SSH_AUTH_SOCK") {
				t.Fatalf("missing %s agent environment: %v %v", harness, arguments, err)
			}
		}
		if _, err := remoteNativeLaunchArguments("codex", []string{"-c", "shell_environment_policy.set.SSH_AUTH_SOCK=\"/foreign\""}, "protocol", environment); err == nil {
			t.Fatal("caller replaced the selected agent socket")
		}
	}
}

func TestRemoteNativeLaunchArgumentsKeepTransportInShellEnvironment(t *testing.T) {
	for _, harness := range []string{"codex", "claude"} {
		t.Run(harness, func(t *testing.T) {
			environment := remoteLaunchEnvironment{
				Task:       "task-one",
				Generation: "generation-one",
				Directory:  "/remote/task directory",
			}
			original := []string{"--model", "selected-model"}
			arguments, err := remoteNativeLaunchArguments(
				harness,
				original,
				"keep the native instructions",
				environment,
			)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(original, []string{"--model", "selected-model"}) {
				t.Fatal("changed reusable native arguments")
			}
			if arguments[0] != "--add-dir" || arguments[1] != environment.Directory ||
				arguments[len(arguments)-1] != "keep the native instructions" {
				t.Fatalf("native scope or prompt changed: %v", arguments)
			}
			variables := map[string]string{
				"CONCIERGE_SPAWN_GENERATION":   environment.Generation,
				"CONCIERGE_WORKER_TASK":        environment.Task,
				"CONCIERGE_WORKER_SOCKET":      environment.Directory + "/worker.sock",
				"CONCIERGE_WORKER_SECRET_FILE": environment.Directory + "/secret",
			}
			if harness == "claude" {
				var settings struct {
					Env map[string]string `json:"env"`
				}
				if arguments[2] != "--settings" ||
					json.Unmarshal([]byte(arguments[3]), &settings) != nil ||
					!reflect.DeepEqual(settings.Env, variables) {
					t.Fatalf("native Claude environment: %v", arguments)
				}
				return
			}
			joined := strings.Join(arguments, "\n")
			for key, value := range variables {
				if !strings.Contains(joined, "shell_environment_policy.set."+key+"=\""+value+"\"") {
					t.Fatalf("missing shell variable %s: %v", key, arguments)
				}
			}
			if strings.Contains(joined, "dangerously") || strings.Contains(joined, "--sandbox") ||
				strings.Contains(joined, "--ask-for-approval") {
				t.Fatal("changed native permissions")
			}
		})
	}
}

func TestRemoteNativeLaunchRejectsInvalidContextAndPolicyOverrides(t *testing.T) {
	for _, environment := range []remoteLaunchEnvironment{
		{Task: "../foreign", Generation: "generation", Directory: "/remote/task"},
		{Task: "task", Generation: "", Directory: "/remote/task"},
		{Task: "task", Generation: "generation", Directory: "/"},
		{Task: "task", Generation: "generation", Directory: "relative/task"},
	} {
		if _, err := remoteNativeLaunchArguments("codex", nil, "protocol", environment); err == nil {
			t.Fatal("accepted invalid remote launch context")
		}
	}
	environment := remoteLaunchEnvironment{
		Task:       "task",
		Generation: "generation",
		Directory:  "/remote/task",
	}
	if _, err := remoteNativeLaunchArguments("codex", []string{"-c", "shell_environment_policy.set.CONCIERGE_WORKER_SOCKET=\"/foreign\""}, "protocol", environment); err == nil {
		t.Fatal("accepted a caller transport override")
	}
}
