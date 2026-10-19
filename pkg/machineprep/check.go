// Package machineprep checks and prepares saved SSH machines without provisioning credentials.
package machineprep

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/sshcommand"
)

type Repository struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
	Step   string `json:"step,omitempty"`
}

type Report struct {
	MachineID string  `json:"machine_id"`
	Checks    []Check `json:"checks"`
}

func (report Report) Ready() bool {
	if len(report.Checks) == 0 {
		return false
	}
	for _, check := range report.Checks {
		if check.Status != "ready" {
			return false
		}
	}
	return true
}

type Checker struct {
	Runner         sshcommand.Runner
	Authentication sshcommand.Authentication
}

func (checker Checker) Check(
	ctx context.Context,
	machine herdr.SavedMachine,
	agent string,
	repositories []Repository,
) (Report, error) {
	if checker.Runner == nil || !machine.Enabled || machine.ID == "" ||
		!sshcommand.ValidTarget(machine.Target) {
		return Report{}, errors.New("machine check requires an enabled saved SSH machine")
	}
	if agent != "codex" && agent != "claude" {
		return Report{}, errors.New("agent must be codex or claude")
	}
	if err := validateRepositories(repositories); err != nil {
		return Report{}, err
	}
	if err := checker.Authentication.Validate(); err != nil {
		return Report{}, err
	}
	report := Report{MachineID: machine.ID}
	if checker.Authentication == sshcommand.ForwardedAgent {
		check := Check{Name: "primary-agent", Status: "ready"}
		if err := sshcommand.CheckPrimaryAgent(ctx); err != nil {
			check.Status = "missing"
			check.Step = "On the primary, start or connect to an SSH agent, load the repository key with ssh-add, and ensure the Concierge process inherits SSH_AUTH_SOCK."
			report.Checks = append(report.Checks, check)
			return report, nil
		}
		report.Checks = append(report.Checks, check)
	}
	probe := func(name, step string, command ...string) (string, bool) {
		output, ok := checker.probe(ctx, machine.Target, command...)
		check := Check{Name: name, Status: "ready", Detail: output}
		if !ok {
			check.Status, check.Detail, check.Step = "missing", "", step
		}
		report.Checks = append(report.Checks, check)
		return output, ok
	}
	_, connected := probe(
		"ssh",
		"Run ssh "+sshcommand.Quote(
			machine.Target,
		)+" on the primary; verify the host key and configure the primary's SSH login.",
		"true",
	)
	if !connected {
		return report, nil
	}
	policy := Check{Name: "ssh-policy", Status: "ready"}
	if err := sshcommand.CheckNativePolicy(ctx, checker.Runner, machine.Target); err != nil {
		policy.Status, policy.Step = "missing", err.Error()
		report.Checks = append(report.Checks, policy)
		return report, nil
	}
	report.Checks = append(report.Checks, policy)
	osName, osOK := checker.probe(ctx, machine.Target, "uname", "-s")
	architecture, archOK := checker.probe(ctx, machine.Target, "uname", "-m")
	platform := Check{Name: "platform", Status: "ready", Detail: osName + " " + architecture}
	supportedPlatform := osName == "Darwin" && architecture == "arm64" ||
		osName == "Linux" && (architecture == "x86_64" || architecture == "aarch64")
	if !osOK || !archOK || !supportedPlatform {
		platform.Status, platform.Step = "missing", "Use an Apple Silicon macOS, x86_64 Linux, or aarch64 Linux saved machine."
	}
	report.Checks = append(report.Checks, platform)
	probe(
		"nix",
		"Install Nix on the remote and make nix available to non-interactive SSH commands.",
		"nix",
		"--version",
	)
	probe(
		"flakes",
		"Repair the remote Nix installation so nix eval --extra-experimental-features 'nix-command flakes' --impure --expr builtins.currentSystem succeeds.",
		"nix",
		"eval",
		"--extra-experimental-features",
		"nix-command flakes",
		"--impure",
		"--expr",
		"builtins.currentSystem",
	)
	probe(
		"herdr",
		"Install Herdr 0.9.5 or newer on the remote and make herdr available to non-interactive SSH commands.",
		"herdr",
		"--version",
	)
	for _, repository := range repositories {
		step := "On the remote, provision read-only access to " + repository.Name + "; run git ls-remote -- " + sshcommand.Quote(
			repository.URL,
		) + " and verify its host key."
		if checker.Authentication == sshcommand.ForwardedAgent {
			step = "On the primary, load a key authorized for " + repository.Name + " with ssh-add. Verify the repository host key on the remote, then retry machine check with agent-forwarding."
		}
		probe(
			"repository:"+repository.Name,
			step,
			"env",
			"GIT_TERMINAL_PROMPT=0",
			"GIT_SSH_COMMAND="+repositorySSHCommand,
			"git",
			"ls-remote",
			"--",
			repository.URL,
		)
	}
	login := []string{"codex", "login", "status"}
	if agent == "claude" {
		login = []string{"claude", "auth", "status"}
	}
	// Authentication output can contain account information. Retain only the exit status.
	_, loggedIn := checker.probe(ctx, machine.Target, login...)
	loginCheck := Check{Name: "agent:" + agent, Status: "ready"}
	if !loggedIn {
		loginCheck.Status = "missing"
		loginCheck.Step = "On the remote, install " + agent + " and run " + agent + " login; then run " + strings.Join(
			login,
			" ",
		) + "."
		if agent == "claude" {
			loginCheck.Step = "On the remote, install claude and run claude auth login; then run claude auth status."
		}
	}
	report.Checks = append(report.Checks, loginCheck)
	return report, nil
}

func (checker Checker) probe(ctx context.Context, target string, command ...string) (string, bool) {
	arguments, err := sshcommand.TrustedAuthenticationArguments(
		target,
		checker.Authentication,
		command...)
	if err != nil {
		return "", false
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	stdout, _, err := checker.Runner.Run(ctx, arguments, nil)
	if err != nil || ctx.Err() != nil {
		return "", false
	}
	// Repository refs and login diagnostics have no place in the status record.
	if command[0] == "env" || command[0] == "codex" || command[0] == "claude" {
		return "", true
	}
	output := strings.TrimSpace(string(stdout))
	if len(output) > 256 || strings.IndexFunc(output, unicode.IsControl) >= 0 {
		return "", false
	}
	return output, true
}

func validRepositoryURL(source string) bool {
	if source == "" || strings.HasPrefix(source, "-") ||
		strings.IndexFunc(
			source,
			func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) },
		) >= 0 {
		return false
	}
	if strings.Contains(source, "://") {
		parsed, err := url.Parse(source)
		if err != nil || parsed.Hostname() == "" || parsed.Path == "" || parsed.RawQuery != "" ||
			parsed.Fragment != "" {
			return false
		}
		if parsed.Scheme == "https" {
			return parsed.User == nil
		}
		if parsed.Scheme != "ssh" {
			return false
		}
		if parsed.User != nil {
			_, password := parsed.User.Password()
			return !password
		}
		return true
	}
	host, path, found := strings.Cut(source, ":")
	return found && path != "" && !strings.ContainsAny(host, "/\\") && sshcommand.ValidTarget(host)
}
