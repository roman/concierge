package workeraccess

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestRestoreSecretRequiresTheOriginalActiveVerifier(t *testing.T) {
	grant, original, err := Issue("task-one", "generation-one", Operations())
	if err != nil {
		t.Fatal(err)
	}
	restored, err := grant.RestoreSecret(bytes.NewReader(original.Bytes()))
	if err != nil || !bytes.Equal(restored.Bytes(), original.Bytes()) {
		t.Fatal("changed the recovered secret")
	}
	if strings.Contains(fmt.Sprintf("%v", restored), string(original.Bytes())) {
		t.Fatal("logged the recovered secret")
	}
	for _, test := range []struct {
		name  string
		grant Grant
		data  []byte
	}{
		{"different secret", grant, []byte(strings.Repeat("A", 43))},
		{"malformed secret", grant, []byte("short")},
		{"revoked", func() Grant { copy := grant; copy.Revoked = true; return copy }(), original.Bytes()},
		{"invalid binding", func() Grant { copy := grant; copy.Generation = ""; return copy }(), original.Bytes()},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.grant.RestoreSecret(bytes.NewReader(test.data)); err == nil {
				t.Fatal("accepted an unbound secret")
			}
		})
	}
}
