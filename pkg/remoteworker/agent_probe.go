package remoteworker

import (
	"context"
	"os"
	"path/filepath"
	"syscall"

	"github.com/roman/concierge/pkg/sshcommand"
	"github.com/roman/concierge/pkg/workeraccess"
)

func ProbeAgentSocket(ctx context.Context, directory string, owner SocketOwner) error {
	if owner.Validate() != nil {
		return ErrSocketOwnership
	}
	root, err := workeraccess.OpenPrivateDirectory(directory)
	if err != nil {
		return ErrSocketOwnership
	}
	defer root.Close()
	if !socketDirectoryMatches(root, directory, owner) {
		return ErrSocketOwnership
	}
	before, err := root.Lstat("agent.sock")
	if os.IsNotExist(err) {
		return ErrTunnelUnreachable
	}
	if err != nil {
		return ErrSocketOwnership
	}
	stat, owned := before.Sys().(*syscall.Stat_t)
	if before.Mode()&os.ModeSocket == 0 || !owned || stat.Uid != uint32(os.Geteuid()) {
		return ErrSocketOwnership
	}
	if err := sshcommand.CheckAgentSocket(ctx, filepath.Join(directory, "agent.sock")); err != nil {
		return ErrTunnelUnreachable
	}
	after, err := root.Lstat("agent.sock")
	if err != nil || !os.SameFile(before, after) ||
		!socketDirectoryMatches(root, directory, owner) {
		return ErrSocketOwnership
	}
	return nil
}
