package sshcommand

import (
	"reflect"
	"slices"
	"testing"
)

func TestForwardingRequiresExplicitAuthenticationMode(t *testing.T) {
	for _, mode := range []Authentication{"", RemoteCredentials, ForwardedAgent} {
		t.Run(string(mode), func(t *testing.T) {
			arguments, err := TrustedAuthenticationArguments(
				"saved-target",
				mode,
				"git",
				"ls-remote",
			)
			if err != nil {
				t.Fatal(err)
			}
			want := "ForwardAgent=no"
			if mode == ForwardedAgent {
				want = "ForwardAgent=yes"
			}
			if !slices.Contains(arguments, want) {
				t.Fatalf("missing %s: %v", want, arguments)
			}
			if !slices.Contains(arguments, "StrictHostKeyChecking=yes") ||
				!slices.Contains(arguments, "UpdateHostKeys=no") {
				t.Fatalf("lost host trust: %v", arguments)
			}
			if !reflect.DeepEqual(
				arguments[len(arguments)-3:],
				[]string{"--", "saved-target", "'git' 'ls-remote'"},
			) {
				t.Fatalf("changed command scope: %v", arguments)
			}
		})
	}
	if _, err := TrustedAuthenticationArguments("saved-target", "unknown", "true"); err == nil {
		t.Fatal("unknown authentication mode accepted")
	}
}

func TestForwardedAgentRequiresAvailablePrimaryAgent(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	if err := CheckPrimaryAgent(t.Context()); err == nil {
		t.Fatal("missing primary agent accepted")
	}
}
