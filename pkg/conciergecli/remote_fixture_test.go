package conciergecli

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/roman/concierge/pkg/workeraccess"
	"github.com/roman/concierge/pkg/workerservice"
)

func TestRemoteFixtureRequiresCompleteRequestFrame(t *testing.T) {
	request := workerservice.Request{
		Task: "task-1", Generation: "test-generation", Secret: "fixture-secret",
		Operation: workeraccess.ReadSettings,
	}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	for _, trial := range []struct {
		name, suffix string
		wantOutcome  workerservice.Outcome
	}{
		{"one object", "", workerservice.Completed},
		{"trailing object", "{}", ""},
		{"trailing invalid data", "invalid", ""},
	} {
		t.Run(trial.name, func(t *testing.T) {
			connection := &remoteFixtureConnection{
				request: strings.NewReader(string(data) + trial.suffix),
			}
			requests := make(chan workerservice.Request, 1)
			respondRemoteCLI(
				connection,
				workerservice.Response{Outcome: workerservice.Completed},
				requests,
			)
			if connection.responseBeforeEOF {
				t.Fatal("fixture responded before request EOF")
			}
			if trial.wantOutcome == "" {
				if connection.response.Len() != 0 || len(requests) != 0 {
					t.Fatal("fixture accepted trailing request data")
				}
				return
			}
			var response workerservice.Response
			if err := json.Unmarshal(connection.response.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if response.Outcome != trial.wantOutcome {
				t.Fatalf("response outcome: %s", response.Outcome)
			}
			select {
			case received := <-requests:
				if !reflect.DeepEqual(received, request) {
					t.Fatalf("fixture changed the request: %v", received)
				}
			default:
				t.Fatal("fixture did not retain the completed request")
			}
		})
	}
}

type remoteFixtureConnection struct {
	request           io.Reader
	response          bytes.Buffer
	requestEOF        bool
	responseBeforeEOF bool
}

func (connection *remoteFixtureConnection) Read(data []byte) (int, error) {
	count, err := connection.request.Read(data)
	if err == io.EOF {
		connection.requestEOF = true
	}
	return count, err
}

func (connection *remoteFixtureConnection) Write(data []byte) (int, error) {
	if !connection.requestEOF {
		connection.responseBeforeEOF = true
	}
	return connection.response.Write(data)
}
