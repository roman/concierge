package sshcommand

import (
	"strings"
	"testing"
)

func TestNativeSSHPolicyRejectsCredentialAndSocketForwarding(t *testing.T) {
	safe := "hostname example.invalid\nforwardagent no\nforwardx11 no\ntunnel false\nsendenv LANG LC_*\n"
	for _, test := range []struct {
		name, configuration string
		accepted            bool
	}{
		{"safe", safe, true},
		{"agent", strings.ReplaceAll(safe, "forwardagent no", "forwardagent yes"), false},
		{"agent socket", strings.ReplaceAll(safe, "forwardagent no", "forwardagent /private/agent.sock"), false},
		{"X11", strings.ReplaceAll(safe, "forwardx11 no", "forwardx11 yes"), false},
		{"worker-only bypass", safe + "remoteforward /remote/socket /primary/host.sock\n", false},
		{"local forward", safe + "localforward 8080 localhost:80\n", false},
		{"dynamic forward", safe + "dynamicforward 8080\n", false},
		{"tunnel", strings.ReplaceAll(safe, "tunnel false", "tunnel yes"), false},
		{"inline environment", safe + "setenv OPENAI_API_KEY=private\n", false},
		{"incomplete", "hostname example.invalid\n", false},
		{"duplicate", safe + "forwardagent no\n", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateNativePolicy([]byte(test.configuration))
			if (err == nil) != test.accepted {
				t.Fatalf("accepted=%v error=%v", test.accepted, err)
			}
			if err != nil && strings.Contains(err.Error(), "private") {
				t.Fatal("policy error copied credential content")
			}
		})
	}
}
