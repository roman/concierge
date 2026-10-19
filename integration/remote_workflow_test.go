package integration

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/roman/concierge/pkg/herdr"
	"github.com/roman/concierge/pkg/primary"
	"github.com/roman/concierge/pkg/remoteworker"
	"github.com/roman/concierge/pkg/sshcommand"
	"github.com/roman/concierge/pkg/taskprotocol"
	"github.com/roman/concierge/pkg/taskstate"
	"github.com/roman/tincan/pkg/inbox"
)

func (run *savedMachineAcceptance) launch(kind string) string {
	id := "remote-acceptance-" + kind
	taskKind := "scout"
	work := "Read VERSION and report its value. Do not modify the repository."
	if kind == "ship" {
		taskKind = "task"
		work = "Create concierge-acceptance-proof.txt containing 'Remote acceptance completed'. Stop if that file already exists. Commit only that file. Use git -c user.name='Concierge Acceptance' -c user.email='acceptance@example.invalid' commit if an identity is needed. Do not change Git configuration."
	}
	body := work + ` This is an isolated acceptance task. Keep native permissions and user instructions active.
Do not push, open a PR, install software, change configuration, or create other work products.
After receiving the current assignment, complete the task and send its bound reply.
Use reply key acceptance-result. Write the reply body beside CONCIERGE_WORKER_SECRET_FILE, outside the repository.
Keep the worktree clean, including ignored files. Preserve the inherited generation.
Retain and retry the identical reply after a transport error. Remain idle after replying.
`
	assignment := filepath.Join(run.directory, kind+"-assignment.txt")
	if err := os.WriteFile(assignment, []byte(body), 0600); err != nil {
		run.t.Fatal(err)
	}
	run.cli(
		"backlog",
		"add",
		id,
		"Remote "+kind+" acceptance",
		"--kind",
		taskKind,
		"--repo",
		"concierge",
	)
	run.tasks = append(run.tasks, id)
	run.note("Launching native %s worker", kind)
	output := run.launchWithNativeReview(id,
		"launch",
		id,
		"--machine",
		run.machine,
		"--harness",
		"codex",
		"--project",
		run.project,
		"--branch",
		"acceptance/"+kind+"-"+run.owner,
		"--base-commit",
		run.commit,
		"--agent-arg=--no-daemon",
		"--agent-arg=--sandbox=workspace-write",
		"--agent-arg=-c",
		"--agent-arg=approvals_reviewer=auto_review",
		"--body-file",
		assignment,
	)
	worker := run.readTask(id).Snapshot().Worker
	if worker == nil || worker.Access == nil || worker.Access.Revoked ||
		worker.PendingLaunch != "" {
		run.t.Fatalf("launch needs native inspection before proceeding: %s", output)
	}
	if worker.Machine != run.profile.ID ||
		worker.Lease.Remote.Authentication != sshcommand.ForwardedAgent {
		run.t.Fatal("launch lost its saved machine or approved authentication mode")
	}
	if !insideDirectory(run.remoteDirectory, worker.Worktree) {
		run.t.Fatal("remote worktree escaped the acceptance directory")
	}
	run.note("%s: launched and assignment dispatched", id)
	return id
}

func (run *savedMachineAcceptance) restartPrimary(id string) {
	before := run.readTask(id).Snapshot().Worker
	tunnel := run.tunnel(id)
	if tunnel.Process == nil {
		run.t.Fatal("restart test requires a confirmed owned tunnel")
	}
	run.note("Restarting only the temporary primary host")
	var output string
	run.wait(time.Minute, "temporary primary restart", func() (bool, error) {
		var err error
		output, err = run.call(run.ctx, nil, "host", "restart", "--timeout", "30s")
		if err == nil {
			return true, nil
		}
		if !acceptanceRestartBusy(output, err) {
			run.t.Fatalf("temporary primary restart has an uncertain outcome: %v\n%s", err, output)
		}
		return false, err
	})
	run.note("Primary restart: %s", output)
	after := run.readTask(id).Snapshot().Worker
	recovered := run.tunnel(id)
	if after == nil || after.Generation != before.Generation || after.Lease != before.Lease ||
		after.Access == nil || after.Access.Verifier != before.Access.Verifier ||
		recovered.Process == nil || *recovered.Process != *tunnel.Process || recovered.Spec != tunnel.Spec {
		run.t.Fatal("primary restart replaced the worker, verifier, lease, or tunnel")
	}
	run.note("Primary restart preserved generation, verifier, native lease and tunnel process")
}

func (run *savedMachineAcceptance) dropAndRecoverTunnel(id string) {
	before := run.readTask(id).Snapshot().Worker
	record := run.tunnel(id)
	if record.Process == nil {
		run.t.Fatal("drop requires a confirmed owned tunnel")
	}
	// The production driver checks the process incarnation before signalling.
	if err := (remoteworker.SSHProcesses{}).Stop(run.ctx, *record.Process); err != nil {
		run.t.Fatal(err)
	}
	run.note("Deliberately stopped the temporary task's owned SSH tunnel")
	run.wait(2*time.Minute, "tunnel recovery", func() (bool, error) {
		current := run.readTask(id).Snapshot().Worker
		if current == nil || current.Generation != before.Generation ||
			current.Lease != before.Lease ||
			current.Access == nil ||
			current.Access.Verifier != before.Access.Verifier {
			run.t.Fatal("unreachable state changed the worker binding or verifier")
		}
		recovered := run.tunnel(id)
		if recovered.Process == nil || *recovered.Process == *record.Process ||
			recovered.Phase != remoteworker.TunnelRunning {
			return false, nil
		}
		if recovered.Spec != record.Spec {
			run.t.Fatal("reconnect changed the tunnel authority")
		}
		err := (remoteworker.SSHTunnelRemote{Runner: sshcommand.SystemRunner{}}).Probe(
			run.ctx,
			recovered.Spec,
		)
		return err == nil, err
	})
	run.note("Worker socket and agent socket recovered without changing generation or lease")
}

func (run *savedMachineAcceptance) boundAgent(id string) (herdr.Client, taskstate.Worker) {
	worker := run.readTask(id).Snapshot().Worker
	if worker == nil {
		run.t.Fatal("missing native worker")
	}
	client, err := herdr.NewClient("herdr").ForEndpoint(worker.Machine, worker.Session)
	if err != nil {
		run.t.Fatal(err)
	}
	return client, *worker
}

func (run *savedMachineAcceptance) waitReply(id string) string {
	messages, err := inbox.New(run.messages)
	if err != nil {
		run.t.Fatal(err)
	}
	recipient, err := inbox.ParseRecipient("concierge")
	if err != nil {
		run.t.Fatal(err)
	}
	var replyID string
	var replyBody string
	run.waitNative(id, id+" bound reply", func() (bool, error) {
		pending, err := messages.List(recipient)
		if err != nil {
			return false, err
		}
		count := 0
		for _, message := range pending {
			reply, err := taskprotocol.DecodeReply(message.Body, message.Key, run.root)
			if err != nil || reply.Task != id {
				continue
			}
			count++
			replyID = message.ID.String()
			replyBody = reply.Body
		}
		if count > 1 {
			run.t.Fatal("duplicate bound reply published")
		}
		if count == 1 {
			return true, nil
		}
		return false, nil
	})
	if run.readTask(id).Definition().Kind == "scout" {
		version := run.git(run.project, "show", run.commit+":VERSION")
		if version == "" || !strings.Contains(replyBody, version) {
			run.t.Fatal("scout reply did not report the assigned VERSION value")
		}
		run.note("%s: reply reports the committed VERSION value %s", id, version)
	}
	run.note("%s: exactly one bound reply received", id)
	return replyID
}

func (run *savedMachineAcceptance) acceptAndClean(id string) {
	replyID := run.waitReply(id)
	worker := *run.readTask(id).Snapshot().Worker
	zone, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		run.t.Fatal(err)
	}
	date := time.Now().In(zone).Format("2006-01-02")
	run.cli("backlog", "resolve", replyID, "--closed-on", date)
	accepted := run.readTask(id)
	if accepted.Definition().Kind != "scout" {
		result := accepted.Snapshot().Worker.RemoteResult
		if result.Phase != taskstate.RemoteResultReady || result.Commit == run.commit ||
			!insideDirectory(run.directory, result.Lease.Path) {
			run.t.Fatal(
				"ship result was not immutably materialized into the temporary primary pool",
			)
		}
		if changed := run.git(result.Lease.Path, "diff", "--name-only", run.commit+"..."+result.Commit); changed != "concierge-acceptance-proof.txt" {
			run.t.Fatal("ship commit did not change exactly the assigned proof file")
		}
		if proof := run.git(result.Lease.Path, "show", result.Commit+":concierge-acceptance-proof.txt"); proof != "Remote acceptance completed" {
			run.t.Fatal("ship commit did not contain the assigned proof text")
		}
		run.note("%s: immutable commit contains only the assigned proof file and required text", id)
		if destination := run.git(run.project, "remote", "get-url", "--push", "origin"); destination != run.origin {
			run.t.Fatal("landing destination changed")
		}
		run.cli("land", id)
		if tip := run.git(run.origin, "rev-parse", "refs/heads/main"); tip != result.Commit {
			run.t.Fatal("ship result did not land in the temporary origin")
		}
		run.note(
			"%s: selected commit %s landed by the primary into the temporary origin",
			id,
			result.Commit,
		)
	}
	run.wait(time.Minute, "native worker idle", func() (bool, error) {
		client, current := run.boundAgent(id)
		agent, err := client.Get(run.ctx, current.Pane)
		return err == nil &&
			(agent.Status == herdr.Idle || agent.Status == herdr.Done || agent.IsReadyWithUnknownActivity()), err
	})
	tunnel := run.tunnel(id)
	run.cli("task", "stop", id, "--generation", worker.Generation)
	run.cli("backlog", "cleanup", id, "--generation", worker.Generation)
	retired := run.readTask(id).Snapshot()
	if retired.Worker != nil || retired.Completion != nil || retired.Receipt == nil ||
		retired.Receipt.Lease.State != taskstate.LeaseReturned {
		run.t.Fatal("native worker cleanup or lease return is incomplete")
	}
	if retired.Receipt.RemoteResult.Commit != "" &&
		retired.Receipt.RemoteResult.Lease.State != taskstate.LeaseReturned {
		run.t.Fatal("primary result lease remains held")
	}
	if tunnel.Process == nil {
		run.t.Fatal("missing cleanup process evidence")
	}
	if status, err := primary.ProbeProcessToken(*tunnel.Process); err != nil ||
		status != primary.ProcessExited {
		run.t.Fatalf("owned tunnel process remains after cleanup: %s %v", status, err)
	}
	run.note("%s: native pane, tunnel, secret and both applicable leases cleaned", id)
}

func (run *savedMachineAcceptance) verifyComplete() {
	for _, id := range run.tasks {
		task := run.readTask(id)
		if task.State() != taskstate.Done || task.Snapshot().Worker != nil {
			run.t.Fatalf("unfinished acceptance task: %s", id)
		}
		if record := run.tunnel(id); record.Phase != remoteworker.TunnelClosed ||
			record.Process != nil {
			run.t.Fatalf("unclosed task tunnel: %s", id)
		}
	}
	run.note(
		"Scout and ship workflows completed with the selected saved machine, native permissions and no GitHub writes",
	)
}

func insideDirectory(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != "." && relative != ".." && !filepath.IsAbs(relative) &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
