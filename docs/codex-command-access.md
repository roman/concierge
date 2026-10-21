# Predictable Codex command access

Existing shared version locks now open for reading. This removes an unnecessary
write requirement from task-record reads outside the writable workspace. All
version validation, migration exclusion, pinned-root checks, and record checks
remain in place. Missing locks still require creation. Exclusive migration locks
retain their existing access requirements.

Concierge errors for denied Unix sockets, denied filesystem paths, and unknown
process ownership now name one exact-command retry through native approval.
The supervisor skill distinguishes file reads from socket, process, write, and
network operations. It requests the appropriate access first after observing a
restriction. Neither the command nor a hook changes native permissions or retries
automatically.

## Historical command inventory

The fixed audit interval is 2026-10-20 22:05:50 UTC through 2026-10-21 04:05:50
UTC. The source is the two Exmachina primary rollouts ending in
`01a15b4e-127c-71d1-bace-d0a6c3d5a243` and
`01a15be3-5dbc-73f3-ba17-cda1f92882ea`, under
`~/.codex/sessions/2026/10/20/`. This command-specific inventory reuses the
earlier audit interval. It does not repeat the token audit.

Totals reconcile with `/tmp/usage-measurement-tool-baseline.toon`: **478 literal
escalation arguments and 32 nonzero command results** in primary sessions.
The definitions match `concierge-usage` and [session usage](session-usage.md).
Decode tool-result JSON text blocks before counting exits. Count call arguments
and results at their own timestamps. Keep associations across the window edge.

An escalation argument records a request, not evidence that escalation was
necessary or approved. JavaScript loops can execute a literal argument more than
once. These counts describe literal sites, not the number of resulting native
reviews. A shell batch can contain multiple failing subcommands but yield one
recorded exit. The counts below must not be interpreted as individual shell
operation counts.

The escalation table assigns each literal site to its command or batch. A batch
with several command classes uses its first identifiable command. Nine dynamic
sites remain mixed. **All escalation causes are inferred resource classes**,
not observed failures. File-only reads appear under `other` because the saved
call does not establish a need for elevation. Herdr commands with a remote
machine also use network access. The table assigns them to their socket entry
point to avoid counting them twice.

| Inferred cause | Escalation arguments | Nonzero results |
| --- | ---: | ---: |
| Socket access | 278 | 1 |
| Process identity | 72 | 1 |
| State-root write | 52 | 1 |
| Network | 39 | 1 |
| Other / mixed / need unproven | 37 | 28 |
| **Total** | **478** | **32** |

Ranked escalation inventory:

| Command or batch | Inferred cause | Count |
| --- | --- | ---: |
| Herdr pane, agent, and review commands | socket | 162 |
| `heartbeat claim` | process | 36 |
| `heartbeat ack` | process | 35 |
| Git fetch, push, or ls-remote batches | network | 24 |
| `task send` | socket | 15 |
| `launch` | socket | 15 |
| `task reply check/send` | state write | 15 |
| `backlog resolve` | socket | 14 |
| `backlog cleanup` | socket | 13 |
| `task stop` | socket | 12 |
| Tincan `inbox list` | other | 10 |
| `backlog add` | socket | 9 |
| `operator resolve` | state write | 9 |
| Dynamic command batches | other | 9 |
| Local Git mutations | state write | 8 |
| SSH commands | network | 8 |
| `project show` | other | 7 |
| `host status` | socket | 7 |
| `backlog release-worktree` | socket | 6 |
| UI and review commands | socket | 5 |
| `operator ask` | state write | 5 |
| `land` | socket | 5 |
| `backlog hold` | state write | 5 |
| `machine check` | network | 4 |
| `worker show` | other | 4 |
| `task abandon` | socket | 4 |
| `host restart` | socket | 3 |
| Nix / direnv batches | socket | 3 |
| `task pending` | other | 3 |
| `state upgrade` | state write | 2 |
| `host serve` | socket | 2 |
| `machine prepare` | network | 2 |
| `backlog release` | state write | 2 |
| `heartbeat show` | state write | 2 |
| `supervise stop` | socket | 2 |
| `task report` | other | 2 |
| `heartbeat enable` | state write | 1 |
| `host stop` | socket | 1 |
| Host log and process inspection | process | 1 |
| Configuration file write | state write | 1 |
| `machine status` | network | 1 |
| `backlog list` | other | 1 |
| Tincan `inbox ack` | state write | 1 |
| `backlog show` | other | 1 |
| Vault write | state write | 1 |

Ranked nonzero inventory follows. The failed subcommand is identified from the
result when a batch includes other commands. `Other` includes expected search
misses and native checkpoints. Such exits do not establish a sandbox fault.
The remote prerequisite result concerns repository network access; it does not
establish a local sandbox network denial.

| Failed command or result | Cause | Count |
| --- | --- | ---: |
| Launch: folder-trust checkpoint | other | 2 |
| Heartbeat receipt: expired claim | other | 2 |
| Task abandonment: treehouse flag version | other | 2 |
| Herdr wait: timeout | other | 2 |
| Note help: missing argument | other | 1 |
| UI command: missing tool path | other | 1 |
| UI client inspection | other | 1 |
| Heartbeat claim: ownership cannot be confirmed | process | 1 |
| Backlog intake batch: Unix connection denied | socket | 1 |
| Machine configuration parse | other | 1 |
| State-file glob search | other | 1 |
| Git path lookup | other | 1 |
| Repository file search | other | 1 |
| State upgrade: backup location | other | 1 |
| Host restart: missing socket | other | 1 |
| Remote machine: repository prerequisites | network | 1 |
| Remote machine: build failure | other | 1 |
| Launch: reserved controller | other | 1 |
| Launch: worktree argument | other | 1 |
| Launch: unresolved abandoned lease | other | 1 |
| Launch: cancelled Herdr command | other | 1 |
| Herdr: missing agent | other | 1 |
| Review directory search | other | 1 |
| Review search: no match | other | 1 |
| Vault validation | other | 1 |
| Nix cache: database write denied | state write | 1 |
| Git commit: hook migration mode | other | 1 |
| Note target lookup | other | 1 |

Child sessions remain separate, matching the baseline's **9 escalation arguments
and 3 nonzero results**. Their requests comprise Git fetch (1, network), host
status (1, socket), worker show (1, other), and Herdr agent/pane/workspace commands
(6, socket). Their failures comprise task-file searches (2) and a historical Git
source lookup (1), all `other`. Help commands in those batches do not by
themselves need escalation.

## Safe before/after replay

The baseline binary was built from `origin/main` before edits. Both binaries ran
the same eight-command set against the same synthetic temporary root. The root
contains a queued task and a valid synthetic bound worker. It contains no live
worker resources. The persistent version lock has mode `0400`; task and records
directories have mode `0500`. This reproduces a readable state root outside a
writable workspace without accessing the live Concierge root.

The command set includes five task-record reads, one file-only configuration
read, an exclusive migration attempt, and a denied Unix connection. The socket
fixture is a user-owned temporary Unix socket with mode `0000`. The outer native
sandbox returns `EPERM` for its connection. No retry, migration, or network
operation is performed against the live root.

| Cause / command set | Commands | Blocked before | Blocked after | Reduction |
| --- | ---: | ---: | ---: | ---: |
| State access: five shared-lock reads | 5 | 5 | 0 | 100% |
| State access: exclusive migration | 1 | 1 | 1 | 0% |
| State access combined | 6 | 6 | 1 | 83.3% |
| Socket access: host status | 1 | 1 | 1 | 0% |
| Other: project settings read | 1 | 0 | 0 | unchanged |
| **Total** | **8** | **7** | **2** | **71.4%** |

Commands, with the temporary roots supplied explicitly:

```text
backlog list
backlog show sample
worker list
worker show bound
task list
project show sample
state upgrade --backup /tmp/codex-command-access-fixture/backup
host status
```

All six reads pass after the change. Migration and socket controls retain
nonzero exits. The replay deliberately makes **zero native escalation requests
before and after**. Permission-blocked commands that would require additional
approved access decrease from seven to two. This is an access-requirement proxy,
not a measured reduction in native approval requests or quota use.

The replay script, synthetic root, raw results, and classified call-site evidence
are retained under `/tmp/codex-command-access-*` for acceptance. Re-run:

```sh
bash /tmp/codex-command-access-replay.sh /tmp/codex-command-access-before \
  /tmp/codex-command-access-fixture
bash /tmp/codex-command-access-replay.sh /tmp/codex-command-access-after \
  /tmp/codex-command-access-fixture
```

Native sandbox nesting could not be started: the installed Codex command rejected
its inherited app-server socket directory. The filesystem portion therefore uses
DAC permissions, not a nested Codex filesystem sandbox. Process identity is
covered by the existing ownership tests and a wrapped-error guidance test. Its
historical sandbox failure was not replayed. Network behavior and operator-owned
Nix cache access were not replayed. Their before/after benefit remains unmeasured.

The historical primary already had writable Exmachina state. The read-lock replay
primarily establishes the benefit for read-only or outside-workspace layouts.
**No reduction in the historical 478 escalation arguments is established.**
Skill guidance can avoid repeated failed attempts and unnecessary elevation of
file reads, but that behavioral benefit needs a matched supervision run.

## Changes and limits by cause

- Socket: retain the transport and its approval boundary. Give one native retry
  in access-denied errors. Remember an observed restriction for the same
  operation and roots. Missing or refused sockets still require host inspection.
- Process: retain PID namespace and process-incarnation checks. Request native
  access first for a known restricted claim and its receipt. An expired claim
  still requires fresh verification.
- State write: existing shared locks no longer demand writes. Missing locks,
  migrations, assignments, and other mutations retain their write requirements.
- Network: use native approval for an observed network restriction. No repository
  change can grant network access within the operator's sandbox policy.
- Other: keep file-only reads in the sandbox. Report configuration, lease,
  dependency, search, and checkpoint failures according to their actual cause.

No operator configuration change is proposed. In particular, adding writable
roots would not resolve process visibility or Unix socket restrictions. Broad
automatic escalation would increase authority and would retain the review cost.
Native permission guidance follows [OpenAI's security documentation](https://developers.openai.com/codex/security/).
