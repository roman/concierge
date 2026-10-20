package heartbeat

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestActionStatusRejectsInvalidRecords(t *testing.T) {
	for _, body := range []string{`{"actions":null}`, `{"actions":{"bad/id":"text"}}`, `{"actions":{"action":" "}}`} {
		t.Run(body, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "actions.json")
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := readActions(path); err == nil {
				t.Fatal("invalid action status accepted")
			}
		})
	}
}

func TestActionStatusRejectsFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "actions.json")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := readActions(path); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted")
		}
	case <-time.After(100 * time.Millisecond):
		writer, err := os.OpenFile(path, os.O_WRONLY|syscall.O_NONBLOCK, 0)
		if err == nil {
			writer.Write([]byte("invalid"))
			writer.Close()
		}
		<-done
		t.Fatal("action status blocked on a FIFO")
	}
}
