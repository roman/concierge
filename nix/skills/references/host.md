## Host readiness

The owning root SessionStart hook checks the pinned host before printing the
brief. It starts a detached host only after a missing or refused socket. The
host survives root session exit. Its log is `.concierge/host.log`.

```sh
concierge host status --timeout 1s
```

A ready result verifies the roots, recipient, and supplied Herdr session.
It reports the host revision, calling binary revision, and `upgrade_required`.
An upgrade warning leaves the existing host running.
It does not establish every worker's recovery. An incompatible or unresponsive
host requires inspection. Do not replace it automatically or start a second
host manually. Root startup allows a bounded readiness wait. A slow host can
continue recovery after that wait; inspect the status and log before retrying.

A root outside Herdr can reuse a compatible host. A new host without a Herdr
session supports backlog operations. Worker launch requires a Herdr session.
## Upgrading Concierge

Land the Concierge change on `main`. Bump the Concierge flake pin in the
adopting repository. Run these commands in that repository:

```sh
direnv reload
concierge host status
concierge host restart
```

Then start a new primary session. The primary is the agent session. The host
is the background process. A new primary session is needed because the agent
keeps the PATH it started with.

`host restart` starts the calling binary with the existing roots, Herdr session,
and monitor settings. It leaves workers running. It refuses during a launch or
landing, including an unfinished launch reservation. Finish or inspect that
operation before retrying. `concierge host stop` safely stops only the host.
The root SessionStart hook starts a missing host on the next primary session.
An older host without the shutdown endpoint requires a manual stop after
inspection. Never kill workers to upgrade the host.
