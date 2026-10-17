package machineprep

import (
	"context"
	"encoding/hex"
	"errors"
	"path"
	"strings"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/sshcommand"
)

var ErrClientUnavailable = errors.New(
	"prepared client is unavailable or unconfirmed; check SSH and run machine prepare through the primary",
)

func (preparer Preparer) ReadClient(
	ctx context.Context,
	machine herdr.SavedMachine,
	configuration Configuration,
) (Client, error) {
	if preparer.Runner == nil || !machine.Enabled || machine.ID == "" ||
		configuration.Target != machine.Target {
		return Client{}, ErrClientUnavailable
	}
	if err := configuration.Validate(); err != nil {
		return Client{}, err
	}
	arguments, err := sshcommand.TrustedBatchArguments(
		machine.Target,
		"sh",
		"-c",
		readClientScript,
		"concierge-client-status",
		configuration.Base,
		machine.ID,
	)
	if err != nil {
		return Client{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	output, _, err := preparer.Runner.Run(ctx, arguments, nil)
	if err != nil || ctx.Err() != nil {
		return Client{}, ErrClientUnavailable
	}
	commit := strings.TrimSpace(string(output))
	decoded, err := hex.DecodeString(commit)
	if err != nil || len(decoded) != 20 || strings.ToLower(commit) != commit {
		return Client{}, ErrClientUnavailable
	}
	return Client{
		Commit:     commit,
		Executable: path.Join(configuration.Base, "clients", commit, "bin", "concierge"),
	}, nil
}

const readClientScript = `set -eu
base=$1
identity=$2
[ -d "$base" ] && [ ! -L "$base" ] || exit 1
[ "$(cd -P "$base" && pwd)" = "$base" ] || exit 1
[ -f "$base/machine-owner" ] && [ ! -L "$base/machine-owner" ] || exit 1
[ "$(cat "$base/machine-owner")" = "$identity" ] || exit 1
[ -f "$base/client-commit" ] && [ ! -L "$base/client-commit" ] || exit 1
commit=$(cat "$base/client-commit")
case "$commit" in ''|*[!0-9a-f]*) exit 1;; esac
[ "${#commit}" -eq 40 ] || exit 1
[ ! -L "$base/clients" ] || exit 1
client="$base/clients/$commit/bin/concierge"
[ -x "$client" ] || exit 1
"$client" --version >/dev/null
printf '%s\n' "$commit"
`
