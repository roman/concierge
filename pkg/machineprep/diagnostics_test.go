package machineprep

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type failedPreparation struct{}

func (failedPreparation) Run(context.Context, []string, io.Reader) ([]byte, []byte, error) {
	return nil, []byte(
			strings.Repeat("older diagnostic\n", 1000) + "error: cache download failed\n",
		), errors.New(
			"exit status 37",
		)
}

func TestPreparationFailureRetainsCauseAndBoundedDiagnosticTail(t *testing.T) {
	configuration := Configuration{
		Target: "dev-remote", Base: "/remote/base", Agent: "codex",
		Repositories: []Repository{{Name: "concierge", URL: "ssh://git.test/concierge.git"}},
	}
	preparer := Preparer{Runner: failedPreparation{}}
	for _, phase := range []string{"repositories", "client build"} {
		t.Run(phase, func(t *testing.T) {
			var err error
			if phase == "repositories" {
				err = preparer.PrepareRepositories(t.Context(), testMachine(), configuration)
			} else {
				_, err = preparer.BuildClient(t.Context(), testMachine(), configuration,
					strings.Repeat("a", 40), strings.NewReader("bundle"))
			}
			if !errors.Is(err, ErrPreparationUnconfirmed) ||
				!strings.Contains(err.Error(), "exit status 37") ||
				!strings.Contains(err.Error(), "error: cache download failed") ||
				!strings.Contains(err.Error(), phase) || len(err.Error()) > 5000 {
				t.Fatalf("preparation lost its bounded cause: %v", err)
			}
		})
	}
}
