package taskprotocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestDecodeReplyRejectsInvalidAttachments(t *testing.T) {
	valid := `{"name":"evidence.txt","size":3,"sha256":"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad","data":"YWJj"}`
	for _, test := range []struct{ name, attachments string }{
		{"checksum", strings.Replace(valid, "ba7816", "aa7816", 1)},
		{"size", strings.Replace(valid, `"size":3`, `"size":2`, 1)},
		{"traversal", strings.Replace(valid, "evidence.txt", "../evidence.txt", 1)},
		{"absolute", strings.Replace(valid, "evidence.txt", "/evidence.txt", 1)},
		{"backslash", strings.Replace(valid, "evidence.txt", `dir\\file`, 1)},
		{"duplicate", valid + "," + valid},
		{"case collision", valid + "," + strings.Replace(valid, "evidence.txt", "EVIDENCE.txt", 1)},
		{"base64", strings.Replace(valid, "YWJj", "?", 1)},
		{"missing bytes", strings.Replace(valid, `,"data":"YWJj"`, "", 1)},
		{"count", strings.TrimSuffix(strings.Repeat(valid+",", 9), ",")},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := fmt.Sprintf(
				`{"kind":"task-reply","state":"/state/tasks","task":"task-1","spawn_generation":"gen-1","body":"Done","attachments":[%s]}`,
				test.attachments,
			)
			if _, err := DecodeReply(body, "unbound", "/state"); err == nil {
				t.Fatal("invalid attachments accepted")
			}
		})
	}
}

func TestOversizedAttachmentReplyRejectedBeforeBase64Decode(t *testing.T) {
	for _, test := range []struct{ name, body string }{
		{"invalid base64", strings.Repeat(" ", MaxReplyBytes) + `{"kind":"task-reply","attachments":[{"name":"evidence.bin","data":"invalid base64"}]}`},
		{"duplicate invalid base64", strings.Repeat(" ", MaxReplyBytes) + `{"kind":"task-reply","attachments":[{"name":"evidence.bin","data":"invalid base64"}],"attachments":[]}`},
		{"duplicate valid base64", `{"kind":"task-reply","state":"/state/tasks","task":"task-1","spawn_generation":"gen-1","body":"Done","attachments":[{"name":"evidence.bin","data":"` + strings.Repeat("A", MaxReplyBytes) + `"}],"attachments":[]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := DecodeReply(test.body, "unbound", "/state"); !errors.Is(
				err,
				ErrInvalidReply,
			) {
				t.Fatalf("oversized reply reached attachment decoding: %v", err)
			}
		})
	}
}

func TestReplyPreservesAttachmentBytes(t *testing.T) {
	body := `{"kind":"task-reply","state":"/state/tasks","task":"task-1","spawn_generation":"gen-1","body":"Done","attachments":[{"name":"evidence.txt","size":3,"sha256":"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad","data":"YWJj"}]}`
	reply, err := DecodeReply(body, "unbound", "/state")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(reply)
	if err != nil || !strings.Contains(string(encoded), `"data":"YWJj"`) {
		t.Fatalf("attachment bytes lost: %s %v", encoded, err)
	}
}

func TestAttachmentSizeLimits(t *testing.T) {
	for _, test := range []struct {
		name  string
		sizes []int
		valid bool
	}{
		{"empty", []int{0}, true},
		{"file boundary", []int{MaxAttachmentBytes}, true},
		{"file overflow", []int{MaxAttachmentBytes + 1}, false},
		{"total boundary", []int{MaxAttachmentBytes, MaxAttachmentBytes}, true},
		{"total overflow", []int{MaxAttachmentBytes, MaxAttachmentBytes, 1}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var attachments []Attachment
			for index, size := range test.sizes {
				data := make([]byte, size)
				attachment, err := NewAttachment(fmt.Sprintf("file-%d", index), data)
				if err != nil {
					if !test.valid && size > MaxAttachmentBytes {
						return
					}
					t.Fatal(err)
				}
				attachments = append(attachments, attachment)
			}
			if err := ValidateAttachments(attachments); (err == nil) != test.valid {
				t.Fatalf("validation: %v", err)
			}
		})
	}
}

func TestLegacyReplyHasNoNewSizeLimit(t *testing.T) {
	reply := Reply{
		Kind:            "task-reply",
		State:           "/state/tasks",
		Task:            "task-1",
		SpawnGeneration: "gen-1",
		Body:            strings.Repeat("x", MaxReplyBytes+1),
	}
	body, err := json.Marshal(reply)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeReply(string(body), "unbound", "/state"); err != nil {
		t.Fatalf("legacy reply rejected: %v", err)
	}
}
