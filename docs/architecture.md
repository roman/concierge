# Architecture

Concierge owns task state and worker lifecycle. Tincan owns durable message
delivery. Herdr owns terminal sessions and agent processes. Treehouse owns
worktree leases. The host coordinates those external effects through recorded
task transitions rather than treating a successful subprocess as durable state.

`cmd/concierge` starts the command-line adapter in `pkg/conciergecli`.

`pkg/conciergecli` parses commands, validates caller options, and renders command
results. It depends on `hostservice`, `taskops`, `taskstate`, `taskview`, `primary`,
`control`, `supervise`, `herdr`, `firstmate`, `internal/lockfile`, and Tincan inboxes
for operations, settings, and validation.

`pkg/hostservice` admits primary-owned requests over a Unix socket. It owns host
configuration, endpoint reservations, background recovery, and controller
lifetimes. It depends on `taskops`, `taskstate`, `primary`, `resident`, `supervise`,
`herdr`, `control`, `worktree`, `firstmate`, `stateversion`, and Tincan messaging.

`pkg/taskops` executes task operations across state, messages, panes, and leases.
It owns assignment delivery, replies, adoption, launch, restart, stop, and
retirement. It depends on `taskstate`, `taskprotocol`, `taskview`, `firstmate`,
`herdr`, `presentation`, `supervise`, `worktree`, and Tincan messaging.

`pkg/taskstate` owns authoritative version 2 task records, locked transitions,
launch intents, recovery plans, lease identities, backups, and migration. It
depends on `firstmate`, `herdr`, `stateversion`, `internal/lockfile`, and Tincan
message types. Its
legacy inventory also reads `crew` records.

`pkg/taskprotocol` validates assignment and reply envelopes against a task and
spawn generation. It depends on `firstmate` for identity validation and Tincan
for message representation.

`pkg/taskview` builds briefs, task views, worker views, and backlog projections.
It depends on `taskstate`, `stateversion`, `internal/lockfile`, and Tincan inboxes.

`pkg/herdr` provides typed worker-control, pane, workspace, screen, and process
health operations. It uses Tincan's shared Herdr command client for execution,
session selection, and common error handling. Local process inspection uses
platform process tools.

`pkg/control` verifies worker identity before interrupting or starting an agent.
It depends on `herdr`.

`pkg/presentation` serializes pane changes and restores the user's terminal
focus. It depends on `herdr` and `firstmate` locking.

`pkg/primary` verifies the owning root session through native process identities
and hook events. It uses operating-system process and file APIs. Darwin builds
require Cgo for kernel peer credentials and process identity.

`pkg/resident` runs a controller's health supervision and event-driven inbox
watcher. It owns controller leases. It depends on `supervise`, `firstmate`,
Tincan inboxes and notifications, and filesystem notifications.

`pkg/supervise` runs the health and recovery loop against a worker interface.
It depends on Capataz for restart limits. Worker adapters supply health and
recovery operations through the package's interface.

`pkg/worktree` prepares task branches and manages Treehouse lease acquisition,
verification, and return. It invokes Git and Treehouse through typed adapters.

`pkg/stateversion` prevents task operations from crossing a migration boundary.
It depends on `internal/lockfile` for the shared state-version gate.

`pkg/firstmate` validates task IDs, generations, and paths. It also reads legacy
task metadata and provides guarded metadata creation and path locking. It uses
standard-library filesystem APIs.

`pkg/crew` reads and updates legacy worker registry records, adoption records,
generation rotation, and completion receipts. It depends on `firstmate`,
`herdr`, `presentation`, and `internal/lockfile`. Version 2 migration reads this
registry; new task operations use `taskstate`.

`internal/lockfile` opens lock files without following symlinks.

`internal/nativetest` supplies native parent-process fixtures for integration
tests. It depends only on standard-library process APIs.

`integration` exercises the command boundary and lifecycle across packages.
