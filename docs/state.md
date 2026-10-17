# State roots and migration


From the project root, set absolute paths once:

```sh
export CONCIERGE_HOME="$PWD/.concierge"
export TINCAN_HOME="$PWD/.tincan"
concierge state init
concierge
```

`--root` overrides `CONCIERGE_HOME` for Concierge and `TINCAN_HOME` for Tincan.
Concierge's `--message-root` overrides `TINCAN_HOME`. Without these settings,
the commands use `.concierge` and `.tincan` under the current directory.
Concierge requires clean absolute state paths. Its primary inbox recipient is
`concierge`; use `--recipient NAME` consistently when selecting another primary.

`state init` requires an existing parent directory. It initializes a fresh root
or confirms an existing version 2 or 3 root. It preserves primary ownership files,
the installed `profile` symlink, and existing version 2 task records. It rejects legacy
backlogs, unknown fresh-root entries, pending migrations, and unsafe file types.
Stop state writers and installers during fresh initialization.

Task records under `.concierge/records/` are authoritative. `backlog.md` is a
readable projection rebuilt by task operations. Edit tasks through the command.
A failed projection can leave a committed task change; inspect the task and
retry the same request after correcting the storage error.

`operator-actions.json` stores open operator requests and reminder timestamps.
Operator commands serialize changes with `.operator-actions.lock` and replace
the ledger atomically. These commands require initialized version 2 or 3 state.
An absent ledger represents no requests and no previous reminders. Malformed
or unreadable ledgers stop the command. Resolved requests are removed; pending
worker retirement is derived from task records. Back up this ledger with the
state root to preserve outstanding requests and reminder intervals.

### Migrate existing task state

Stop all legacy task writers and controllers first. Keep them stopped until
all task callers use the version 2 commands. Choose an absolute backup path
beside the state root:

```sh
concierge state backup --backup /absolute/concierge-state-backup
concierge state migrate --backup /absolute/concierge-state-backup
```

`backup` refuses an existing destination. `migrate` creates a backup or verifies
and reuses an existing one. It converts the legacy records and publishes
version 2 after validation. An interrupted migration blocks task operations
until recovery verifies the backup and converted records:

```sh
concierge state recover --backup /absolute/concierge-state-backup
```

Recovery completes a pending switch. It does not restore an arbitrary backup.

### Upgrade local state for remote records

Version 3 provides the state boundary for remote worker records. Current
commands accept version 2 and version 3. Older version 2 commands refuse a
version 3 root before reading or changing its records.

Retire every worker and finish or abandon every launch before the upgrade.
Stop the host, state writers and installers. Select a new absolute sibling
backup directory:

```sh
concierge state upgrade --backup /absolute/concierge-version-two-backup
```

The upgrade validates the task records, copies the complete root, verifies
the backup, and publishes the version 3 marker. It preserves the existing
record bytes. It refuses a root with a worker or a launch intent. Restart
with current commands after the upgrade completes.

An interrupted upgrade blocks task operations. Keep writers stopped and use
the original backup:

```sh
concierge state recover-upgrade --backup /absolute/concierge-version-two-backup
```

Recovery compares all task records against that backup before finishing the
marker. It refuses changed records. `state recover` remains restricted to the
legacy version 1 to 2 migration.
