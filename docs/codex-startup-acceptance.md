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

After startup, inspect `$trial/work/.concierge/primary.json` and
`supervisor.json`. Require an enabled scheduler, a live native primary, a Codex
binding, an absolute private endpoint, and the exact native thread UUID.
Require the endpoint's recorded server incarnation to match the owning native
hook process. Do not manually register the destination to repair this check.

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
