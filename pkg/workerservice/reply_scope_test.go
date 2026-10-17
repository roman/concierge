package workerservice

import (
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/taskops"
	"github.com/roman/concierge/pkg/workeraccess"
)

func TestWorkerReplyCarriesHostSelectedBindingMetadata(t *testing.T) {
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
	for _, invalid := range []func(*Reply){
		func(reply *Reply) { reply.Root = "" },
		func(reply *Reply) { reply.Root = "relative" },
		func(reply *Reply) { reply.Recipient = "" },
		func(reply *Reply) { reply.Recipient = "../other" },
	} {
		changed := reply
		invalid(&changed)
		if err := validateResponse(Response{Outcome: Completed, Reply: &changed}, request); err == nil {
			t.Fatal("reply confirmation omitted valid binding metadata")
		}
	}
}
