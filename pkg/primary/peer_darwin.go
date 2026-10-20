package primary

import (
	"errors"
	"net"

	"golang.org/x/sys/unix"
)

func PeerCaller(connection *net.UnixConn) (Caller, error) {
	raw, err := connection.SyscallConn()
	if err != nil {
		return Caller{}, err
	}
	var caller Caller
	var peerErr error
	err = raw.Control(func(fd uintptr) {
		pid, err := unix.GetsockoptInt(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERPID)
		if err != nil {
			peerErr = err
			return
		}
		credentials, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if err != nil {
			peerErr = err
			return
		}
		if pid < 1 || credentials.Version != 0 {
			peerErr = errors.New("kernel peer identity is unavailable")
			return
		}
		caller = Caller{PID: pid, UID: credentials.Uid}
	})
	if err := errors.Join(err, peerErr); err != nil {
		return Caller{}, err
	}
	return caller, nil
}
