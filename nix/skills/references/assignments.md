## Bound assignments and replies

The primary sends a task assignment:

```sh
concierge task send TASK_ID --key ASSIGNMENT_KEY --body-file BRIEF_FILE
```

Choose one unique key per logical message. Retry the same key only with the same
body. Durable delivery precedes notification. Accept `notification: sent` or
`acknowledged` or `deferred`. A deferred notification retains the durable
assignment. Wait for the existing worker or a `task-stalled` notice. On
`notification: failed`, preserve the durable message ID,
correct the cause, and retry the same request. Keep the pending message.

The worker lists message IDs, then validates its assignment:

```sh
tincan inbox list TASK_ID
concierge task receive TASK_ID MESSAGE_ID --generation "$CONCIERGE_SPAWN_GENERATION"
concierge task reply send TASK_ID --generation "$CONCIERGE_SPAWN_GENERATION" --key REPLY_KEY \
  --assignment-id MESSAGE_ID --body-file RESULT_FILE
```

Act only on `status: current`. Stale assignments have no actionable body.
For an assignment without a reply, use `concierge task ack TASK_ID MESSAGE_ID
--generation "$CONCIERGE_SPAWN_GENERATION"` after handling it. Reply publication precedes assignment
acknowledgement. An identical keyed retry repairs interrupted acknowledgement.
Use generic `tincan inbox send RECIPIENT --key KEY --body-file FILE` for independent
coordination messages.
