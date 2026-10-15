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
or confirms an existing version 2 root. It preserves primary ownership files,
the installed `profile` symlink, and existing version 2 task records. It rejects legacy
backlogs, unknown fresh-root entries, pending migrations, and unsafe file types.
Stop state writers and installers during fresh initialization.

Task records under `.concierge/records/` are authoritative. `backlog.md` is a
readable projection rebuilt by task operations. Edit tasks through the command.
A failed projection can leave a committed task change; inspect the task and
retry the same request after correcting the storage error.

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
