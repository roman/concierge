package remoteworker

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/roman/concierge/pkg/workeraccess"
)

var ErrSocketActive = errors.New("remote worker socket has an active listener; retain the tunnel")

func PrepareSocket(ctx context.Context, directory string, owner SocketOwner) error {
	dialer := net.Dialer{Timeout: 2 * time.Second}
	return prepareSocket(
		ctx,
		directory,
		owner,
		func(ctx context.Context, path string) (net.Conn, error) {
			return dialer.DialContext(ctx, "unix", path)
		},
	)
}

func prepareSocket(
	ctx context.Context,
	directory string,
	owner SocketOwner,
	probe func(context.Context, string) (net.Conn, error),
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ClaimSocketDirectory(directory, owner); err != nil {
		return err
	}
	root, err := workeraccess.OpenPrivateDirectory(directory)
	if err != nil {
		return ErrSocketOwnership
	}
	defer root.Close()
	if !socketDirectoryMatches(root, directory, owner) {
		return ErrSocketOwnership
	}
	before, err := root.Lstat("worker.sock")
	if errors.Is(err, os.ErrNotExist) {
		return syncSocketDirectory(root)
	}
	if err != nil {
		return ErrSocketOwnership
	}
	stat, owned := before.Sys().(*syscall.Stat_t)
	if before.Mode()&os.ModeSocket == 0 || !owned || stat.Uid != uint32(os.Geteuid()) {
		return ErrSocketOwnership
	}
	connection, err := probe(ctx, filepath.Join(directory, "worker.sock"))
	if err == nil {
		if connection != nil {
			connection.Close()
		}
		return ErrSocketActive
	}
	// A refused connection excludes an active listener. Other failures leave
	// the remote socket's state unknown and must preserve its pathname.
	if !errors.Is(err, syscall.ECONNREFUSED) {
		return ErrSocketOwnership
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !socketDirectoryMatches(root, directory, owner) {
		return ErrSocketOwnership
	}
	after, err := root.Lstat("worker.sock")
	if err != nil || !os.SameFile(before, after) {
		return ErrSocketOwnership
	}
	if err := root.Remove("worker.sock"); err != nil {
		return ErrSocketOwnership
	}
	return syncSocketDirectory(root)
}

func socketDirectoryMatches(root *os.Root, directory string, owner SocketOwner) bool {
	current, err := readSocketOwner(root)
	if err != nil || current != owner {
		return false
	}
	pinned, err := root.Stat(".")
	if err != nil {
		return false
	}
	named, err := os.Lstat(directory)
	return err == nil && os.SameFile(pinned, named)
}
