package conciergecli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/hostservice"
	"github.com/roman/concierge/pkg/machineprep"
)

func TestMachinePrepareUsesPrimaryHostAndReportsBuiltCommit(t *testing.T) {
	root := shortTaskRoot(t)
	listener, err := net.ListenUnix(
		"unix",
		&net.UnixAddr{Name: filepath.Join(root, "host.sock"), Net: "unix"},
	)
	if err != nil {
		t.Fatal(err)
	}
	received := make(chan hostservice.Request, 1)
	done := make(chan struct{})
	commit := strings.Repeat("a", 40)
	go func() {
		defer close(done)
		connection, err := listener.AcceptUnix()
		if err != nil {
			return
		}
		defer connection.Close()
		var request hostservice.Request
		if json.NewDecoder(connection).Decode(&request) != nil {
			return
		}
		received <- request
		_ = json.NewEncoder(connection).
			Encode(hostservice.Response{Outcome: hostservice.Completed, PreparedMachine: &hostservice.PreparedMachineResult{MachineID: "saved-id", Target: "dev-remote", Client: machineprep.Client{Commit: commit, Executable: "/remote/base/client/bin/concierge"}}})
	}()
	t.Cleanup(func() { _ = listener.Close(); <-done })
	source := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var output bytes.Buffer
	code := Run(
		ctx,
		[]string{"--root", root, "machine", "prepare", "dev-remote", "--client-source", source},
		nil,
		&output,
		&output,
	)
	if code != 0 || !strings.Contains(output.String(), "client_commit: "+quote(commit)) {
		t.Fatalf("code=%d: %s", code, &output)
	}
	select {
	case request := <-received:
		if request.Operation != hostservice.PrepareMachine || request.MachinePreparation == nil ||
			request.MachinePreparation.Label != "dev-remote" ||
			request.MachinePreparation.ClientSource != source {
			t.Fatalf("changed preparation request: %+v", request)
		}
	case <-ctx.Done():
		t.Fatal("preparation never reached primary host")
	}
}
