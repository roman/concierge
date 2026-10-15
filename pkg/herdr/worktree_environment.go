package herdr

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// WorktreeStartupTimeout includes cold devshell evaluation before native startup.
const WorktreeStartupTimeout = 2 * time.Minute

func worktreeEnvironment(ctx context.Context, worktree string) ([]string, error) {
	if _, err := os.Stat(filepath.Join(worktree, ".envrc")); err == nil {
		return []string{"direnv", "exec", worktree}, nil
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(worktree, "flake.nix")); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	architecture := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	if architecture == "" || (runtime.GOOS != "linux" && runtime.GOOS != "darwin") {
		return nil, fmt.Errorf("unsupported Nix platform %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	path := strings.ReplaceAll(strconv.Quote(worktree), "${", `\${`)
	expression := fmt.Sprintf(
		"(builtins.getFlake %s) ? devShells.%s-%s.default",
		path,
		architecture,
		runtime.GOOS,
	)
	output, err := exec.CommandContext(ctx, "nix", "eval", "--json", "--impure", "--expr", expression).
		Output()
	if err != nil {
		return nil, fmt.Errorf("inspect worktree devshell: %w", err)
	}
	var available bool
	if err := json.Unmarshal(output, &available); err != nil {
		return nil, fmt.Errorf("inspect worktree devshell: %w", err)
	}
	if !available {
		return nil, nil
	}
	return []string{"nix", "develop", worktree, "-c"}, nil
}

// StartInWorktree applies the environment to the native executable after pane shell startup.
func (client Client) StartInWorktree(
	ctx context.Context,
	paneID, name, kind, worktree string,
	arguments []string,
) error {
	if kind != "codex" && kind != "claude" {
		return fmt.Errorf("unsupported worker harness %q", kind)
	}
	prefix, err := worktreeEnvironment(ctx, worktree)
	if err != nil {
		return err
	}
	return client.startWithEnvironment(ctx, paneID, name, kind, arguments, prefix)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func (client Client) prepareEnvironment(
	ctx context.Context,
	paneID, kind string,
	prefix []string,
) error {
	if len(prefix) == 0 {
		return nil
	}
	parts := make([]string, len(prefix))
	for index, argument := range prefix {
		parts[index] = shellQuote(argument)
	}
	// Herdr owns native arguments and readiness detection. A one-use shell function
	// wraps its canonical executable without changing either contract.
	marker := "concierge-environment-" + rand.Text()
	command := fmt.Sprintf("%s() { unset -f %s; command %s %s \"$@\"; }; printf '%%s\\n' %s",
		kind, kind, strings.Join(parts, " "), shellQuote(kind), shellQuote(marker))
	if _, err := client.shared().Run(ctx, "pane", "run", paneID, command); err != nil {
		return err
	}
	_, err := client.shared().Run(
		ctx,
		"pane",
		"wait-output",
		paneID,
		"--regex",
		"^"+marker+"$",
		"--timeout",
		"10000",
	)
	return err
}
