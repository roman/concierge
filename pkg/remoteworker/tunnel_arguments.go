package remoteworker

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/roman/concierge/pkg/sshcommand"
	"github.com/roman/concierge/pkg/taskmeta"
)

var ErrTunnelConfiguration = errors.New(
	"select a forwarding-free SSH host entry; remove LocalForward, RemoteForward, DynamicForward and Tunnel settings",
)

func TunnelArguments(spec TunnelSpec, localSocket string) ([]string, error) {
	options, err := tunnelOptions(spec, localSocket)
	if err != nil {
		return nil, err
	}
	options = append(
		options,
		"-T",
		"-N",
		"-R",
		filepath.Join(spec.Tools.TaskDirectory, "worker.sock")+":"+localSocket,
	)
	if spec.Authentication == sshcommand.ForwardedAgent {
		agentSocket := spec.PrimaryAgentSocket
		if agentSocket == "" {
			agentSocket = os.Getenv("SSH_AUTH_SOCK")
		}
		if _, err := taskmeta.ParseAbsolutePath(agentSocket); err != nil ||
			strings.ContainsRune(agentSocket, ':') {
			return nil, sshcommand.ErrPrimaryAgent
		}
		// A fixed task-owned socket lets the existing worker keep its environment
		// when this SSH process reconnects or the primary host restarts.
		options = append(
			options,
			"-R",
			filepath.Join(spec.Tools.TaskDirectory, "agent.sock")+":"+agentSocket,
		)
	}
	return append(options, "--", spec.Target), nil
}

func TunnelConfigArguments(spec TunnelSpec, localSocket string) ([]string, error) {
	options, err := tunnelOptions(spec, localSocket)
	if err != nil {
		return nil, err
	}
	return append(options, "-G", "-T", "--", spec.Target), nil
}

func tunnelOptions(spec TunnelSpec, localSocket string) ([]string, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	if _, err := taskmeta.ParseAbsolutePath(localSocket); err != nil ||
		(filepath.Base(localSocket) != "worker.sock" && localSocket != spec.PrimaryWorkerSocket) {
		return nil, ErrTunnelOwnership
	}
	for _, path := range []string{localSocket, spec.Tools.TaskDirectory} {
		if strings.ContainsRune(path, ':') || strings.IndexFunc(path, unicode.IsControl) >= 0 {
			return nil, errors.New(
				"SSH reverse socket paths cannot contain colons or control characters",
			)
		}
	}
	return []string{
		"-S", "none", "-o", "ControlMaster=no", "-o", "ControlPersist=no",
		"-o", "BatchMode=yes", "-o", "ExitOnForwardFailure=yes",
		"-o", "ForwardAgent=no", "-o", "ForwardX11=no", "-o", "ForwardX11Trusted=no",
		"-o", "SetEnv=CONCIERGE_SSH_TRANSPORT=1",
		"-o", "StrictHostKeyChecking=yes", "-o", "UpdateHostKeys=no",
		"-o", "ForkAfterAuthentication=no", "-o", "StdinNull=yes",
		"-o", "PermitLocalCommand=no", "-o", "StreamLocalBindUnlink=no",
		"-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3",
		"-o", "ConnectTimeout=10", "-o", "StreamLocalBindMask=0177",
		// Clearing forwards also removes our explicit reverse socket. The caller
		// must inspect the effective configuration before starting this command.
		"-o", "ClearAllForwardings=no",
	}, nil
}

func ValidateTunnelConfiguration(data []byte) error {
	hostname := false
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return ErrTunnelConfiguration
		}
		switch strings.ToLower(fields[0]) {
		case "hostname":
			if hostname || len(fields) != 2 {
				return ErrTunnelConfiguration
			}
			hostname = true
		case "localforward", "remoteforward", "dynamicforward":
			return ErrTunnelConfiguration
		case "tunnel":
			if len(fields) != 2 || (fields[1] != "false" && fields[1] != "no") {
				return ErrTunnelConfiguration
			}
		}
	}
	if !hostname {
		return ErrTunnelConfiguration
	}
	return nil
}
