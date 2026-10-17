package workerservice

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/workeraccess"
)

func TestWorkerProtocolRejectsAuthorityAndPathFields(t *testing.T) {
	valid := Request{
		Task:       "task-1",
		Generation: "gen-1",
		Secret:     strings.Repeat("s", 43),
		Operation:  workeraccess.ListPending,
	}
	data, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	var request Request
	if err := decodeBounded(bytes.NewReader(data), &request); err != nil ||
		request.validate() != nil {
		t.Fatalf("valid pending request: %v", err)
	}
	for _, trial := range []string{
		`"root":"/other"`, `"message_root":"/other"`, `"recipient":"primary"`,
		`"body_file":"/remote/result"`, `"socket":"/host.sock"`,
	} {
		body := append(bytes.Clone(data[:len(data)-1]), []byte(","+trial+"}")...)
		if err := decodeBounded(bytes.NewReader(body), new(Request)); err == nil {
			t.Fatalf("caller authority field accepted: %s", trial)
		}
	}
	for _, operation := range []workeraccess.Operation{"land", "launch", "stop", "intake", "host-stop"} {
		request := valid
		request.Operation = operation
		if err := request.validate(); err == nil {
			t.Fatalf("primary operation accepted: %s", operation)
		}
	}
	if strings.Contains(fmt.Sprintf("%+v", valid), valid.Secret) {
		t.Fatal("formatted request exposed its secret")
	}
}

func TestWorkerProtocolRejectsOversizeAndTrailingData(t *testing.T) {
	for _, data := range []string{
		strings.Repeat(" ", maxWireBytes+1),
		`{} {}`, `{} trailing`, `null`, `[]`,
	} {
		var request Request
		err := decodeBounded(strings.NewReader(data), &request)
		if err == nil {
			err = request.validate()
		}
		if err == nil {
			t.Fatal("invalid worker request was accepted")
		}
	}
}

func TestWorkerProtocolRejectsContradictoryPayloads(t *testing.T) {
	for _, request := range []Request{
		{Operation: workeraccess.Receive},
		{Operation: workeraccess.ListPending, Body: "unexpected"},
		{Operation: workeraccess.ReadSettings, Key: "unexpected"},
		{Operation: workeraccess.SendReply, Key: "reply", Body: strings.Repeat("x", maxBodyBytes+1)},
		{Operation: workeraccess.SendReply, Key: "reply", Body: " "},
	} {
		request.Task, request.Generation, request.Secret = "task-1", "gen-1", strings.Repeat(
			"s",
			43,
		)
		if err := request.validate(); err == nil {
			t.Fatalf("contradictory %s request accepted", request.Operation)
		}
	}
}
