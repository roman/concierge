package conciergecli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoteCodexPermissionsFailDuringParsing(t *testing.T) {
	for _, sandbox := range []string{"", "--sandbox=read-only", "--sandbox=danger-full-access"} {
		t.Run(sandbox, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "uncreated-state")
			arguments := append(
				launchArguments(root),
				"--machine=unresolved-machine",
				"--herdr=/missing/herdr",
			)
			if sandbox != "" {
				arguments = append(arguments, "--agent-arg="+sandbox)
			}
			for index, argument := range arguments {
				if argument == "--body-file" {
					arguments[index+1] = "/missing/assignment"
				}
			}
			var output bytes.Buffer
			code := Run(t.Context(), arguments, nil, &output, io.Discard)
			if code != 2 ||
				!strings.Contains(output.String(), "--agent-arg=--sandbox=workspace-write") {
				t.Fatalf("late permission failure: %d %s", code, &output)
			}
			if _, err := os.Stat(root); !os.IsNotExist(err) {
				t.Fatalf("argument validation created state: %v", err)
			}
		})
	}
}

func TestLaunchHelpStatesRemoteCodexSandboxRequirement(t *testing.T) {
	var output bytes.Buffer
	if code := Run(t.Context(), []string{"launch", "--help"}, nil, &output, io.Discard); code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(
		strings.Join(strings.Fields(output.String()), " "),
		"--agent-arg=--sandbox=workspace-write",
	) {
		t.Fatal(output.String())
	}
}
