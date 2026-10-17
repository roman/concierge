package remoteworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"path/filepath"
	"time"

	"github.com/roman/concierge/pkg/workeraccess"
)

// ProbeSocket checks both sides of the forward without a credential or task
// operation. An invalid request must be rejected before acquiring a task lock.
func ProbeSocket(ctx context.Context, directory string, owner SocketOwner) error {
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
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(
		ctx,
		"unix",
		filepath.Join(directory, "worker.sock"),
	)
	if err != nil {
		return ErrTunnelUnreachable
	}
	defer connection.Close()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if connection.SetDeadline(deadline) != nil {
		return ErrTunnelUnreachable
	}
	if _, err := io.WriteString(connection, "{}"); err != nil {
		return ErrTunnelUnreachable
	}
	if connection.(*net.UnixConn).CloseWrite() != nil {
		return ErrTunnelUnreachable
	}
	data, err := io.ReadAll(io.LimitReader(connection, 4097))
	if err != nil || len(data) > 4096 {
		return ErrTunnelUnreachable
	}
	var response struct {
		Outcome string `json:"outcome"`
		Error   string `json:"error"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&response) != nil || !errors.Is(decoder.Decode(new(any)), io.EOF) {
		return ErrTunnelUnreachable
	}
	if response.Outcome == "invalid" && response.Error == "invalid worker request" {
		return nil
	}
	if response.Outcome == "busy" &&
		response.Error == "worker request rate limit reached; retry later" {
		return nil
	}
	return ErrTunnelUnreachable
}
