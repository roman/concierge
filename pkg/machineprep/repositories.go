package machineprep

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/sshcommand"
)

var ErrPreparationUnconfirmed = errors.New(
	"remote preparation is unconfirmed; retain existing files and inspect the owned base, repository origins, and preparation lock before retrying",
)

type Preparer struct{ Runner sshcommand.Runner }

func (preparer Preparer) PrepareRepositories(
	ctx context.Context,
	machine herdr.SavedMachine,
	configuration Configuration,
) error {
	if preparer.Runner == nil || !machine.Enabled || machine.ID == "" ||
		configuration.Target != machine.Target {
		return errors.New("preparation requires the configured saved machine identity and target")
	}
	if err := configuration.Validate(); err != nil {
		return err
	}
	command := []string{"sh", "-s", "--", configuration.Base, machine.ID}
	for _, repository := range configuration.Repositories {
		command = append(command, repository.Name, repository.URL)
	}
	arguments, err := sshcommand.TrustedBatchArguments(machine.Target, command...)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	output, _, err := preparer.Runner.Run(
		ctx,
		arguments,
		strings.NewReader(prepareRepositoriesScript),
	)
	if err != nil || ctx.Err() != nil ||
		strings.TrimSpace(string(output)) != "status: repositories-ready" {
		return ErrPreparationUnconfirmed
	}
	return nil
}

const prepareRepositoriesScript = `set -eu
umask 077
base=$1
identity=$2
shift 2
[ ! -L "$base" ] || exit 1
mkdir -p "$base"
[ "$(cd -P "$base" && pwd)" = "$base" ] || exit 1
marker="$base/machine-owner"
if [ -e "$marker" ] || [ -L "$marker" ]; then
    [ -f "$marker" ] && [ ! -L "$marker" ] || exit 1
    [ "$(cat "$marker")" = "$identity" ] || exit 1
else
    for entry in "$base"/* "$base"/.[!.]* "$base"/..?*; do
        if [ -e "$entry" ] || [ -L "$entry" ]; then exit 1; fi
    done
    (set -C; printf '%s\n' "$identity" > "$marker")
fi
chmod 700 "$base"
mkdir "$base/.prepare-lock" || exit 1
temporary=
cleanup() {
    if [ -n "$temporary" ]; then rm -rf "$temporary"; fi
    rmdir "$base/.prepare-lock"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM
for directory in "$base/tasks" "$base/repositories"; do
    [ ! -L "$directory" ] || exit 1
    if [ -e "$directory" ]; then [ -d "$directory" ] || exit 1; else mkdir "$directory"; fi
done
export GIT_TERMINAL_PROMPT=0
export GIT_SSH_COMMAND='ssh -S none -oBatchMode=yes -oStrictHostKeyChecking=yes -oUpdateHostKeys=no -oForwardAgent=no -oClearAllForwardings=yes'
while [ "$#" -gt 0 ]; do
    name=$1
    source=$2
    shift 2
    repository="$base/repositories/$name.git"
    [ ! -L "$repository" ] || exit 1
    if [ -e "$repository" ]; then
        [ -d "$repository" ] || exit 1
        [ "$(git -C "$repository" rev-parse --is-bare-repository)" = true ] || exit 1
        [ "$(git -C "$repository" rev-parse --absolute-git-dir)" = "$repository" ] || exit 1
        [ "$(git -C "$repository" config --get remote.origin.url)" = "$source" ] || exit 1
    else
        temporary=$(mktemp -d "$base/repositories/.clone.XXXXXX")
        git clone --bare -- "$source" "$temporary/repository"
        mv "$temporary/repository" "$repository"
        rmdir "$temporary"
        temporary=
    fi
    git -C "$repository" fetch --no-tags origin '+refs/heads/*:refs/remotes/origin/*'
    git -C "$repository" remote set-head origin --auto >&2
done
printf 'status: repositories-ready\n'
`
