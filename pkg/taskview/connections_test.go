package taskview

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/roman/concierge/pkg/taskstate"
)

func TestRemoteWorkerShowsIndependentConnections(t *testing.T) {
	worker := viewWorker()
	worker.Machine = "baker"
	task := viewTask(t, "remote", taskstate.InFlight, worker, nil)
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	var err error
	for _, observation := range []struct {
		channel taskstate.ConnectionChannel
		at      time.Time
		failure string
	}{
		{taskstate.HerdrChannel, now, ""},
		{taskstate.TunnelChannel, now, ""},
		{taskstate.TunnelChannel, now.Add(time.Second), "SSH disconnected"},
	} {
		task, err = task.RecordConnection(
			"gen-1",
			observation.channel,
			taskstate.ConnectionObservation{
				At: observation.at, Error: observation.failure,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	if err := WriteWorker(&output, task); err != nil {
		t.Fatal(err)
	}
	want := "machine: \"baker\"\n" +
		"herdr_reachability:\n  status: reachable\n  last_success: \"2026-10-17T12:00:00Z\"\n  last_error: null\n  next_retry: null\n" +
		"tunnel_reachability:\n  status: unreachable\n  last_success: \"2026-10-17T12:00:00Z\"\n  last_error: \"SSH disconnected\"\n  next_retry: \"2026-10-17T12:00:02Z\"\n"
	if !strings.Contains(output.String(), want) {
		t.Fatalf("missing independent connection states: %s", &output)
	}
}

func TestUnprobedRemoteWorkerReportsUnknownConnections(t *testing.T) {
	worker := viewWorker()
	worker.Machine = "baker"
	var output bytes.Buffer
	if err := WriteWorker(&output, viewTask(t, "remote", taskstate.InFlight, worker, nil)); err != nil {
		t.Fatal(err)
	}
	for _, channel := range []string{"herdr", "tunnel"} {
		want := channel + "_reachability:\n  status: unknown\n  last_success: null\n  last_error: null\n  next_retry: null\n"
		if !strings.Contains(output.String(), want) {
			t.Fatalf("unknown connection omitted: %s", &output)
		}
	}
}
