package primary

import (
	"errors"
	"golang.org/x/sys/unix"
	"net"
)

func PeerCaller(connection *net.UnixConn) (Caller, error) {
	raw, err := connection.SyscallConn()
	if err != nil {
		return Caller{}, err
	}
	var credentials *unix.Ucred
	var peerErr error
	err = raw.Control(func(fd uintptr) {
		credentials, peerErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	})
	if err := errors.Join(err, peerErr); err != nil {
		return Caller{}, err
	}
	return Caller{PID: int(credentials.Pid), UID: credentials.Uid}, nil
}
