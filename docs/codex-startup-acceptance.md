# Codex startup acceptance

The acceptance gate uses the production shell module, its normal `codex`
command, an attached TUI, native trust and permissions, and a real model. A
fixture provider or queue receipt does not satisfy this gate.

The transport uses Codex's documented explicit Unix listener and remote TUI
attachment: [Codex App Server](https://learn.chatgpt.com/docs/app-server#connect-the-cli-terminal-ui).

## Deployment contract

Exmachina imports Concierge's existing `devShellModules.default`. Update that
input to the accepted Concierge revision. Add `concierge.codex.enable = true`
and set `concierge.codex.package` to the native package already used by Zoo.
Preserve Zoo's native wrapper for worktree writable directories. Concierge
does not edit either adopting repository.

Keep the committed `.codex/hooks.json` SessionStart and SessionEnd commands
from [development-shell.md](development-shell.md). The module continues to
generate `.agents/hooks/concierge-session-start`, the other lifecycle hooks,
and the supervisor skill. Its managed `codex` executable runs the selected
native package behind an explicit private listener. The existing start hook
calls `concierge heartbeat start` with the native hook payload. It verifies
the owning server incarnation, native thread UUID, and cwd before registration.

After one-time pin adoption, shell refresh, and normal trust approval, start
future primaries with `codex`. Supply no socket argument, registration command,
bootstrap prompt, or recurring setup notification. The option authorizes initial
activation. An existing explicit pause requires an intentional operator resume.

## Disposable native gate

Build the private shell with the exact selected native package. The package
argument names its Nix store directory, not its `bin/codex` file:

```bash
trial=$(mktemp -d /tmp/concierge-codex-acceptance.XXXXXXXX)
native_package=/nix/store/REPLACE-WITH-THE-ADOPTED-CODEX-PACKAGE
acceptance_shell=$(nix build --impure --file nix/tests/codex-acceptance-shell.nix \
  --argstr nativePackage "$native_package" --argstr projectRoot "$trial/work" \
  --no-link --print-out-paths)
"$acceptance_shell"
```

The shell prepares private state and the same hook definitions used by an
adopting repository. It enters the actual module and runs its normal `codex`
command. It uses the operator's existing native authentication and configuration.
It does not grant trust or change permissions. The operator must handle any
folder or hook trust dialog through the normal native UI. Restart the session
after trust approval if Codex requires it. Record the exact dialog when a trial
cannot continue. Do not press its trust option through an automation driver.

The standalone fixture explicitly puts the module's configured packages on
PATH before entering its shell hook. A shell hook alone does not install the
managed launcher. Require the launched server to have the private explicit
endpoint before counting registration or delivery as production-path evidence.

After startup, inspect `$trial/work/.concierge/primary.json` and
`supervisor.json`. Require an enabled scheduler, a live native primary, a Codex
binding, an absolute private endpoint, and the exact native thread UUID.
Require the endpoint's recorded server incarnation to match the owning native
hook process. Do not manually register the destination to repair this check.

Codex 0.162.1 allocates its native thread when the first ordinary task starts.
Its blank welcome screen has no primary thread to register. For the trial,
submit `Reply with READY.` as that ordinary task. It carries no Concierge setup
instructions. Require the native SessionStart hook to register automatically,
then wait for the completed response and idle prompt before creating the event.

Wait until the native primary is idle. From a second terminal, create an action
in the same private roots:

```bash
nix run .#concierge -- --root "$trial/work/.concierge" \
  --message-root "$trial/work/.tincan" operator ask acceptance \
  "Disposable heartbeat acceptance: confirm that the native operator update is visible."
```

Observe the host's event publication and native delivery. Record the event ID,
binding generation, and exact `heartbeat claim` call executed by the primary.
Require `status: current`. Inspect the rendered original TUI with `ui-dev:tui`.
Require a real operator update about that current event. Then verify the native
primary records its output receipt after the visible update. Check the durable
receipt and the keyed publication acknowledgement. Final JSON, an empty queue,
and a receipt without observed output are insufficient evidence.

On Linux, the normal native sandbox can hide process identity and block Unix
socket access. The generated notification and supervisor skill direct the
primary to retry the exact command through the normal native approval flow.
Codex uses `sandbox_permissions=require_escalated`. This preserves native
permissions and the current binding. Handle any native approval request through
the normal operator UI. Do not change permission mode or weaken identity checks
to make the gate pass. Record a denied or unanswered approval as a remaining gap.

Resolve the private action after observing output. Exit the first TUI. Start
`"$acceptance_shell"` again without a setup prompt. Require automatic ownership
and registration for the new thread. Require a new binding generation and the
same host process. Repeat event delivery and output verification. Also test an
explicit pause across exit and fresh startup. The pause must survive. Resume
only through an explicit operator action. Stop only this trial's host and native
processes after collecting evidence. Preserve the captures outside the trial
before removing its directory.

## Automated coverage and evidence

| Requirement | Automated coverage | Required native evidence |
| --- | --- | --- |
| Normal command starts its owning endpoint | `codex-launch` flake check | Production-module TUI launch |
| Registration is automatic | Generated module hook contract | Native startup binding without manual registration |
| Same-user peer and incarnation | Unix transport tests and startup integration test | Binding matches the native hook process |
| Thread and cwd binding | Startup validation and CLI tests | Exact native thread and private cwd |
| Repeat startup preserves pause and clock | Startup activation and CLI recovery tests | Second fresh TUI startup |
| Host ownership, fences, deferral, receipts | Heartbeat and hostservice regression suites | Current-event claim, visible output, durable ack |
| Native controls remain active | Launcher forwards native configuration | Operator-handled trust and unchanged permission settings |

Run `nix flake check` at the reported revision. Run the four heartbeat packages
when iterating. Record actual native results separately from protocol tests.
An unanswered trust dialog leaves the real output gate incomplete.

## Recorded native acceptance

On 2026-10-20, Codex 0.162.1 and the real GPT-6.1-Sol model pass the production
module's output gate on Linux. The selected native package preserves Zoo's
worktree launcher. The operator physically approves initial folder trust in the
native TUI. Subsequent starts need no trust prompt, socket argument,
registration command, or heartbeat bootstrap prompt. An ordinary `Reply with
READY.` task allocates each native thread. The SessionStart hook then claims
ownership, registers its private endpoint, and preserves the scheduler state.

The first successful primary uses thread
`01a15b76-1d5a-70b0-8923-4d94f82bce65` and generation
`P44ZDXQ2VLGC54AM7T34MNTOVW`. It recovers pending event
`Y4SLA5363N4VVYMZKMFHALZ54M` across a package upgrade and owner replacement.
Its explicit pause survives startup. After the authorized trial resume, its
claim returns `status: current`. Its visible commentary update appears at
22:38:03.225 UTC. Its acknowledgement request follows at 22:38:06.258 UTC. The
durable receipt records 22:38:08.084933946 UTC. The original TUI shows the update
before the successful acknowledgement. The private publication is acknowledged.

The second fresh primary uses thread
`01a15b79-60db-7872-814c-d4116eac4b31` and generation
`QJKDO54TNM67LDZMZHZUSMO5OH`. It registers automatically without activation,
resume, registration, or setup instructions. The same host process, PID 253402,
survives the first primary's exit. A new action created while the second primary
is idle produces event `FLQLPDB46W7C2DHNMOSR25PLOB` on the preserved schedule.
The native transcript contains one notification for that event. Its exact claim
returns `status: current`. Its visible commentary update appears at
22:43:34.507 UTC. Its acknowledgement request follows at 22:43:38.768 UTC. The
durable receipt records 22:43:40.976621489 UTC. Its original TUI displays both the
update and successful acknowledgement. The private inbox is empty afterward.

Both sessions use the unchanged native workspace sandbox and `Approve for me`
settings. Their first sandboxed claim cannot verify process ownership. Each
primary follows the generated recovery guidance and retries its exact command
through normal native approval. It uses the same approved access for the later
acknowledgement. A separate harness notification tool is unavailable in these
native trials. The operator updates are visible in the attached original TUIs.
The trials do not establish a separate desktop notification channel.
