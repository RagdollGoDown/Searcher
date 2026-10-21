# Searcher

A lightweight, self-hostable LLM chat app with tool use (Wikipedia search, RAG over
uploaded documents and crawled sites), targeting small consumer hardware. Written in
Go, backed by a local Ollama server (`phi3:mini` for chat, `nomic-embed-text` for
embeddings). Serves `localhost:8080`.

## Packages

- `llm/` — provider-agnostic `Client` (chat) and `Embedder` (embeddings) interfaces, with Ollama and Anthropic implementations.
- `source/` — pluggable tools and data sources behind the `Source` interface (`Fetch` + `Icon`): Wikipedia, browser-uploaded attachments, and RAG over the document store.
- `store/` — the single SQLite file holding everything persistent: documents, chunk embeddings, conversations, background job state, and key-value settings.
- `scraper/` — crawls a site from a root URL and ingests pages into the RAG store, so crawled content is searchable through the existing document source with no separate retrieval path.
- `extract/` — decodes non-text formats (currently PDF) to plain text.
- `query/` — the `Query` type passed to sources, including file attachments.
- `web/` — HTTP server plus the chat, document, scraper, and settings UIs.
- `main.go` — wires the above together.

Packages carry doc comments covering their own internals and file layout. Read those
instead of duplicating them here.

## Design decisions

- **Small hardware is the binding constraint.** No CGO, no Docker, no server-grade
  dependencies. That is why `store/` uses pure-Go SQLite (`modernc.org/sqlite`) and
  brute-force cosine similarity rather than Postgres+pgvector, `sqlite-vec`, or an ANN
  index. Raise the tradeoff before reaching for anything heavier.
- **Keep the LLM backend swappable.** Model-specific behavior belongs behind
  `llm.Client`/`llm.Embedder`.
- **Don't generalize a source capability until a second source needs it.** `RAGSource`
  has document-management and settings methods that `web` calls directly rather than
  through `Source`. One example isn't enough to know the right shape for a general
  interface.
- **`store/`'s `settings` table is a generic key-value store**, not RAG-specific. Other
  sources and UI preferences are meant to share it.
- **Conversation messages are one row per turn, not a JSON blob per conversation.** A
  turn is durable the moment it's saved, and messages can later grow an `embedding`
  column the way chunks did.
- **Settings changes apply prospectively.** Changing chunk size or overlap never
  rechunks or re-embeds already-ingested documents.
- **`Icon()` output is rendered unsanitized.** Trusted developer-authored SVG only,
  never anything derived from user or source input.

## Code style

Simplicity is a requirement here, not a preference. This is a small app on small
hardware and should stay readable by one person holding all of it in their head.

- **Comments explain why, not what.** The existing ones do this well. The note on
  `Source.Icon`, the one on `FilesSource`'s truncation limits, and the one in `main.go`
  explaining why the scraper is deliberately absent from the sources map are the model
  to follow. Each records a decision the code can't show on its own.
- **Don't narrate the code.** No `// increment counter` above `counter++`, no
  `// Step 1:` headers chopping up a short function, no doc comment that just restates
  a field's name. Go's convention of opening a doc comment with the identifier is fine;
  padding it to a sentence that adds nothing is not.
- **Don't write comments about the edit.** Nothing saying "now handles X", "updated to
  use Y", or "this used to do Z". Comments describe the code as it stands. Git holds
  the history.

## Known limitations

- `extract.PDFText` doesn't reliably resolve ligature glyphs (`fi`, `fl`, `ff`) through
  the font's `ToUnicode` CMap, so PDFs that use them heavily come out garbled. Affects
  every caller, since they share one extraction path.

## Future work

- Research/report generation is mid-build on the `research` branch, kept off `main`
  until it works end to end. `RESEARCH_PLAN.md` there has the task breakdown and
  locked design decisions.
