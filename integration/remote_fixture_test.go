package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/machineprep"
	"github.com/roman/concierge/pkg/primary"
	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/sshcommand"
	"github.com/roman/concierge/pkg/taskstate"
)

type savedMachineAcceptance struct {
	t                                          *testing.T
	ctx                                        context.Context
	cancel                                     context.CancelFunc
	directory, root, messages, project, origin string
	binary, source, commit, reportPath         string
	machine                                    string
	remoteDirectory, owner                     string
	fixedBase                                  string
	decisionWait                               time.Duration
	profile                                    herdr.SavedMachine
	store                                      *taskstate.Store
	host                                       *exec.Cmd
	hostDone                                   chan error
	tasks                                      []string
	transcript                                 bytes.Buffer
}

func newSavedMachineAcceptance(t *testing.T) *savedMachineAcceptance {
	t.Helper()
	binary := os.Getenv("CONCIERGE_ACCEPTANCE_BIN")
	source := os.Getenv("CONCIERGE_ACCEPTANCE_SOURCE")
	report := os.Getenv("CONCIERGE_ACCEPTANCE_REPORT")
	if strings.TrimSpace(os.Getenv("CONCIERGE_ACCEPTANCE_MACHINE")) == "" {
		t.Fatal("select a saved machine with scripts/acceptance-remote.sh")
	}
	for _, path := range []string{binary, source, report} {
		if !filepath.IsAbs(path) {
			t.Fatal("use scripts/acceptance-remote.sh with an absolute report path")
		}
	}
	fixedBase := os.Getenv("CONCIERGE_ACCEPTANCE_BASE")
	if fixedBase != "" && !validSavedMachineAcceptanceDirectory(fixedBase) {
		t.Fatal(
			"--remote-base requires a canonical /tmp/ca.XXXXXX or /private/tmp/ca.XXXXXX path with six letters or digits",
		)
	}
	decisionWait, err := acceptanceDecisionWait(os.Getenv("CONCIERGE_ACCEPTANCE_DECISION_WAIT"))
	if err != nil {
		t.Fatal(err)
	}
	directory, err := newAcceptancePrimaryDirectory()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*decisionWait+30*time.Minute)
	run := &savedMachineAcceptance{t: t, ctx: ctx, cancel: cancel, directory: directory,
		root: filepath.Join(directory, "state"), messages: filepath.Join(directory, "messages"),
		project: filepath.Join(
			directory,
			"project",
		), origin: filepath.Join(directory, "origin.git"),
		binary: binary, source: source, reportPath: report, owner: rand.Text(), machine: os.Getenv("CONCIERGE_ACCEPTANCE_MACHINE"), fixedBase: fixedBase, decisionWait: decisionWait}
	t.Cleanup(run.cleanup)
	run.store, err = taskstate.NewStore(run.root)
	if err != nil {
		t.Fatal(err)
	}
	run.profile, err = (herdr.MachineCatalog{Runner: herdr.NewClient("herdr").Runner}).Resolve(
		ctx,
		run.machine,
	)
	if err != nil {
		t.Fatal(err)
	}
	run.note("Source and primary state: %s; %s", source, run.root)
	run.note("Native decision wait per startup or reply stage: %s", run.decisionWait)
	run.note(
		"Saved machine: %s (%s), session %s",
		run.profile.ID,
		run.profile.Target,
		run.profile.Session,
	)
	return run
}

func newAcceptancePrimaryDirectory() (string, error) {
	directory, err := os.MkdirTemp("/tmp", "ca-")
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		_ = os.Remove(directory)
	}
	return resolved, err
}

func (run *savedMachineAcceptance) note(format string, arguments ...any) {
	message := fmt.Sprintf(format, arguments...)
	run.t.Log(message)
	fmt.Fprintf(&run.transcript, "- %s\n", message)
}

func (run *savedMachineAcceptance) command(
	ctx context.Context,
	input io.Reader,
	executable string,
	arguments ...string,
) (string, error) {
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Dir = run.directory
	command.Env = append(
		os.Environ(),
		"TREEHOUSE_ROOT="+filepath.Join(run.directory, "primary-pool"),
	)
	command.Stdin = input
	command.WaitDelay = time.Second
	data, err := command.CombinedOutput()
	return strings.TrimSpace(string(data)), err
}

func (run *savedMachineAcceptance) call(
	ctx context.Context,
	input io.Reader,
	arguments ...string,
) (string, error) {
	arguments = append(
		[]string{"--root", run.root, "--message-root", run.messages, "--recipient", "concierge"},
		arguments...)
	return run.command(ctx, input, run.binary, arguments...)
}

func (run *savedMachineAcceptance) cli(arguments ...string) string {
	run.t.Helper()
	output, err := run.call(run.ctx, nil, arguments...)
	if err != nil {
		run.t.Fatalf("%s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return output
}

func (run *savedMachineAcceptance) git(directory string, arguments ...string) string {
	run.t.Helper()
	output, err := run.command(
		run.ctx,
		nil,
		"git",
		append([]string{"-C", directory}, arguments...)...)
	if err != nil {
		run.t.Fatalf("git %v: %v\n%s", arguments, err, output)
	}
	return output
}

func (run *savedMachineAcceptance) ssh(ctx context.Context, command ...string) (string, error) {
	arguments, err := sshcommand.TrustedAuthenticationArguments(
		run.profile.Target,
		sshcommand.ForwardedAgent,
		command...)
	if err != nil {
		return "", err
	}
	stdout, diagnostics, err := (sshcommand.SystemRunner{}).Run(ctx, arguments, nil)
	if err != nil && len(diagnostics) != 0 {
		if len(diagnostics) > 4096 {
			diagnostics = diagnostics[len(diagnostics)-4096:]
		}
		err = fmt.Errorf("%w: %s", err, strings.TrimSpace(string(diagnostics)))
	}
	return strings.TrimSpace(string(stdout)), err
}

func (run *savedMachineAcceptance) wait(
	timeout time.Duration,
	name string,
	check func() (bool, error),
) {
	run.t.Helper()
	if err := waitAcceptanceCondition(run.ctx, timeout, time.Second, check); err != nil {
		run.t.Fatalf("%s: %v", name, err)
	}
}

func (run *savedMachineAcceptance) prepare() {
	run.commit = run.git(run.source, "rev-parse", "HEAD")
	if status := run.git(run.source, "status", "--porcelain", "--untracked-files=all", "--ignored"); status != "" {
		run.t.Fatalf("acceptance requires committed, clean source: %s", status)
	}
	declaredOrigin := run.git(run.source, "config", "--get", "remote.origin.url")
	run.cli("state", "init")
	run.cli("state", "upgrade", "--backup", filepath.Join(run.directory, "version-two-backup"))
	session := os.Getenv("CODEX_THREAD_ID")
	if session == "" {
		session = os.Getenv("CODEX_SESSION_ID")
	}
	if os.Getenv("CLAUDE_PID") != "" {
		session = os.Getenv("CLAUDE_CODE_SESSION_ID")
	}
	if session == "" {
		run.t.Fatal("run acceptance from an existing native Codex or Claude session")
	}
	payload, err := json.Marshal(
		primary.Hook{SessionID: session, Cwd: run.directory, HookEventName: "SessionStart"},
	)
	if err != nil {
		run.t.Fatal(err)
	}
	if output, err := run.call(run.ctx, bytes.NewReader(payload), "primary", "claim"); err != nil {
		run.t.Fatalf("native primary claim: %v\n%s", err, output)
	}
	run.startHost()
	// The project keeps its declared source URL. Git transport resolves only to
	// the owned bare fixture, so the landing command cannot publish to GitHub.
	for _, arguments := range [][]string{
		{"init", "--bare", "-b", "main", run.origin},
		{"--git-dir", run.origin, "fetch", "--no-tags", run.source, "HEAD:refs/heads/main"},
		{"clone", run.origin, run.project},
	} {
		if output, err := run.command(run.ctx, nil, "git", arguments...); err != nil {
			run.t.Fatalf("fixture repository: %v %s", err, output)
		}
	}
	run.git(run.project, "remote", "set-url", "origin", declaredOrigin)
	run.git(run.project, "config", "url."+run.origin+".insteadOf", declaredOrigin)
	if destination := run.git(run.project, "remote", "get-url", "--push", "origin"); destination != run.origin {
		run.t.Fatal("landing push destination escaped the temporary origin")
	}
	create := createSavedMachineAcceptanceDirectory
	parameters := []string{run.owner}
	if run.fixedBase != "" {
		create = claimFixedAcceptanceBase
		parameters = []string{run.fixedBase, run.owner, run.profile.ID, declaredOrigin}
		run.note(
			"Claiming fixed acceptance base %s; prepared clients and trusted pool slots remain between runs",
			run.fixedBase,
		)
	}
	remote, err := run.ssh(
		run.ctx,
		append([]string{"sh", "-c", create, "concierge-acceptance"}, parameters...)...,
	)
	if err != nil {
		run.t.Fatalf("create remote temporary directory: %v", err)
	}
	if !validSavedMachineAcceptanceDirectory(remote) {
		run.t.Fatalf("unexpected remote temporary directory: %q", remote)
	}
	run.remoteDirectory = remote
	configuration := map[string]machineprep.Configuration{run.profile.ID: {
		Target: run.profile.Target, Base: remote + "/work", Agent: "codex", Authentication: sshcommand.ForwardedAgent,
		Repositories: []machineprep.Repository{{Name: "concierge", URL: declaredOrigin}},
	}}
	data, err := json.Marshal(configuration)
	if err != nil {
		run.t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(run.root, "config"), 0700); err != nil {
		run.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(run.root, "config", "machines.json"), data, 0600); err != nil {
		run.t.Fatal(err)
	}
	run.note("Remote temporary directory: %s", remote)
	run.note("Machine check: %s", run.cli("machine", "check", run.machine))
	run.note("Building the committed remote client %s with the saved machine's Nix", run.commit)
	run.note(
		"Machine prepare: %s",
		run.cli("machine", "prepare", run.machine, "--client-source", run.source),
	)
	run.note("Machine status: %s", run.cli("machine", "status", run.machine))
	run.cli("project", "set", "concierge", "--merge", "on", "--delivery", "fast-forward")
}

func validSavedMachineAcceptanceDirectory(directory string) bool {
	if directory != filepath.Clean(directory) {
		return false
	}
	parent := filepath.Dir(directory)
	if parent != "/tmp" && parent != "/private/tmp" {
		return false
	}
	name := filepath.Base(directory)
	if len(name) != len("ca.")+6 || !strings.HasPrefix(name, "ca.") {
		return false
	}
	return strings.IndexFunc(name[len("ca."):], func(character rune) bool {
		return !(character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' || character >= '0' && character <= '9')
	}) == -1
}

const createSavedMachineAcceptanceDirectory = `set -eu
umask 077
directory=$(mktemp -d /tmp/ca.XXXXXX)
printf '%s\n' "$1" > "$directory/.acceptance-owner"
cd "$directory"
pwd -P
`

func (run *savedMachineAcceptance) startHost() {
	log, err := os.OpenFile(
		filepath.Join(run.root, "host.log"),
		os.O_WRONLY|os.O_CREATE|os.O_APPEND,
		0600,
	)
	if err != nil {
		run.t.Fatal(err)
	}
	defer log.Close()
	run.host = exec.Command(run.binary, "--root", run.root, "--message-root", run.messages,
		"--recipient", "concierge", "host", "--interval", "1s", "serve")
	run.host.Dir = run.directory
	run.host.Env = append(
		os.Environ(),
		"TREEHOUSE_ROOT="+filepath.Join(run.directory, "primary-pool"),
	)
	run.host.Stdout, run.host.Stderr = log, log
	if err := run.host.Start(); err != nil {
		run.t.Fatal(err)
	}
	run.hostDone = make(chan error, 1)
	go func() { run.hostDone <- run.host.Wait() }()
	run.wait(20*time.Second, "temporary host readiness", func() (bool, error) {
		_, err := run.call(run.ctx, nil, "host", "status")
		return err == nil, err
	})
	run.note("Temporary primary host: ready")
}

func (run *savedMachineAcceptance) readTask(id string) taskstate.Task {
	run.t.Helper()
	task, err := run.store.Read(run.ctx, id)
	if err != nil {
		run.t.Fatal(err)
	}
	return task
}

func (run *savedMachineAcceptance) tunnel(id string) remoteworker.TunnelRecord {
	run.t.Helper()
	store, err := remoteworker.NewTunnelStore(run.root)
	if err != nil {
		run.t.Fatal(err)
	}
	var record remoteworker.TunnelRecord
	err = store.WithTask(run.ctx, id, func(lease *remoteworker.TunnelLease) error {
		stored, err := lease.Read()
		if err != nil {
			return err
		}
		if stored == nil {
			return fmt.Errorf("missing tunnel for %s", id)
		}
		record = *stored
		return nil
	})
	if err != nil {
		run.t.Fatal(err)
	}
	return record
}
