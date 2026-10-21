# Offline session usage

`concierge-usage` measures saved Codex rollouts or Claude transcripts. It opens
JSONL files for reading. It does not call either harness, Concierge, a host
service, or the network. The Nix Concierge package includes the command.

```sh
concierge-usage --harness codex --project /path/to/project 6h
concierge-usage --harness claude --sessions /saved/transcripts \
  --from 2026-10-20T15:05:50-07:00 --to 2026-10-20T21:05:50-07:00 --format json
```

The default format is a compact TOON report. Use `--full` for per-session
details and metric limits. JSON always includes those details. Both outputs
separate primary sessions from child sessions. A successful empty window has
zero totals and empty session arrays.

`--sessions` defaults to `~/.codex/sessions` for Codex. Claude defaults to
`~/.claude/projects/<encoded-project>` when `--project` is supplied. Claude
requires either `--project` or `--sessions`. The project filter matches the
session's initial working directory exactly after path cleaning. Relative
project paths become absolute. A parentless worker cannot be distinguished from
a supervisor by its metadata; inspect the reported session IDs.

Specify both `--from` and `--to` as RFC 3339 timestamps, or a trailing Go
duration such as `6h` or `90m`. A duration ends at `--to`, or at the current
time when `--to` is absent. It cannot be combined with `--from`. Both endpoints
are inclusive. Explicit timestamps make comparisons repeatable. Use an explicit
UTC offset for local windows across daylight saving changes.

## Metrics and limits

| Metric | Definition and limit |
| --- | --- |
| Requests and tokens | Codex sums `token_usage_record.payload.usage`, once per response ID within a session. Cumulative `thread_token_usage` and repeated `token_count` events do not add tokens. Claude counts each assistant message ID once and uses its latest saved usage snapshot within the window. A duplicate whose first observation precedes the window does not add tokens. Missing IDs or usage cannot establish a request. |
| Input | Codex records input directly. Claude input sums `input_tokens`, `cache_creation_input_tokens`, and `cache_read_input_tokens`. |
| Cached and uncached input | Cached input uses Codex `cached_input_tokens` or Claude `cache_read_input_tokens`. Uncached input equals total input minus cached input. Claude cache creation counts as uncached input. |
| Output and reasoning | Reasoning is a subset of output. Never add it to output. Claude reasoning uses `output_tokens_details.thinking_tokens` when recorded. A Claude zero can mean the field is absent. |
| Turns | Codex counts turns started in the window plus earlier turns with usage in the window. Claude starts a turn at a non-meta user message containing no tool results. Zero-request turns count. Missing turn IDs are grouped under an unknown turn. |
| Wake turns | The first user prompt must start with `Concierge supervisor event `. Codex also accepts an explicit `wake` or `heartbeat` trigger. Later user steering remains part of the same Codex turn. Wake usage covers requests within the window, including turns started earlier. |
| Per wake turn | Requests, input, output, and total tokens divided by all wake turns, including zero-request wakes. Total tokens equal input plus output. Zero wake turns produce zero averages. |
| Tool output | For `heartbeat claim`, `task reply check`, and `task report`, count saved call batches and returned UTF-8 text bytes. Results include recorded wrappers. All output from a matching batch counts, including output from other commands in that batch. Categories can overlap. A batch with two claims counts as one call. Calls and results use their own timestamps; a result can fall in the window after its call. Missing results contribute no bytes. |
| Skill reads | Count call records that read `concierge-local-supervisor/SKILL.md` through Read, cat, sed, head, or read_file. This is a transcript estimate of reads, not a filesystem trace. A matching batch counts once. Each session includes an array of counts separated by observed compactions. The first and last intervals can be partial. |
| Compactions | Count Codex `compacted` records or Claude `system/compact_boundary` records in the window. |
| Escalations | Codex counts literal `sandbox_permissions: require_escalated` argument values, including JavaScript call input. This counts requests for escalation, without determining their approval. Claude has no direct equivalent; its field is null. |
| Nonzero commands | Codex decodes result JSON and counts nonzero `exit_code` fields, including nested text blocks. Native `Process exited with code N` envelopes also count. A result without a recorded exit code is unmeasured. Claude has no directly comparable field; its field is null. |
| Tool errors | Claude counts `tool_result` records with `is_error: true`. This includes non-command tools and is separate from nonzero commands. Codex has no field in this report; its value is null. |
| Pending replies at turn end | Reported as `not derivable`. Saved commands and their outputs do not establish a complete, authoritative pending-reply timeline. |
| Weekly quota | First and last saved percentage observations for a 10,080-minute limit in primary sessions. This is a percentage of a weekly limit, not a token balance. Other sessions can contribute. Null means absent. Claude quota is unavailable unless a supported explicit record exists; this reader currently has none. |
| Skipped records | Count unknown or invalid relevant records within the window. Unknown records with invalid/missing timestamps and malformed JSON count separately as `unattributed_records`, across all scanned matching sessions. Files without identifiable metadata count as `skipped_files`. Known bookkeeping records do not add to skipped counts. |

The reader streams one line at a time without a fixed scanner line limit. It
retains request IDs, turn summaries, and call associations for deduplication and
attribution. Memory therefore grows with the number of requests and calls,
rather than the total transcript text. Files are visited in path order. Avoid
directories containing conflicting revisions of the same saved transcript.
Unknown record types cannot break a report. A filesystem read error fails the
command with a structured error. Exit codes are 0 for success, 1 for read/write
errors, and 2 for invalid arguments.

The synthetic fixtures under `pkg/sessionusage/testdata` contain no operator
text. Tests cover expected totals, deduplication, wake attribution, child
exclusion, exact window boundaries, cache accounting, compaction intervals,
quota observations, large records, unknown records, and decoded command exits.

See [the fixed Codex baseline](session-usage-baseline.md) for a reproducible
comparison against the earlier audit. Effectiveness and subscription charging
remain separate measurements.
