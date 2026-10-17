package remoteworker

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/herdr"
)

type baseImportSSHFixture struct {
	t        *testing.T
	request  WorktreeRequest
	response string
	calls    int
}

func (fixture *baseImportSSHFixture) Run(
	_ context.Context,
	arguments []string,
	input io.Reader,
) ([]byte, []byte, error) {
	fixture.calls++
	if !strings.Contains(arguments[len(arguments)-1], "'worker-worktree' 'import'") {
		fixture.t.Fatal("used an unfixed import command")
	}
	request, bundle, err := DecodeBaseImport(input)
	if err != nil || request != fixture.request {
		fixture.t.Fatal("changed base import ownership")
	}
	data, err := io.ReadAll(bundle)
	if err != nil || string(data) != "bundle bytes\x00\n" {
		fixture.t.Fatal("changed the bundle payload")
	}
	return []byte(fixture.response), nil, nil
}

func TestRemoteBaseTransportKeepsBundleOnStdin(t *testing.T) {
	request := remoteWorktreeRequest(t)
	profile := herdr.SavedMachine{
		ID:      request.Location.Machine,
		Target:  request.Location.Target,
		Enabled: true,
	}
	fixture := &baseImportSSHFixture{t: t, request: request, response: "status: imported\n"}
	transport := WorktreeTransport{Runner: fixture}
	if err := transport.ImportBase(context.Background(), profile, request, strings.NewReader("bundle bytes\x00\n")); err != nil {
		t.Fatal(err)
	}
	fixture.response = "status: imported\nunexpected"
	if err := transport.ImportBase(context.Background(), profile, request, strings.NewReader("bundle bytes\x00\n")); err == nil {
		t.Fatal("accepted an ambiguous import response")
	}
	profile.Target = "changed-target"
	if err := transport.ImportBase(context.Background(), profile, request, strings.NewReader("bundle bytes\x00\n")); err == nil {
		t.Fatal("followed a changed target")
	}
	if fixture.calls != 2 {
		t.Fatal("contacted a changed target")
	}
}

func TestRemoteBaseImportRejectsMalformedHeader(t *testing.T) {
	request := remoteWorktreeRequest(t)
	header, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range [][]byte{
		header,
		[]byte(strings.Repeat("x", 64<<10) + "\n"),
		[]byte("{\"unknown\":true}\nbytes"),
		append(append(bytes.Clone(header), []byte(" {}\n")...), []byte("bytes")...),
	} {
		if _, _, err := DecodeBaseImport(bytes.NewReader(input)); err == nil {
			t.Fatal("accepted an invalid import header")
		}
	}
}
