# Codex command access

Keep native approvals, sandbox mode, trust, process ownership, launch generation,
and landing checks active. An access failure grants no authority to change them.
Never change a generation or identity variable to make a command pass.

## Choose access for the command

| Commands | Required resources |
| --- | --- |
| `concierge project show`, `tincan inbox list/read`, ordinary `cat`, local Git reads | Files only. Start in the sandbox. Do not inherit escalation from an unrelated command. |
| `concierge backlog list/show`, `worker list/show`, `task list/report` | Files and a shared version lock. An existing lock needs read access. A missing lock still needs creation in the state root's parent. These reads do not call the host. |
| `concierge heartbeat claim/ack`, primary verification | Native process identity and state writes. A sandbox can hide the primary's PID namespace or process metadata. |
| Host readiness, intake, launch, worker control, acceptance, landing, cleanup, Herdr pane reads | Unix sockets. A visible socket path does not establish permission to connect. |
| `task receive/ack/reply`, operator actions, heartbeat state, state migration | State locks or writes. A read-shaped command can still need writable state. `task pending` also reads operator actions through a lock. |
| Remote machine checks, SSH, Git fetch/push | Network and possibly local sockets or state writes. Local file-read access does not establish network access. |

Use the returned claim state for its authorized decisions. Avoid extra status
reads unless a new decision requires them. Current pane reads and status checks
before pane actions remain mandatory.

Once a command has failed for a known sandbox restriction, request
`sandbox_permissions=require_escalated` through native approval on its next
authorized invocation. For example, a restricted claim and its receipt need
native process visibility. Do not repeat the known failing sandbox attempt on
each wake. This does not authorize other commands or a reusable broad shell rule.
Batch independent calls with their individual access settings.

## Handle a failure once

For `process ownership cannot be confirmed`, a denied Unix socket, or a denied
state path, retry the exact command once through native approval. Preserve the
executable, roots, recipient, generation, and other arguments. If approval is
denied, keep the event or assignment pending and report the access failure.
If the approved retry fails, report that result before another attempt.

A missing or refused socket requires host inspection. A stale generation, expired
claim, lease refusal, native trust checkpoint, or missing configuration requires
its documented recovery. Escalation does not repair those conditions. Never
restart a host solely because the sandbox denies its socket.

No hook changes operator-owned Codex configuration or grants access automatically.
See `docs/codex-command-access.md` in the source repository for replay results
and limits.
