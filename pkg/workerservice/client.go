package workerservice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/roman/concierge/pkg/taskmeta"
	"github.com/roman/concierge/pkg/workeraccess"
)

var ErrUnavailable = errors.New("worker service unavailable")
var ErrOutcomeUnknown = errors.New("worker operation outcome is unknown")
var ErrRateLimited = errors.New("worker request rate limit reached")

// Call retains the connection through confirmation. A disconnect after dialing
// cannot establish whether a mutation completed; callers must retain its input.
func Call(ctx context.Context, socket string, request Request) (Response, error) {
	if err := request.validate(); err != nil {
		return Response{}, err
	}
	if _, err := taskmeta.ParseAbsolutePath(socket); err != nil ||
		filepath.Base(socket) == "host.sock" {
		return Response{}, ErrInvalidRequest
	}
	data, err := json.Marshal(request)
	if err != nil || len(data) > maxWireBytes {
		return Response{}, ErrInvalidRequest
	}
	connection, err := (&net.Dialer{Timeout: workerIOTimeout}).DialContext(ctx, "unix", socket)
	if err != nil {
		return Response{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer connection.Close()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	_ = connection.SetDeadline(time.Now().Add(workerOperationTimeout + workerIOTimeout))
	if _, err := connection.Write(data); err != nil {
		return Response{}, fmt.Errorf("%w: %v", ErrOutcomeUnknown, err)
	}
	if err := connection.(*net.UnixConn).CloseWrite(); err != nil {
		return Response{}, fmt.Errorf("%w: %v", ErrOutcomeUnknown, err)
	}
	var response Response
	if err := decodeBounded(connection, &response); err != nil {
		return Response{}, fmt.Errorf("%w: %v", ErrOutcomeUnknown, err)
	}
	response.Error = strings.ReplaceAll(response.Error, request.Secret, "[redacted]")
	if err := validateResponse(response, request); err != nil {
		return Response{}, fmt.Errorf("%w: %v", ErrOutcomeUnknown, err)
	}
	switch response.Outcome {
	case Completed:
		return response, nil
	case Denied:
		return response, workeraccess.ErrDenied
	case Invalid:
		return response, ErrInvalidRequest
	case Busy:
		return response, ErrRateLimited
	case Uncertain:
		return response, fmt.Errorf("%w: %s", ErrOutcomeUnknown, response.Error)
	default:
		return response, errors.New(response.Error)
	}
}
