# Research Feature — Implementation Plan

## Goal

Add a research/report-generation feature to Searcher: given a question, iteratively
search across the existing Sources (Wikipedia, "My Documents" RAG store, scraped
sites) across multiple rounds, then produce a readable, cited report with inline
citation markers and short section-level takeaways. One feature among several
within Searcher — not a new standalone project — reusing the existing RAG store,
scraper, and job-status patterns already in the codebase.

## Status

Phase 0 decisions locked (2026-09-13). Phase 1 items 1-3 done (2026-09-13):
`research_jobs`/`research_citations` schema + `store/research_jobs.go` CRUD.
Phase 2 items 4-5 done (2026-09-14): `llm/anthropic.go` (`AnthropicClient`,
raw `net/http` against the Messages API — the official SDK was tried
first but dropped after measuring +6.2MB/+36% binary size from transitive
deps this app never uses; matches `ollama.go`'s existing raw-HTTP style) +
`research/settings.go` (default backend/model settings on the new
`Researcher` struct, `NewClient` factory choosing Ollama vs. Anthropic). Phase 3 item 7 done (2026-09-14): new
`research/round.go` — `ParseRoundReply`, `DedupeQueries`,
`BuildRoundPrompt`, per 0.2's protocol. Phase 3 items 8-13 (condensed
history builder, per-round fetch, loop control, synthesis, job wiring,
startup recovery) next.

## Phase 0 — Decisions (locked)

### 0.1 — Citation storage

A separate `research_citations` table (not a JSON blob column), following the
existing pattern of `chunks`/`documents` being split tables: id, job_id, citation
number, source name, title, extract, url. Keeps citations independently queryable
later (e.g. "which reports cited this URL").

Citations are deduped per job by URL (or by source name + title when URL is
empty, e.g. a Wikipedia result with no stable link): if two different `SEARCH`
queries in the same job surface the same result, it's cited once under one
number, not twice under two. Implemented in `store.AppendCitation`.

### 0.2 — Round protocol

No native tool-calling. The iterative search loop uses a plain-text convention
parsed by the loop's own Go code, since it must work uniformly across every
`llm.Client` backend — including `phi3:mini` via Ollama, which doesn't support
reliable tool-calling.

Each round's prompt (shape, not final wording):

```
Original question: <question>
Found so far: <condensed list of extract titles + one-line gist, or "nothing yet">
If you need more information, reply with one search per line, each starting
with "SEARCH: ". Otherwise reply with exactly "DONE".
```

Parsing rules (enforced by the loop's Go code, not the model):

- A line starting with `SEARCH:` (case-insensitive) → extract the remainder as a
  query, queue it for fetching.
- Reply is exactly `DONE` (case-insensitive) with no `SEARCH` lines → stop the
  loop, proceed to final synthesis.
- Malformed reply (neither valid `SEARCH` lines nor `DONE`) → treat as `DONE`.
  Fail toward stopping, not toward an infinite loop.
- Both `SEARCH` and `DONE` present in one reply → `SEARCH` wins (run one more
  round).
- Duplicate query (case-insensitive exact match against any query already issued
  this job) → dedupe, don't re-fetch.
- Hard caps regardless of model output: max rounds and max total queries per job,
  enforced by code. This is the real safety net for weak local models that may
  never reliably converge to `DONE` on their own.

Future consideration (also noted in `CLAUDE.md`): once `llm/anthropic.go` exists,
consider giving that backend real native tool-calling for the search step instead
of this text convention — likely more reliable for that provider, at the cost of
a backend-specific code path instead of one uniform loop.

### 0.3 — Cloud provider client

Add `llm/anthropic.go` implementing the existing `llm.Client` interface (`Init`,
`Chat`) — a dedicated Anthropic-specific client, not a generic HTTP-any-provider
abstraction. Sits alongside `llm/ollama.go`, giving Searcher an interchangeable
cloud backend option.

## Product decisions (locked earlier in design discussion)

- Output lives in a **new dedicated report view** (e.g. `web/static/research.html`),
  not squeezed into the chat UI — parallel to `rag.html`/`scraper.html`.
- **Dual backend is a hard requirement** — local (Ollama) and cloud (Anthropic)
  must both work, matching the existing provider-agnostic `llm.Client` design.
- This is **one feature among several** within Searcher, reusing the RAG store,
  scraper, and the `crawl_jobs` job-status pattern rather than inventing new
  architecture.

## Phase 1 — Data layer (depends on 0.1)

1. Add `research_jobs` table + schema migration in `store/store.go` (mirrors
   `crawl_jobs`): id, query, status, round count, sources selected, backend/model
   used, created/updated, final report markdown.
2. Add `research_citations` table (per 0.1), FK to job id.
3. New `store/research_jobs.go`: CRUD — Create, Get, List, UpdateStatus,
   AppendCitation, SaveReport — mirrors `store/crawl_jobs.go`.

## Phase 2 — Dual LLM backend (independent of Phase 1 — safe to build in parallel)

4. `llm/anthropic.go` — new `llm.Client` implementation for Claude, alongside
   `llm/ollama.go`.
5. Backend/model selection wired into settings, extending the existing generic
   KV store in `store/settings.go`. Depends on 4.
6. *(Stretch, non-blocking)* Streaming support on `Client.Chat` for live
   progress — current interface returns one string; skip for v1.

## Phase 3 — Research loop core (depends on Phase 1 items 1-3; needs a working
`llm.Client` — can start against the existing `OllamaClient` before Phase 2
finishes, then swap)

7. Round query-generation prompt + parser, per 0.2.
8. Condensed running-history builder — what gets fed back each round (titles/
   summaries, not full raw extract dumps) so context doesn't blow up on later
   rounds.
9. Per-round fetch + dedupe against selected `source.Source`s, assigning
   citation IDs. Depends on Phase 1 item 2.
10. Loop control: max rounds/extracts, cancellation — mirrors
    `Crawler.CancelCrawl` in `scraper/crawler.go`. Depends on item 3.
11. Final synthesis call: full extract pool → report with inline `[n]`
    citation markers + per-section takeaway callouts ("tips"). Depends on 7-10.
12. Wire the loop as a background job on creation, persisting status at each
    step — mirrors `Crawler.StartCrawl`. Depends on 7-11.
13. Startup recovery for interrupted research jobs — mirrors
    `RecoverInterruptedCrawlJobs`. Depends on 12.

## Phase 4 — API endpoints (depends on Phase 1 + Phase 3's job functions;
handlers can be stubbed earlier against interfaces)

14. `POST /api/research/jobs` — start a job.
15. `GET /api/research/jobs` — list/history.
16. `GET /api/research/jobs/{id}` — status/progress polling.
17. Report + citations retrieval once done (may reuse endpoint 16 once status
    is `done`).
18. `DELETE /api/research/jobs/{id}` — cancel/delete, mirrors
    `handleCancelOrDeleteCrawlJob` in `web/web.go`.
19. `GET`/`PUT /api/research/settings` — default backend/model, mirrors
    `handleGetRAGSettings`/`handlePutRAGSettings` and
    `handleGetScraperSettings`/`handlePutScraperSettings`. Backs
    `Researcher.GetSettings`/`SetSettings` (`research/settings.go`, done in
    Phase 2 item 5) — added here since it was missed when Phase 4 was
    first drafted, even though RAG and the scraper both got one.
20. Wire routes into `Server.Handler()`.

## Phase 5 — Frontend (depends on Phase 4)

20. New `web/static/research.html`: start-job form (question, source
    checkboxes, backend/model picker), job history list, report display area.
21. Polling logic for in-progress jobs (same pattern as the scraper page).
22. Markdown → HTML report rendering with clickable/hoverable citation
    markers — extends `snarkdown.js`.
23. Citation panel/footnotes linked from markers.
24. Callout styling for the section takeaways ("tips") from item 11.
25. Export-to-file button (Markdown/HTML).

## Phase 6 — Quality (depends on Phase 3; runs alongside Phase 4/5)

26. Unit tests for the round parser (malformed lines, empty response,
    premature `DONE`).
27. A quality check in the spirit of `source/rag_quality_test.go` — fixed test
    questions, verify claims trace back to real citations.
28. Manual side-by-side run: same questions on `phi3:mini` vs. the Anthropic
    backend — validates whether the two produce meaningfully different reports.

## Parallelization note

Most of this is one dependency chain through a handful of shared files
(`store.go`'s schema, `web.go`'s handlers, one loop package) — not many
independent chunks. Building it as one continuous sequential effort is expected
to be faster and lower-risk than splitting it across parallel agents/sessions,
which would mostly create same-file merge conflicts on things like the schema
and the HTTP handlers. The one genuinely independent piece is Phase 2 item 4
(the Anthropic client) — safe to build in parallel since it only depends on the
existing `llm.Client` interface.

## Critical path

0 → 1 → 3 → 4 → 5, with Phase 2 and most of Phase 6 running alongside rather
than gating anything. Fastest route to a working prototype: Phase 0 + minimal
Phase 1 + Phase 3 against the existing Ollama client, deferring Phase 2 and
Phase 5 polish until the loop itself is proven out.
