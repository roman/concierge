package machineprep

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/sshcommand"
)

type Client struct {
	Commit     string `json:"commit"`
	Executable string `json:"executable"`
}

// BuildClient imports an immutable primary Git bundle before building with the
// remote Nix. Unpublished task branches therefore need no repository push.
func (preparer Preparer) BuildClient(
	ctx context.Context,
	machine herdr.SavedMachine,
	configuration Configuration,
	commit string,
	bundle io.Reader,
) (Client, error) {
	if preparer.Runner == nil || bundle == nil || !machine.Enabled || machine.ID == "" ||
		configuration.Target != machine.Target {
		return Client{}, errors.New(
			"client build requires the configured saved machine and source bundle",
		)
	}
	if err := configuration.Validate(); err != nil {
		return Client{}, err
	}
	if decoded, err := hex.DecodeString(commit); err != nil || len(decoded) != 20 ||
		strings.ToLower(commit) != commit {
		return Client{}, errors.New("client build requires an immutable lowercase Git commit ID")
	}
	checkout := path.Join(configuration.Base, "client-sources", commit)
	flake := url.URL{
		Scheme:   "git+file",
		Path:     checkout,
		RawQuery: url.Values{"rev": {commit}}.Encode(),
		Fragment: "concierge",
	}
	arguments, err := sshcommand.TrustedBatchArguments(
		machine.Target,
		"sh",
		"-c",
		buildClientScript,
		"concierge-client-build",
		configuration.Base,
		machine.ID,
		commit,
		flake.String(),
	)
	if err != nil {
		return Client{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Minute)
	defer cancel()
	output, _, err := preparer.Runner.Run(ctx, arguments, bundle)
	if err != nil || ctx.Err() != nil ||
		strings.TrimSpace(string(output)) != "status: client-ready" {
		return Client{}, ErrPreparationUnconfirmed
	}
	return Client{
		Commit:     commit,
		Executable: path.Join(configuration.Base, "clients", commit, "bin", "concierge"),
	}, nil
}

const buildClientScript = `set -eu
umask 077
base=$1
identity=$2
commit=$3
flake=$4
[ -d "$base" ] && [ ! -L "$base" ] || exit 1
[ "$(cd -P "$base" && pwd)" = "$base" ] || exit 1
[ -f "$base/machine-owner" ] && [ ! -L "$base/machine-owner" ] || exit 1
[ "$(cat "$base/machine-owner")" = "$identity" ] || exit 1
mkdir "$base/.prepare-lock" || exit 1
bundle=
ready=
cleanup() {
    if [ -n "$bundle" ]; then rm -f "$bundle"; fi
    if [ -n "$ready" ]; then rm -f "$ready"; fi
    rmdir "$base/.prepare-lock"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
repository="$base/repositories/concierge.git"
[ -d "$repository" ] && [ ! -L "$base/repositories" ] && [ ! -L "$repository" ] || exit 1
[ "$(git -C "$repository" rev-parse --is-bare-repository)" = true ] || exit 1
[ "$(git -C "$repository" rev-parse --absolute-git-dir)" = "$repository" ] || exit 1
bundle=$(mktemp "$base/.client-bundle.XXXXXX")
cat > "$bundle"
git -C "$repository" bundle verify "$bundle" >&2
git -C "$repository" fetch --no-tags "$bundle" HEAD >&2
[ "$(git -C "$repository" rev-parse FETCH_HEAD)" = "$commit" ] || exit 1
for directory in "$base/client-sources" "$base/clients"; do
    [ ! -L "$directory" ] || exit 1
    if [ -e "$directory" ]; then [ -d "$directory" ] || exit 1; else mkdir "$directory"; fi
done
checkout="$base/client-sources/$commit"
[ ! -L "$checkout" ] || exit 1
if [ ! -e "$checkout" ]; then
    git -C "$repository" worktree add --detach "$checkout" "$commit" >&2
fi
[ -d "$checkout" ] || exit 1
[ "$(cd -P "$checkout" && pwd)" = "$checkout" ] || exit 1
[ "$(git -C "$checkout" rev-parse --path-format=absolute --git-common-dir)" = "$repository" ] || exit 1
[ "$(git -C "$checkout" rev-parse HEAD)" = "$commit" ] || exit 1
[ -z "$(git -C "$checkout" status --porcelain --untracked-files=all --ignored)" ] || exit 1
client="$base/clients/$commit"
if [ -e "$client" ] && [ ! -L "$client" ]; then exit 1; fi
nix build --extra-experimental-features 'nix-command flakes' --no-write-lock-file --out-link "$client" "$flake" >&2
[ -x "$client/bin/concierge" ] || exit 1
"$client/bin/concierge" --version >/dev/null
[ ! -L "$base/client-commit" ] || exit 1
if [ -e "$base/client-commit" ]; then [ -f "$base/client-commit" ] || exit 1; fi
ready=$(mktemp "$base/.client-ready.XXXXXX")
printf '%s\n' "$commit" > "$ready"
mv "$ready" "$base/client-commit"
ready=
printf 'status: client-ready\n'
`
