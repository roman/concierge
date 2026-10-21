package conciergecli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/roman/concierge/pkg/taskops"
	"github.com/roman/concierge/pkg/taskprotocol"
	"github.com/roman/concierge/pkg/workerservice"
	"github.com/roman/tincan/pkg/inbox"
)

func TestReplyAttachmentCLISendReadAndExport(t *testing.T) {
	t.Setenv(workerLaunchGeneration, "gen-1")
	root := lifecycleRoot(t)
	startFixtureTask(t, root)
	messageRoot := t.TempDir()
	assignment := assignmentMessage(t, root, messageRoot, "gen-1", "Work")
	source := filepath.Join(t.TempDir(), "source,with=punctuation.bin")
	data := []byte{0, 128, 255, '\n'}
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	args := []string{
		"--root",
		root,
		"--message-root",
		messageRoot,
		"task",
		"reply",
		"send",
		"task-1",
		"--generation",
		"gen-1",
		"--key",
		"result",
		"--assignment-id",
		assignment.ID.String(),
		"--body-file",
		"-",
		"--attach",
		"evidence.bin=" + source,
	}
	if code := Run(t.Context(), args, strings.NewReader("Done"), &output, &output); code != 0 {
		t.Fatalf("send: %s", &output)
	}
	messages, _ := inbox.New(messageRoot)
	primary, _ := inbox.ParseRecipient("concierge")
	list, err := messages.List(primary)
	if err != nil || len(list) != 1 {
		t.Fatalf("reply list: %v %v", list, err)
	}
	id := list[0].ID.String()
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	t.Setenv(workerLaunchGeneration, "")
	reportJSON := invoke(t, root, "", "task", "report", "task-1", id, "--format", "json")
	var report taskprotocol.Reply
	if err := json.Unmarshal([]byte(reportJSON), &report); err != nil ||
		len(report.Attachments) != 1 ||
		!bytes.Equal(report.Attachments[0].Data, data) {
		t.Fatalf("JSON report: %s %v", reportJSON, err)
	}
	for _, format := range []string{"toon", "concise"} {
		got := invoke(t, root, "", "task", "report", "task-1", id, "--format", format)
		if !strings.Contains(got, `attachments[1]{name,size,sha256}`) ||
			!strings.Contains(got, "evidence.bin") {
			t.Fatalf("attachment undiscoverable: %s", got)
		}
	}
	destination := filepath.Join(t.TempDir(), "export.bin")
	invoke(
		t,
		root,
		"",
		"task",
		"report",
		"task-1",
		id,
		"--attachment",
		"evidence.bin",
		"--output",
		destination,
	)
	exported, err := os.ReadFile(destination)
	if err != nil || !bytes.Equal(exported, data) {
		t.Fatalf("export: %v %v", exported, err)
	}
	output.Reset()
	args = []string{
		"--root",
		root,
		"task",
		"report",
		"task-1",
		id,
		"--attachment",
		"evidence.bin",
		"--output",
		destination,
	}
	if code := Run(context.Background(), args, strings.NewReader(""), &output, &output); code == 0 {
		t.Fatal("existing export overwritten")
	}
	exported, _ = os.ReadFile(destination)
	if !bytes.Equal(exported, data) {
		t.Fatal("failed export changed destination")
	}
}

func TestRemoteCLITransportsNamedAttachments(t *testing.T) {
	id := "k-" + strings.Repeat("b", 64)
	requests := remoteCLI(t, workerservice.Response{Outcome: workerservice.Completed,
		Reply: &workerservice.Reply{Root: "/primary/state", Recipient: "concierge", MessageID: id,
			Publication: taskops.ReplyConfirmed, Archive: taskops.ReplyConfirmed, Assignment: taskops.ReplyAcknowledged}})
	path := filepath.Join(t.TempDir(), "evidence.bin")
	data := []byte{0, 128, 255}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	code := Run(
		t.Context(),
		[]string{"task", "reply", "send", "task-1", "--generation", "test-generation",
			"--key", "result", "--assignment-id", id, "--body-file", "-", "--attach", "evidence.bin=" + path},
		strings.NewReader("Done"),
		&output,
		&output,
	)
	if code != 0 {
		t.Fatalf("remote reply: %s", &output)
	}
	select {
	case request := <-requests:
		if len(request.Attachments) != 1 || request.Attachments[0].Name != "evidence.bin" ||
			!bytes.Equal(request.Attachments[0].Data, data) {
			t.Fatal("CLI did not send the named attachment bytes")
		}
	default:
		t.Fatal("remote request missing")
	}
}

func TestAttachmentSourceValidation(t *testing.T) {
	directory := t.TempDir()
	regular := filepath.Join(directory, "regular")
	if err := os.WriteFile(regular, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(directory, "symlink")
	if err := os.Symlink(regular, symlink); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(directory, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	oversized := filepath.Join(directory, "oversized")
	if err := os.WriteFile(oversized, make([]byte, taskprotocol.MaxAttachmentBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name           string
		specifications []string
	}{
		{"missing separator", []string{"file"}},
		{"empty path", []string{"file="}},
		{"stdin", []string{"file=-"}},
		{"traversal", []string{"../file=" + regular}},
		{"absolute name", []string{"/file=" + regular}},
		{"directory", []string{"file=" + directory}},
		{"symlink", []string{"file=" + symlink}},
		{"fifo", []string{"file=" + fifo}},
		{"missing", []string{"file=" + filepath.Join(directory, "missing")}},
		{"oversized", []string{"file=" + oversized}},
		{"duplicate", []string{"file=" + regular, "FILE=" + regular}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := readReplyAttachments(test.specifications); err == nil {
				t.Fatal("invalid attachment source accepted")
			}
		})
	}
}

func TestAttachmentExportRefusesExistingPathsAndCorruptSets(t *testing.T) {
	attachment, _ := taskprotocol.NewAttachment("evidence.bin", []byte("abc"))
	for _, kind := range []string{"file", "symlink", "dangling symlink", "directory", "trailing slash", "corrupt", "missing name"} {
		t.Run(kind, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "export")
			target := filepath.Join(directory, "target")
			if err := os.WriteFile(target, []byte("retain"), 0600); err != nil {
				t.Fatal(err)
			}
			attachments := []taskprotocol.Attachment{attachment}
			name := attachment.Name
			switch kind {
			case "file":
				if err := os.WriteFile(path, []byte("retain"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "dangling symlink":
				if err := os.Symlink(filepath.Join(directory, "missing"), path); err != nil {
					t.Fatal(err)
				}
			case "directory", "trailing slash":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
				if kind == "trailing slash" {
					path += string(os.PathSeparator)
				}
			case "corrupt":
				attachments[0].SHA256 = "wrong"
			case "missing name":
				name = "missing.bin"
			}
			if err := exportReplyAttachment(attachments, name, path); err == nil {
				t.Fatal("unsafe export accepted")
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "retain" {
				t.Fatal("export changed an unrelated file")
			}
			if kind == "file" {
				data, _ := os.ReadFile(path)
				if string(data) != "retain" {
					t.Fatal("export overwrote existing file")
				}
			}
			if kind == "corrupt" || kind == "missing name" {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatal("invalid export created a file")
				}
			}
			entries, err := os.ReadDir(directory)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".concierge-attachment-") {
					t.Fatal("export leaked temporary bytes")
				}
			}
		})
	}
}
