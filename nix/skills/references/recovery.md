## Supervision

Inspect bindings with `concierge worker show TASK_ID`. Inspect live endpoints
with `concierge control --session SESSION status PANE_ID`. Run `concierge supervise
task TASK_ID` for a confirmed task. It validates the current binding, prompts
pending inbox work, and relaunches missing workers with recorded resume arguments.
Report stale Herdr agent registrations that prevent recovery. Leave the worker
stopped until the registration is repaired. Manual pane supervision is available
through `concierge supervise pane --help`.


A restart reserves one generation, publishes its bootstrap durably, and supplies
that generation through the harness's native settings. It retains the same
reservation through retries. The new worker uses its launch environment even
when its resumed conversation contains an older generation.

A `task-bootstrap` message is coordination. Read it through the generic inbox
when needed. It can confirm the inherited generation but cannot supply a missing
one. Worker receive, ack, and reply commands reject missing or mismatched launch
context before accessing storage or result bodies. A cold-restored worker without
that context must stop task work and request a controlled relaunch. Do not repair
it from a transcript, registry read, or mailbox. A generic bootstrap acknowledgement
confirms message handling; it does not establish worker readiness.

## Native permission checkpoints

Claude permission settings apply to local task replies. Auto mode can classify a
reply as an external write and require approval. Report the exact denied action
and use the native permission dialog after operator approval. Retry that action
with the same logical key. Keep ordinary permission settings active.

Repeated notifications wait while a worker is working or blocked. The durable
message stays pending until the watcher can notify a ready worker. Do not send
extra prompts from observation or recovery.
