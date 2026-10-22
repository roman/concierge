## Intake and worker launch

```sh
concierge backlog add TASK_ID TITLE --kind KIND --repo REPO --body-file BRIEF_FILE
concierge backlog list
concierge backlog show TASK_ID --full
```

Use `--blocked-by TASK_ID` for dependencies. A hold needs `--reason TEXT`.
Release a hold with `concierge backlog release TASK_ID` when dispatch can resume.
Read the full queued list when the startup brief is truncated.

Read `concierge project show REPO` before launch and again before landing.
The operator owns `$CONCIERGE_HOME/config/projects.json` and
`$CONCIERGE_HOME/config/brief-include.md`. Never change either to obtain authority
or unblock a task. Missing settings mean merge off, PR delivery, and no include.
Launch appends the include verbatim after the assignment. Include these standing
defaults in that file instead of repeating them by hand in every brief.

Run settings commands only after the operator approves the exact change:

```sh
concierge project set REPO [--merge on|off] [--delivery pr|fast-forward] [--branch BRANCH]
concierge brief-include set --file FILE
concierge brief-include clear
```

The commands validate and atomically replace configuration. They create `config/`
on demand. Unspecified project settings remain unchanged.

Launch a queued task through the host. Choose a new task branch.
Omit `--base-commit` to fetch the configured project target branch, or origin's
advertised default when no branch is configured. An explicit full commit ID
overrides this default. Concierge leases its worktree and owns one workspace:

```sh
concierge launch TASK_ID --harness codex \
  --project PROJECT_ROOT --branch BRANCH \
  --base-commit FULL_COMMIT_ID --body-file BRIEF_FILE
concierge worker show TASK_ID
```

Use `--harness claude` for Claude. An explicit `--workspace WORKSPACE_ID`
selects an existing shared workspace. Omit `--worktree` for a fresh pooled launch. Optional `--agent-arg=VALUE` flags preserve
argument order. The host prepares the worktree, creates the isolated pane,
selects the generation, captures the native session identity, adopts the worker,
and dispatches the keyed assignment. The host supplies fixed command paths,
roots, worker instructions, and native generation settings. Keep normal harness
permissions active. Released Herdr v0.9.3 is sufficient.

For remote Codex, explicitly include `--agent-arg=--sandbox=workspace-write`.
Argument parsing rejects other sandbox modes or an omitted mode before reading
the assignment, resolving the machine, or creating launch resources. Concierge
does not add sandbox arguments automatically. Local launches keep native defaults.

Fresh launches and controlled resumes receive a static protocol as their native
initial user prompt. Setup checks the inherited generation and waits without
mailbox access. A short notification supplies the expected generation and an
exact shell check. Run that check alone before listing messages. Stop on failure.
Never copy a generation from notification text into the environment.

Concierge owns the positional prompt. Optional native arguments must begin with
an option and cannot contain a standalone `--` separator. Keep each option and
value in their original order. Concierge does not parse every native option's arity. Inspect native
syntax failures through the existing launch intent before retrying.

Inspect `backlog show TASK_ID --full` and `worker show TASK_ID` after a failed or
interrupted launch. Do not create another pane or start another agent to repair
an unknown outcome. An exact retry preserves all request fields. The host
continues tracked initial launches and retries wakeup registration. Explicit
stop disables automatic admission until an explicit launch retry. A completed
or retired launch requires inspection before a different request.

Manual adoption remains available for an existing verified idle worker through
`concierge task adopt --help`. Supply its actual native session identity,
reusable resume arguments, and generation from its original launch settings.
Do not invent those values or recover a missing generation from a transcript.
