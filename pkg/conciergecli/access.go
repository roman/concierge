package conciergecli

import (
	"errors"
	"net"
	"os"
	"syscall"

	"github.com/roman/concierge/pkg/primary"
)

func commandAccessHelp(err error, help string) string {
	denied := errors.Is(err, os.ErrPermission) || errors.Is(err, syscall.EROFS)
	var socketError *net.OpError
	var pathError *os.PathError
	socketDenied := denied && errors.As(err, &socketError) && socketError.Net == "unix"
	pathDenied := denied && errors.As(err, &pathError)
	if !errors.Is(err, primary.ErrProcessOwnership) && !socketDenied && !pathDenied {
		return help
	}
	return help + ". In Codex, retry the exact command once with " +
		"sandbox_permissions=require_escalated through native approval. " +
		"Keep roots, recipient, generation and arguments unchanged. " +
		"If denied, keep the work pending and report the access failure"
}
