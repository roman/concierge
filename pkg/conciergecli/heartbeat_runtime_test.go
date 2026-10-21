package conciergecli

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/hostactivity"
	"github.com/roman/concierge/pkg/hostservice"
	"github.com/roman/concierge/pkg/taskops"
	"github.com/roman/tincan/pkg/inbox"
)

func TestHeartbeatRuntimeGuard(t *testing.T) {
	for _, scenario := range []string{"current", "revision", "executable", "messages", "recipient", "missing host"} {
		t.Run(scenario, func(t *testing.T) {
			root := shortTaskRoot(t)
			messages := filepath.Join(root, "messages")
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			recipient, _ := inbox.ParseRecipient("concierge")
			config := hostservice.Config{
				Root: root, MessageRoot: messages, Recipient: recipient,
				Revision: revision, HerdrExecutable: unusedHerdr(t),
				WorkerCommands: taskops.CommandPaths{Concierge: executable, Tincan: executable},
			}
			switch scenario {
			case "revision":
				config.Revision = "older"
			case "executable":
				config.WorkerCommands.Concierge = "/older/concierge"
			case "messages":
				config.MessageRoot = filepath.Join(root, "other")
			case "recipient":
				config.Recipient, _ = inbox.ParseRecipient("other")
			}
			if scenario != "missing host" {
				serveConfiguredTaskRoot(t, config)
			}
			command := cli{MessageRoot: messages, Recipient: "concierge"}
			err = command.checkHeartbeatRuntime(context.Background(), root)
			if scenario == "current" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil {
				t.Fatal("accepted a different or missing host")
			}
			if scenario != "revision" {
				return
			}
			for _, operation := range []string{"claim", "ack"} {
				args := []string{
					"--root",
					root,
					"--message-root",
					messages,
					"heartbeat",
					operation,
					"event",
					"--generation",
					"gen",
					"--runtime",
					"host",
				}
				if operation == "ack" {
					args = append(args, "--token", "token", "--output", "posted")
				}
				var output strings.Builder
				code := Run(context.Background(), args, nil, &output, &output)
				if code == 0 {
					t.Fatal("accepted a nonexistent event")
				}
				if !strings.Contains(output.String(), "restore the adopted shell") {
					t.Fatalf("runtime guard did not reject before event processing: %s", &output)
				}
			}
		})
	}
}

func TestHeartbeatRuntimeExcludesHostReplacement(t *testing.T) {
	for _, operation := range []string{"claim", "ack"} {
		t.Run(operation, func(t *testing.T) {
			root := shortTaskRoot(t)
			listener, err := net.ListenUnix(
				"unix",
				&net.UnixAddr{Name: filepath.Join(root, "host.sock"), Net: "unix"},
			)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			leaseResult := make(chan error, 1)
			go func() {
				connection, err := listener.AcceptUnix()
				if err != nil {
					leaseResult <- err
					return
				}
				defer connection.Close()
				var request hostservice.Request
				if err := json.NewDecoder(connection).Decode(&request); err != nil {
					leaseResult <- err
					return
				}
				lease, err := hostactivity.Quiesce(root)
				if lease != nil {
					lease.Close()
				}
				leaseResult <- err
				json.NewEncoder(connection).
					Encode(hostservice.Response{Outcome: hostservice.Invalid, Error: "fixture rejects status"})
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			command := cli{}
			command.Heartbeat.Claim.Runtime = "host"
			command.Heartbeat.Ack.Runtime = "host"
			var output strings.Builder
			command.heartbeatLifecycleResponse(
				ctx,
				root,
				"heartbeat "+operation+" <event-id>",
				&output,
			)
			if err := <-leaseResult; !errors.Is(err, hostactivity.ErrBusy) {
				t.Fatalf("host replacement admitted during %s runtime check: %v", operation, err)
			}
			lease, err := hostactivity.Quiesce(root)
			if err != nil {
				t.Fatalf("runtime failure retained its lease: %v", err)
			}
			lease.Close()
		})
	}
}
