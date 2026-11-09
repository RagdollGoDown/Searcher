# Searcher

A lightweight, self-hostable LLM chat app with tool use, built to run on small
consumer hardware. It's a single Go binary with no CGO, backed by a local
[Ollama](https://ollama.com) server and one SQLite file.

## Features

- **Chat** with a local model (`phi3:mini` by default), or with Claude through the
  Anthropic API. Pick the provider on the settings page.
- **Wikipedia search**: the model can look things up on Wikipedia while answering.

- **File attachments**: attach text, code, or PDF files to a chat message.
- **My Documents (RAG)**: upload documents once and they are chunked, embedded
  (`nomic-embed-text`), and searched by similarity in later chats.
- **Site crawler**: crawl a website from a root URL and add its pages to My Documents.
- **Persistent conversations** and settings.

## Getting started

### Locally

Requirements: Go 1.25+ and a running Ollama server.

```sh
ollama pull phi3:mini
ollama pull nomic-embed-text
go run .
```

Then open <http://localhost:8080>.

Useful flags (see `go run . -h` for all of them):

| Flag | Default | |
|---|---|---|
| `-addr` | `localhost:8080` | HTTP listen address |
| `-provider` | `ollama` | Default chat backend: `ollama` or `anthropic` |
| `-ollama-model` | `phi3:mini` | Ollama chat model |
| `-anthropic-model` | `claude-sonnet-5-5` | Anthropic chat model |
| `-embed-model` | `nomic-embed-text` | Ollama embedding model |
| `-db` | `./data/searcher.db` | SQLite database path |

Environment variables:

- `OLLAMA_HOST`: Ollama server address (default `http://localhost:11434`).
- `ANTHROPIC_API_KEY`: enables the Anthropic provider.

Embeddings always go through Ollama, even when chatting with Anthropic.

### With Docker Compose

```sh
cp .env.example .env   # set DOMAIN
docker compose up -d
```

This starts Searcher, Ollama (pulling both models), and Caddy for TLS and rate
limiting. See [DEPLOYMENT.md](DEPLOYMENT.md) for details. The app has no
authentication, so don't expose it publicly as-is.
