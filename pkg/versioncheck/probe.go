package versioncheck

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"time"
	"unicode"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/sshcommand"
)

type Probe func(context.Context, string, ...string) ([]byte, error)

type Collector struct {
	Herdr  string
	Local  Probe
	Remote sshcommand.Runner
}

type Options struct {
	PrimaryCommit, RemoteCommit, Agent string
	Machine                            herdr.SavedMachine
	Authentication                     sshcommand.Authentication
}

func (collector Collector) Inspect(ctx context.Context, options Options) Report {
	local := collector.Local
	if local == nil {
		local = runLocal
	}
	executable := collector.Herdr
	if executable == "" {
		executable = "herdr"
	}
	primary := inspect(ctx, local, executable, options.Agent)
	primary.Concierge = cleanVersion(options.PrimaryCommit)
	if options.Machine.ID == "" {
		return Compare(primary, nil, options.Agent, "")
	}
	remote := collector.Remote
	machine := Snapshot{Concierge: cleanVersion(options.RemoteCommit)}
	if remote != nil {
		if err := sshcommand.CheckNativePolicy(ctx, remote, options.Machine.Target); err != nil {
			return Compare(primary, &machine, options.Agent, machineLabel(options.Machine))
		}
		probe := func(ctx context.Context, executable string, args ...string) ([]byte, error) {
			arguments, err := sshcommand.TrustedAuthenticationArguments(
				options.Machine.Target,
				options.Authentication,
				append([]string{executable}, args...)...)
			if err != nil {
				return nil, err
			}
			output, _, err := remote.Run(ctx, arguments, nil)
			return output, err
		}
		machine = inspect(ctx, probe, "herdr", options.Agent)
		machine.Concierge = cleanVersion(options.RemoteCommit)
	}
	return Compare(primary, &machine, options.Agent, machineLabel(options.Machine))
}

func machineLabel(machine herdr.SavedMachine) string {
	if machine.Label != "" {
		return machine.Label
	}
	return machine.ID
}

func inspect(ctx context.Context, probe Probe, herdrExecutable, agent string) Snapshot {
	var snapshot Snapshot
	statusContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	output, err := probe(statusContext, herdrExecutable, "status")
	cancel()
	if err == nil {
		snapshot, _ = parseHerdrStatus(output)
	}
	if agent != "codex" && agent != "claude" {
		return snapshot
	}
	agentContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err = probe(agentContext, agent, "--version")
	if err == nil {
		snapshot.Agent = cleanVersion(string(output))
	}
	return snapshot
}

func cleanVersion(value string) string {
	value = strings.TrimSpace(value)
	if value == "dev" || value == "unknown" || len(value) > 256 ||
		strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return ""
	}
	return value
}

func parseHerdrStatus(output []byte) (Snapshot, error) {
	invalid := errors.New("unrecognized Herdr status")
	if len(output) > 16<<10 {
		return Snapshot{}, invalid
	}
	fields := map[string]string{}
	section := ""
	for _, line := range strings.Split(string(output), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.HasPrefix(line, "  ") {
			if !strings.HasSuffix(line, ":") {
				return Snapshot{}, invalid
			}
			section = strings.TrimSuffix(line, ":")
			continue
		}
		key, value, found := strings.Cut(strings.TrimSpace(line), ":")
		key = section + "." + key
		if _, duplicate := fields[key]; duplicate || !found {
			return Snapshot{}, invalid
		}
		fields[key] = strings.TrimSpace(value)
	}
	snapshot := Snapshot{
		HerdrClient:        cleanVersion(fields["client.version"]),
		HerdrServer:        cleanVersion(fields["server.version"]),
		Stale:              fields["update.server_binary_stale"],
		PrivateCompatible:  fields["server.private_protocol_compatible"],
		EndpointCompatible: fields["server.endpoint_compatible"],
	}
	if fields["server.status"] != "running" || snapshot.HerdrClient == "" ||
		snapshot.HerdrServer == "" {
		return Snapshot{}, invalid
	}
	for _, value := range []string{snapshot.Stale, snapshot.PrivateCompatible, snapshot.EndpointCompatible} {
		if value != "yes" && value != "no" {
			return Snapshot{}, invalid
		}
	}
	return snapshot, nil
}

// Bound local output as well as time; a broken version command must not consume
// the host's memory or keep a descendant's output pipe open indefinitely.
func runLocal(ctx context.Context, executable string, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, executable, arguments...)
	command.WaitDelay = time.Second
	var output bytes.Buffer
	command.Stdout = &boundedOutput{writer: &output, remaining: 16 << 10}
	command.Stderr = io.Discard
	err := command.Run()
	return output.Bytes(), err
}

type boundedOutput struct {
	writer    io.Writer
	remaining int
}

func (output *boundedOutput) Write(data []byte) (int, error) {
	if len(data) > output.remaining {
		return 0, errors.New("component version output exceeds 16 KiB")
	}
	output.remaining -= len(data)
	return output.writer.Write(data)
}
