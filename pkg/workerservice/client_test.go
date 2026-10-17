package workerservice

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/taskops"
	"github.com/roman/concierge/pkg/workeraccess"
)

func TestWorkerClientReceivesCurrentWorkAndReportsDenial(t *testing.T) {
	path, request, _ := socketFixture(t, nil)
	response, err := Call(context.Background(), path, request)
	if err != nil || response.Assignment == nil || response.Assignment.Body != "Complete the work" {
		t.Fatalf("current receive: %+v %v", response, err)
	}
	request.Secret = "incorrect"
	response, err = Call(context.Background(), path, request)
	if !errors.Is(err, workeraccess.ErrDenied) || response.Outcome != Denied {
		t.Fatalf("denied receive: %+v %v", response, err)
	}
}

func TestWorkerClientDistinguishesUnavailableFromUnknownOutcome(t *testing.T) {
	directory, err := os.MkdirTemp("", "worker-client-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "worker.sock")
	request := Request{
		Task:       "task-1",
		Generation: "gen-1",
		Secret:     "secret",
		Operation:  workeraccess.ListPending,
	}
	if _, err := Call(context.Background(), path, request); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unavailable socket: %v", err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		connection, err := listener.AcceptUnix()
		if err != nil {
			return
		}
		defer connection.Close()
		var received Request
		_ = decodeBounded(connection, &received)
	}()
	if _, err := Call(context.Background(), path, request); !errors.Is(err, ErrOutcomeUnknown) {
		t.Fatalf("lost confirmation: %v", err)
	}
	<-done
}

func TestWorkerClientRejectsContradictoryResponses(t *testing.T) {
	request := Request{Task: "task-1", Generation: "gen-1", Operation: workeraccess.Receive}
	for _, response := range []Response{
		{Outcome: Completed},
		{Outcome: Completed, Assignment: &Assignment{Status: taskops.AssignmentStale, Body: "untrusted work"}},
		{Outcome: Denied, Error: "denied", Assignment: &Assignment{Status: taskops.AssignmentCurrent, Body: "work"}},
		{Outcome: Failed, Error: "failed", Reply: &Reply{MessageID: "invalid"}},
		{Outcome: Completed, Bootstrap: &taskops.BootstrapInfo{Task: "other"}},
		{Outcome: "unknown"},
	} {
		if err := validateResponse(response, request); err == nil {
			t.Fatalf("contradictory response accepted: %+v", response)
		}
	}
}

func TestWorkerClientRequiresRequestedAcknowledgement(t *testing.T) {
	id := "k-" + strings.Repeat("a", 64)
	request := Request{
		Task:       "task-1",
		Generation: "gen-1",
		Operation:  workeraccess.SendReply,
		MessageID:  id,
	}
	response := Response{
		Outcome: Completed,
		Reply: &Reply{MessageID: id, Assignment: taskops.ReplyUnbound,
			Publication: taskops.ReplyConfirmed, Archive: taskops.ReplyConfirmed,
			Root: "/primary/state", Recipient: "concierge"},
	}
	if err := validateResponse(response, request); err == nil {
		t.Fatal("bound reply accepted a missing acknowledgement outcome")
	}
}

func TestWorkerClientRequiresIndependentReplyConfirmations(t *testing.T) {
	id := "k-" + strings.Repeat("a", 64)
	request := Request{
		Task:       "task-1",
		Generation: "gen-1",
		Operation:  workeraccess.SendReply,
		MessageID:  id,
	}
	reply := Reply{MessageID: id, Assignment: taskops.ReplyAcknowledged,
		Publication: taskops.ReplyConfirmed, Archive: taskops.ReplyConfirmed,
		Root: "/primary/state", Recipient: "concierge"}
	if err := validateResponse(Response{Outcome: Completed, Reply: &reply}, request); err != nil {
		t.Fatal(err)
	}
	for _, missing := range []string{"publication", "archive", "unconfirmed archive"} {
		incomplete := reply
		switch missing {
		case "publication":
			incomplete.Publication = ""
		case "archive":
			incomplete.Archive = ""
		case "unconfirmed archive":
			incomplete.Archive = taskops.ReplyUnconfirmed
		}
		if err := validateResponse(Response{Outcome: Completed, Reply: &incomplete}, request); err == nil {
			t.Fatalf("reply completed without %s confirmation", missing)
		}
	}
	reply.Archive, reply.Assignment = taskops.ReplyUnconfirmed, taskops.ReplyAcknowledgementUnconfirmed
	if err := validateResponse(Response{Outcome: Uncertain, Reply: &reply, Error: "archive unavailable"}, request); err != nil {
		t.Fatalf("partial publication confirmation was discarded: %v", err)
	}
}
