package taskops

const workerReplyInstructions = "When the project records notes in a vault, finish your reply with the following sections. Never write into a vault yourself.\n" +
	"Use exact, case-sensitive headings. Include each section at most once. Omit optional sections when there is nothing to record.\n" +
	"Use `## Handoff` with `### Outcome`, `### Current state`, and `### Next actions`. Give nonempty Markdown text for each field. Report unfinished work as pending.\n" +
	"Optional `## Gaps`: use one `### <title>` per gap with `#### Gap` and `#### Why it matters`.\n" +
	"Optional `## Learnings`: use one `### <name>` per learning with `#### Description` and `#### Type`. Type is technical, project, or workflow. An optional `#### Body` adds Markdown text.\n" +
	"Optional `## Artifacts`: use one named entry per artifact with the same description and body field headings, plus `#### Reusable for`. Reusable for contains a JSON array of nonempty strings, or []. Body contains exactly one fenced code block; use a fence longer than any matching fence inside it.\n" +
	"Keep all required fields nonempty. Do not include frontmatter, note paths, or lifecycle transitions. Do not put structural headings inside field text; protect code with fences.\n"
