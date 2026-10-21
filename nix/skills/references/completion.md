## Accept results and retire workers

Concierge saves every bound task reply, including each rework assignment reply, in
`$CONCIERGE_HOME/reports/` before acknowledging its assignment. Read it with
`concierge task report TASK_ID REPLY_ID`, including after worker cleanup.
List saved reply IDs and creation times with `concierge task report TASK_ID`.
When recording accepted results elsewhere, read them from these reports. Reports survive
`backlog resolve`, worker stop, and cleanup. Generic coordination messages are
not saved as reports.
This command reads historical evidence; check a pending reply through
`task reply check` before accepting its result.

Report bodies are opaque text. Separately supplied integration instructions
own any report format and downstream processing within operator-authorized
scope. Use `concierge task report TASK_ID REPLY_ID --format body > reply.md`
to pass the exact saved body to an authorized consumer.

For a scout, require findings, evidence, limitations, and open decisions in the
completion reply body. Keep unresolved operator decisions
open. Do not turn scout findings into implementation without authorization.

For project changes, delivery and merge authority are separate settings:

- `delivery: pr`: the worker pushes its branch and opens a PR.
- `delivery: fast-forward`: the worker commits a clean ready branch, removes
  ignored files, and does not push or open a PR. Keep its branch until landing.
- `merge: off`: obtain operator approval for every PR merge or default-branch landing.
- `merge: on`: the primary lands green, in-scope work without another approval.

Escalate destructive, irreversible, or security-sensitive landings to the
operator even with merge on. Standing merge authority cannot approve them.

Require completed passing checks for the exact commit and confirm its scope.
Accept the worker result, then run `concierge land TASK_ID` before stopping or
cleaning up the worker. With merge off, obtain approval for that exact landing
and pass `--operator-approved`. The command checks the recorded worktree and
branch, discovers the remote default branch, and applies the configured delivery.
Report the result's landed commit or PR URL and any skipped operation. Inspect
the remote after a failed or unconfirmed landing. Never force, discard unlanded
work, or bypass a lease refusal. A scout needs no landing.

Preserve the accepted worker generation through stop and cleanup. Check the
bound reply, then accept the result with an explicit closing date:

```sh
concierge task reply check REPLY_ID
concierge backlog resolve REPLY_ID --mode close --closed-on YYYY-MM-DD
```

For project changes, land before stopping the worker. Scouts skip this command.
With merge off, obtain the operator's approval and add `--operator-approved`:

```sh
concierge land TASK_ID
```

Stop and clean up the accepted worker:

```sh
concierge task stop TASK_ID --generation GEN
concierge backlog cleanup TASK_ID --generation GEN
```

Resolution validates the bound assignment and generation, records acceptance,
and acknowledges the reply. Retry the same reply ID, mode, and closing date
after interruption. For an unbound result, use `concierge backlog finish TASK_ID
--generation GEN --mode close --closed-on YYYY-MM-DD` after accepting it.

For a held task that must return to the queue, use `--mode retain` without a
closing date. Stop and clean up its accepted worker before releasing the hold:

```sh
concierge backlog resolve REPLY_ID --mode retain
concierge task stop TASK_ID --generation GEN
concierge backlog cleanup TASK_ID --generation GEN
concierge backlog release TASK_ID
```

Confirm the recorded pane is idle or done with `concierge control --session
SESSION status PANE_ID` before stopping it. Stop closes the accepted generation's
exact pane. If a live viewer selects that tab, move the viewer and retry.
Retry failed focus restoration after the viewer detaches or returns to its
original tab. A receipt-based stop retry preserves newer workers. Cleanup
verifies pane departure and retires the accepted binding. Do not adopt another
worker before cleanup completes. `concierge task pending` lists unfinished
completion work.
