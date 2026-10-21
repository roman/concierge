# Worker reply attachments

Publish a result body and named files with one reply:

```sh
concierge task reply send TASK_ID --generation "$CONCIERGE_SPAWN_GENERATION" \
  --key result --assignment-id MESSAGE_ID --body-file result.md \
  --attach diagram.png=./output/diagram.png --attach measurements.csv=/tmp/results.csv
```

Repeat `--attach NAME=PATH` for each file. The first equals sign separates the
logical filename from the source path. Commas and later equals signs in paths
remain literal. Paths are explicit local inputs. Absolute and relative source
paths are allowed. The source must be a readable regular file. A final symlink,
directory, FIFO, socket, or stdin marker is refused. Parent directories follow
normal local filesystem resolution.

The limits are eight attachments, 262,144 bytes per file, and 524,288 bytes across
all files. Empty files are supported. A reply containing attachments must fit
within 2,097,152 serialized JSON bytes, including its Markdown body and base64
data. The existing remote body limit of 1 MiB and transport/outbox limits of
2 MiB also apply. JSON escaping can exhaust a serialized limit before a raw-byte
limit. Existing attachment-free local replies retain their previous behavior.

Names have 1–128 ASCII characters. The first character must be a letter or digit.
Later characters can also include `.`, `_`, and `-`. A trailing dot is refused.
Names cannot contain directory separators. Names must be unique after ASCII
case folding. Source basenames are never used as implicit destination paths.

Concierge computes a SHA-256 checksum and size from the bytes it reads. The host
validates the complete attachment set before publishing a reply. Invalid names,
sizes, checksums, or limits reject the entire reply. The assignment remains
pending. Attachment order does not affect an identical retry. Changed bytes,
names, or metadata conflict with an existing reply under the same assignment or
unbound reply key.

## Persistence and remote transport

Attachments travel as bytes inside the reply envelope. The host never opens a
worker-supplied source path. Remote workers read their files locally and send the
bytes through the existing authenticated worker socket. The existing task,
generation, grant, recipient, and assignment checks remain in force.

The remote outbox retains the complete attachment set before the first network
request. An offline or revoked worker keeps that record for exact recovery.
Trusted `task reply reconcile --file RECORD` preserves the original attachment
bytes, generation, and assignment. It does not restore worker access.

The saved report contains the same envelope as the primary reply. Publication
and report archival precede assignment acknowledgement. An interrupted archive
can be repaired by an identical retry. A reply with attachments is archived even
when it is unbound. Removing a worker checkout or worker mailbox does not remove
the archived bytes. Existing text-only replies remain readable without migration.

## Reading and exporting

`task reply check REPLY_ID` and `task report TASK_ID REPLY_ID` list attachment
names, raw sizes, and SHA-256 checksums in their TOON and concise output. A stale
reply check withholds attachment content. `--format body` continues to write the
exact Markdown body alone.

Export one attachment to a new, explicitly selected file:

```sh
concierge task report TASK_ID REPLY_ID \
  --attachment diagram.png --output ./saved-diagram.png
```

The export validates the entire set before writing. It writes and syncs a private
temporary file, then publishes the complete bytes with an exclusive hard link.
An existing file, directory, or symlink at the output path causes failure. The
filesystem must support hard links. Temporary files are removed after success
or failure. Export does not create missing parent directories.

## Report interface for external consumers

Read a validated saved report as JSON:

```sh
concierge task report TASK_ID REPLY_ID --format json
```

The output is one `task-reply` object followed by a newline. Its existing fields
are `kind`, `state`, `task`, `spawn_generation`, optional `assignment_id`, optional
`reply_key`, and `body`. The optional `attachments` array contains:

| Field | Meaning |
| --- | --- |
| `name` | Validated logical filename. |
| `size` | Raw byte count, expressed as a JSON integer. |
| `sha256` | Lowercase hexadecimal SHA-256 of the raw bytes. |
| `data` | Standard base64 with padding. An empty file uses `""`. |

An absent attachment array means no attachments. The report carries no source
paths or extraction targets. Consumers should ignore additional metadata fields,
verify each size and checksum, and select their own safe destination. Report
JSON describes historical evidence. It does not indicate current-generation
authority or operator acceptance. The caller already has the reply ID used to
select the report.

External consumers own their import schemas, storage policies, and authorization.
