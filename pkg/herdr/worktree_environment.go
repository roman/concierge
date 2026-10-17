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

type WorktreeEnvironment string

const (
	PlainWorktree  WorktreeEnvironment = "plain"
	DirenvWorktree WorktreeEnvironment = "direnv"
	NixWorktree    WorktreeEnvironment = "nix"
)

// DetectWorktreeEnvironment runs on the machine that owns the checkout.
func DetectWorktreeEnvironment(ctx context.Context, worktree string) (WorktreeEnvironment, error) {
	return inspectWorktreeEnvironment(
		ctx,
		worktree,
		[]string{"--extra-experimental-features", "nix-command flakes"},
	)
}

func inspectWorktreeEnvironment(
	ctx context.Context,
	worktree string,
	nixOptions []string,
) (WorktreeEnvironment, error) {
	if _, err := os.Stat(filepath.Join(worktree, ".envrc")); err == nil {
		return DirenvWorktree, nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(worktree, "flake.nix")); os.IsNotExist(err) {
		return PlainWorktree, nil
	} else if err != nil {
		return "", err
	}
	architecture := map[string]string{"amd64": "x86_64", "arm64": "aarch64"}[runtime.GOARCH]
	if architecture == "" || (runtime.GOOS != "linux" && runtime.GOOS != "darwin") {
		return "", fmt.Errorf("unsupported Nix platform %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	path := strings.ReplaceAll(strconv.Quote(worktree), "${", `\${`)
	expression := fmt.Sprintf(
		"(builtins.getFlake %s) ? devShells.%s-%s.default",
		path,
		architecture,
		runtime.GOOS,
	)
	arguments := append([]string{"eval"}, nixOptions...)
	arguments = append(arguments, "--json", "--impure", "--expr", expression)
	output, err := exec.CommandContext(ctx, "nix", arguments...).
		Output()
	if err != nil {
		return "", fmt.Errorf("inspect worktree devshell: %w", err)
	}
	var available bool
	if err := json.Unmarshal(output, &available); err != nil {
		return "", fmt.Errorf("inspect worktree devshell: %w", err)
	}
	if !available {
		return PlainWorktree, nil
	}
	return NixWorktree, nil
}

func worktreeEnvironment(ctx context.Context, worktree string) ([]string, error) {
	environment, err := inspectWorktreeEnvironment(ctx, worktree, nil)
	if err != nil {
		return nil, err
	}
	return worktreeEnvironmentPrefix(worktree, environment)
}

func worktreeEnvironmentPrefix(worktree string, environment WorktreeEnvironment) ([]string, error) {
	switch environment {
	case PlainWorktree:
		return nil, nil
	case DirenvWorktree:
		return []string{"direnv", "exec", worktree}, nil
	case NixWorktree:
		return []string{"nix", "develop", worktree, "-c"}, nil
	default:
		return nil, fmt.Errorf("unknown worktree environment %q", environment)
	}
}

// StartRemoteInWorktree uses the environment verified by the native remote helper.
func (client Client) StartRemoteInWorktree(
	ctx context.Context,
	paneID, name, kind, worktree string,
	arguments []string,
	environment WorktreeEnvironment,
) error {
	if client.Machine() == "" || !filepath.IsAbs(worktree) ||
		filepath.Clean(worktree) != worktree ||
		strings.ContainsAny(worktree, "\x00\r\n") {
		return fmt.Errorf("remote startup requires a bound machine and absolute worktree")
	}
	if kind != "codex" && kind != "claude" {
		return fmt.Errorf("unsupported worker harness %q", kind)
	}
	prefix, err := worktreeEnvironmentPrefix(worktree, environment)
	if err != nil {
		return err
	}
	if environment == NixWorktree {
		prefix = []string{
			"nix",
			"develop",
			"--extra-experimental-features",
			"nix-command flakes",
			worktree,
			"-c",
		}
	}
	return client.startWithEnvironment(ctx, paneID, name, kind, arguments, prefix)
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
