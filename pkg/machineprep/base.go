package machineprep

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/sshcommand"
)

// ResolveBase reads existing ancestors without creating or claiming a directory.
func (preparer Preparer) ResolveBase(
	ctx context.Context,
	machine herdr.SavedMachine,
	authentication sshcommand.Authentication,
	configured string,
) (string, error) {
	if preparer.Runner == nil || !machine.Enabled || machine.ID == "" ||
		(configured != "" && !validBase(configured)) {
		return "", errors.New(
			"base resolution requires an enabled machine and a clean absolute base",
		)
	}
	arguments, err := sshcommand.TrustedAuthenticationArguments(machine.Target, authentication,
		"sh", "-c", resolveBaseScript, "concierge-base-resolve", configured)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	output, diagnostics, err := preparer.Runner.Run(ctx, arguments, nil)
	resolved := strings.TrimSuffix(string(output), "\n")
	if err != nil || ctx.Err() != nil || len(output) > 4096 || !validBase(resolved) {
		cause := strings.TrimSpace(string(diagnostics))
		if len(cause) > 512 {
			cause = cause[:512]
		}
		return "", fmt.Errorf(
			"resolve remote base %q: %w; %s",
			configured,
			ErrPreparationUnconfirmed,
			cause,
		)
	}
	return resolved, nil
}

func validBase(base string) bool {
	return base != "/" && path.IsAbs(base) && path.Clean(base) == base &&
		strings.IndexFunc(base, unicode.IsControl) < 0
}

const resolveBaseScript = `set -eu
base=${1:-"$HOME/.local/share/concierge"}
suffix=
while [ ! -d "$base" ]; do
    if [ -e "$base" ] || [ -L "$base" ]; then
        printf 'base prefix is not a directory: %s\n' "$base" >&2
        exit 1
    fi
    suffix="/$(basename "$base")$suffix"
    base=$(dirname "$base")
done
physical=$(cd -P "$base" && pwd)
if [ "$physical" = / ]; then physical=; fi
printf '%s%s\n' "$physical" "$suffix"
`
