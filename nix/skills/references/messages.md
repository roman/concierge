## Primary messages

```sh
tincan inbox list concierge
tincan inbox read concierge MESSAGE_ID
concierge task reply check MESSAGE_ID --format concise
```

Read ordinary coordination messages with Tincan. For a bound worker reply, use
`concierge task reply check MESSAGE_ID --format concise` for the first inspection.
Act on its body only with `status: current`. Before acceptance, run a fresh
`concierge task reply check MESSAGE_ID --format toon` if the preview lacks
decision evidence. An empty outcome requires the full check. Apply the evidence
rules in [Completion](completion.md).
Stale results have no actionable body. Leave unfamiliar messages pending and
report them. Resolve accepted bound replies through the completion procedure
below. Acknowledge other handled messages with `tincan inbox ack concierge
MESSAGE_ID`.
## Open operator actions

Record each request before asking the operator for an approval, a command,
or a decision. Give the request a stable ID and a short, explicit description.
Resolve the action only after the operator answers or the request is withdrawn.
Keep an action open while a worker waits for the answer.

```sh
concierge operator ask stop-worker "Approve stopping the accepted worker"
concierge operator list
concierge operator resolve stop-worker
```

End every reply with a short **Waiting on you** list while operator actions
remain open. Repeat the unresolved requests in that list. A permission failure
that needs an operator command or approval also requires an action.
`concierge brief` and `concierge task pending` include all open actions.
Pending completions include accepted workers awaiting stop and cleanup.
Verify their current pane content and status before stopping them.
