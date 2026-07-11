---
name: dbrain-review
description: Review recent activity in the user's local dbrain memory and produce a source-linked briefing. Use when the user asks what happened recently, wants a weekly/daily/local-brain digest, wants important politics/AI/other themes from their brain, asks for reviewable headlines with links, or passes a specific focus such as "AI", "Canada politics", "Alberta", "security", or another topic.
---

# dbrain Review

## Overview

Create a concise briefing from recent dbrain evidence. Default to the last 7 days when the user does not specify an interval, and use any requested focus as a prioritization lens rather than as permission to invent outside context.

## Inputs

- **Interval:** Accept relative durations such as `24h`, `3d`, `7d`, `14d`, `last week`, `today`, or explicit RFC3339 timestamps. If absent, use `7d`.
- **Focus:** Accept any user-provided topic, entity, beat, or source family. Examples: `AI`, `politics`, `Canada`, `Alberta separatism`, `agent infrastructure`, `security`, `energy`, `anything surprising`.
- **Format:** If absent, produce a reviewable Markdown briefing with headlines, short summaries, implications, source keys, and external links.

## Required Tooling

Use the read-only dbrain MCP tools through the `dbrain-mcp` skill. Do not mutate dbrain state.

If MCP tools are unavailable, follow the fallback in `dbrain-mcp`; do not silently switch to web search. Browse the web only when the user explicitly asks for external verification beyond the saved corpus.

## Workflow

1. Resolve the interval and focus.
   - If no interval is given, set `since: "7d"`.
   - Convert `last week` to `7d` unless the user clearly means a calendar week.
   - Keep the focus string verbatim for prioritization and query-windowing.

2. Review recent entities.
   - Call `dbrain_whats_new` with `view: "entities"`, `types: ["all"]`, the resolved `since`, and a large enough limit, usually `200` to `300`.
   - If `truncated` is true, continue with `cursor` until the interval is covered or the evidence budget is clearly sufficient.
   - Merge pages by stable entity/source key. Prefer rows with stronger summaries, higher importance/actionability, and later events.

3. Cluster and rank.
   - Build topic clusters from titles, summaries, tags, domains, source types, and repeated entities.
   - If a focus was provided, prioritize clusters matching the focus, but still include a short "Other notable" section when there are strong unrelated signals.
   - Separate supported facts, source claims, opinions, and weak/blocked-source signals.

4. Fetch details before writing.
   - Select the top evidence rows for each cluster and call `dbrain_get_many` with `content_mode: "evidence"`.
   - Pass the focus plus cluster keywords as `query` so long raw extracts, OCR, and transcripts are windowed around useful text.
   - Use `content_mode: "brief"` only for secondary rows where title, URL, tags, and metadata are enough.
   - For source claims from media, prefer raw `x_media_transcript` or `ocr_text` over derived summaries when available.

5. Produce the briefing.
   - Start with the interval and a confidence line.
   - Use topic sections such as `Politics`, `AI And Agents`, `Security`, `Economy`, `Other Notable`, or focus-specific headings.
   - Under each section, write headline bullets. Each bullet should include:
     - a concise headline
     - a 1-3 sentence summary
     - an implication or upcoming event/date when present in the evidence
     - external links from canonical URLs, plus source keys such as `[x:...]`, `[src:...]`, or `[gh-star:...]`
   - Prefer linking the source title or publisher name. Do not link local note paths unless there is no external URL and the user needs the local artifact.

## Output Defaults

Use this format unless the user asks for another shape:

```markdown
**dbrain Review**

Window: <interval or date range>. Focus: <focus or "general">.
Confidence: <high/moderate/low> for corpus themes; <confidence note for contested claims>.

**<Topic>**

- **<Headline>.** <Summary and implication.> Sources: [<title or site>](<external-url>). Keys: [src:...], [x:...].
```

For a short executive digest, use 1-2 paragraphs with inline source keys and links. For a reviewable version, use sectioned bullets.

## Quality Rules

- Answer from saved dbrain evidence, not general model memory.
- Include external URLs when present in MCP payloads.
- Preserve exact dates, bill numbers, source keys, and names.
- Flag blocked, empty, or extract-failed sources instead of treating them as confirmed facts.
- Do not overstate X posts, OCR, transcripts, or opinion pieces as verified external reality.
- If the user asks for upcoming implications, report only upcoming dates/events present in the evidence or clearly mark analysis as inference.
- If the user wants periodic delivery, say this skill is suitable for an automation and that the automation should invoke it with an interval and optional focus.

## Example Invocations

- `Use $dbrain-review for the last week.`
- `Use $dbrain-review with focus on AI agents and MCP.`
- `Use $dbrain-review for the last 24h, focus on Canada politics, with links.`
- `Use $dbrain-review every Monday morning for politics, AI, and anything surprising.`

## Recurring Use

When invoked by an automation, default to:

- interval: `7d`
- focus: `general`, unless the automation prompt supplies one
- format: reviewable Markdown briefing
- evidence policy: fetch details for top rows before final synthesis
