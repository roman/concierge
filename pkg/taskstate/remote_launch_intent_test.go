package taskstate

import (
	"bytes"
	"testing"
)

func remoteInitialLaunchFixture() LaunchIntent {
	remote := remoteLeaseFixture().Remote
	remote.Backend = ""
	request := launchForTest()
	request.Machine = remote.Machine
	request.Remote = remote
	request.Project = remote.ProjectPath()
	request.Worktree = ""
	return request
}

func TestRemoteLaunchRetainsPreparationRoutingWhenAbandonedBeforeLeaseCapture(t *testing.T) {
	request := remoteInitialLaunchFixture()
	task, err := taskForTransition(t).ReserveInitialLaunch(request)
	if err != nil {
		t.Fatal(err)
	}
	task, err = task.BeginLaunchPreparation(request.Generation)
	if err != nil {
		t.Fatal(err)
	}
	task, err = task.RequestLaunchLease(request.Generation, "physical-task-holder")
	if err != nil {
		t.Fatal(err)
	}
	task, err = task.AbandonInitialLaunch(request.Generation)
	if err != nil {
		t.Fatal(err)
	}
	receipt, found := task.LaunchAbandonment(request.Generation)
	if !found || receipt.Remote != request.Remote || receipt.Lease != (WorktreeLease{}) {
		t.Fatal("lost routing for an interrupted acquisition")
	}
	data, err := task.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"format":3`)) {
		t.Fatal("abandoned remote launch lost its format gate")
	}
	restored, err := Decode(data)
	if err != nil {
		t.Fatal(err)
	}
	receipt, found = restored.LaunchAbandonment(request.Generation)
	if !found || receipt.Remote != request.Remote {
		t.Fatal("lost remote routing during reload")
	}
	legacy := bytes.Replace(data, []byte(`"format":3`), []byte(`"format":2`), 1)
	if _, err := Decode(legacy); err == nil {
		t.Fatal("read remote preparation metadata as format two")
	}
}

func TestRemoteLaunchRejectsReboundPreparationLocation(t *testing.T) {
	for _, change := range []func(*LaunchIntent){
		func(intent *LaunchIntent) { intent.Machine = "different-machine" },
		func(intent *LaunchIntent) { intent.Project = "/other/project" },
		func(intent *LaunchIntent) { intent.Remote.Target = "-invalid" },
		func(intent *LaunchIntent) { intent.Remote.Backend = RemoteGit },
	} {
		request := remoteInitialLaunchFixture()
		change(&request)
		if _, err := taskForTransition(t).ReserveInitialLaunch(request); err == nil {
			t.Fatal("reserved an inconsistent remote launch")
		}
	}
}
