package integration

import (
	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/taskstate"
)

func (run *savedMachineAcceptance) launchWithNativeReview(id string, arguments ...string) string {
	run.t.Helper()
	output, err := run.call(run.ctx, nil, arguments...)
	if err == nil {
		return output
	}
	snapshot := run.readTask(id).Snapshot()
	if snapshot.Worker != nil && snapshot.LaunchIntent == nil {
		return output
	}
	intent := snapshot.LaunchIntent
	if intent == nil || intent.Phase != taskstate.LaunchAgentRequested || intent.Endpoint == nil {
		run.t.Fatalf("launch failed before native startup: %v\n%s", err, output)
	}
	client, endpointError := herdr.NewClient("herdr").ForEndpoint(intent.Machine, intent.Session)
	if endpointError != nil {
		run.t.Fatal(endpointError)
	}
	agent, inspectionError := client.Get(run.ctx, intent.Name)
	if inspectionError != nil || agent.PaneID != intent.Endpoint.Pane ||
		agent.Kind != intent.Harness {
		run.t.Fatalf("launch failed without a confirmed native agent: %v\n%s", err, output)
	}
	run.note(
		"%s native startup: machine %s, session %s, workspace %s, pane %s, state %s. Resolve any native dialog; automatic reconciliation remains active.",
		id,
		intent.Machine,
		intent.Session,
		intent.Endpoint.Workspace,
		intent.Endpoint.Pane,
		agent.Status,
	)
	run.waitNative(id, id+" native startup review", func() (bool, error) {
		current := run.readTask(id).Snapshot()
		if pending := current.LaunchIntent; pending != nil {
			if pending.Generation != intent.Generation || pending.Endpoint == nil ||
				*pending.Endpoint != *intent.Endpoint {
				run.t.Fatal("native review changed the launch generation or endpoint")
			}
			return false, nil
		}
		worker := current.Worker
		if worker == nil || worker.Generation != intent.Generation ||
			worker.Pane != intent.Endpoint.Pane {
			run.t.Fatal("native review lost the original worker binding")
		}
		return worker.PendingLaunch == "", nil
	})
	return output
}
