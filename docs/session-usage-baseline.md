# Codex Concierge baseline

The offline reader matches the earlier audit for the fixed window
**2026-10-20 15:05:50–21:05:50 Pacific**. All five acceptance values match:
141,605,078 input, 139,773,312 cached input, 409,250 output, 204,234 reasoning,
and 777 model requests. Uncached input is 1,831,766 tokens.

Run from the Concierge repository:

```sh
direnv exec . go run ./cmd/concierge-usage \
  --harness codex \
  --project /home/roman/Projects/self/exmachina \
  --from 2026-10-20T15:05:50-07:00 \
  --to 2026-10-20T21:05:50-07:00 \
  --format json
```

The installed command accepts the same arguments. The saved inputs are under
`~/.codex/sessions`. Source files remain unchanged.

| Scope | Requests | Input | Cached input | Uncached input | Output | Reasoning subset |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Primary total | 777 | 141,605,078 | 139,773,312 | 1,831,766 | 409,250 | 204,234 |
| Child total | 494 | 55,505,973 | 52,986,240 | 2,519,733 | 45,194 | 13,467 |
| Primary `01a15b4e-127c-71d1-bace-d0a6c3d5a243` | 171 | 34,212,891 | 33,942,528 | 270,363 | 155,596 | 111,842 |
| Primary `01a15be3-5dbc-73f3-ba17-cda1f92882ea` | 606 | 107,392,187 | 105,830,784 | 1,561,403 | 253,654 | 92,392 |

## Primary activity

| Metric | Value |
| --- | ---: |
| Turns | 65 |
| Wake turns | 34 |
| Wake requests | 240 |
| Wake input | 44,269,780 |
| Wake cached input | 43,445,888 |
| Wake uncached input | 823,892 |
| Wake output | 109,044 |
| Wake reasoning subset | 36,897 |
| Requests per wake turn | 7.059 |
| Input per wake turn | 1,302,052.353 |
| Output per wake turn | 3,207.176 |
| Total tokens per wake turn | 1,305,259.529 |
| Supervisor skill reads | 8 |
| Compactions | 2 |
| Escalation arguments | 478 |
| Decoded nonzero command results | 32 |
| Skipped records | 12 |
| Duplicate usage records | 0 |
| Unattributed records | 0 |

The earlier audit counts 64 primary turns **with usage**. This reader also
counts one zero-request wake turn. Both methods count 34 wake turns. Eight
child sessions have usage. A ninth child has an in-window record without usage
and appears with zero totals.

The first primary session has one skill read and no compactions. The second
session has seven reads across three intervals: `[4, 1, 2]`. The two compactions
separate those intervals. Four reads repeat an earlier read in their interval.

| Operation | Call batches | Results | Returned bytes | Bytes per call |
| --- | ---: | ---: | ---: | ---: |
| `heartbeat claim` | 39 | 39 | 94,772 | 2,430.051 |
| `task reply check` | 40 | 40 | 373,472 | 9,336.800 |
| `task report` | 21 | 21 | 77,038 | 3,668.476 |

Returned bytes measure UTF-8, including saved tool wrappers and other output in
the same batch. The earlier audit's character figures use a different unit.
The two figures cannot be compared directly for non-ASCII text.

The first weekly quota observation is **28% used** at
`2026-10-20T22:05:50.678Z`. The last observation is **53% used** at
`2026-10-21T04:00:22.704Z`. These are percentages of a weekly limit. They do
not establish a token balance or a subscription charging formula.

Pending replies at turn end remain **not derivable** from this transcript.
See [metric definitions and limits](session-usage.md) before comparing runs.
