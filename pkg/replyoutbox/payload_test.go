package replyoutbox

import (
	"errors"
	"strings"
	"testing"
)

func payloadFixture() Payload {
	return Payload{Root: "/primary/state", Task: "task-1", Generation: "gen-1",
		AssignmentID: "k-" + strings.Repeat("a", 64), Key: "stable-result", Recipient: "concierge",
		Body: "Completed\nwith tests\n"}
}

func TestPayloadRetainsTheCompleteOriginalReplyTuple(t *testing.T) {
	original := payloadFixture()
	encoded, err := original.Encode()
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodePayload(encoded)
	if err != nil || decoded != original {
		t.Fatalf("reply tuple changed: %+v %v", decoded, err)
	}
	for _, invalid := range [][]byte{
		append(encoded, []byte(` {}`)...),
		[]byte(strings.Replace(string(encoded), `"root":`, `"unknown":true,"root":`, 1)),
	} {
		if _, err := DecodePayload(invalid); !errors.Is(err, ErrInvalidPayload) {
			t.Fatalf("unsupported payload accepted: %v", err)
		}
	}
}

func TestBoundReplyIdentityDoesNotPermitASecondPayload(t *testing.T) {
	original := payloadFixture()
	identity, err := original.ID()
	if err != nil {
		t.Fatal(err)
	}
	changed := original
	changed.Key, changed.Body = "different-key", "Different result"
	otherID, err := changed.ID()
	if err != nil || otherID != identity {
		t.Fatal("a different body or key escaped the same assignment's outbox entry")
	}
	for _, field := range []string{"root", "task", "generation", "assignment", "recipient"} {
		changed = original
		switch field {
		case "root":
			changed.Root = "/other/state"
		case "task":
			changed.Task = "task-2"
		case "generation":
			changed.Generation = "gen-2"
		case "assignment":
			changed.AssignmentID = "k-" + strings.Repeat("b", 64)
		case "recipient":
			changed.Recipient = "other"
		}
		otherID, err = changed.ID()
		if err != nil || otherID == identity {
			t.Fatalf("%s did not bind reply identity: %v", field, err)
		}
	}
}

func TestUnboundReplyIdentityIncludesItsOriginalKey(t *testing.T) {
	original := payloadFixture()
	original.AssignmentID = ""
	identity, _ := original.ID()
	original.Key = "other-result"
	otherID, err := original.ID()
	if err != nil || otherID == identity {
		t.Fatal("unbound replies with different keys share an outbox entry")
	}
}

func TestPayloadRejectsUnsupportedOrLossyValues(t *testing.T) {
	for _, invalid := range []func(*Payload){
		func(payload *Payload) { payload.Root = "relative" },
		func(payload *Payload) { payload.Root = "/primary/../state" },
		func(payload *Payload) { payload.Generation = "" },
		func(payload *Payload) { payload.AssignmentID = "not-a-message" },
		func(payload *Payload) { payload.Recipient = "../other" },
		func(payload *Payload) { payload.Key = " " },
		func(payload *Payload) { payload.Key = strings.Repeat("k", 1025) },
		func(payload *Payload) { payload.Body = " " },
		func(payload *Payload) { payload.Body = strings.Repeat("b", (1<<20)+1) },
		func(payload *Payload) { payload.Body = string([]byte{255}) },
	} {
		payload := payloadFixture()
		invalid(&payload)
		if _, err := payload.Encode(); !errors.Is(err, ErrInvalidPayload) {
			t.Fatalf("unsupported reply payload accepted: %v", err)
		}
	}
}
