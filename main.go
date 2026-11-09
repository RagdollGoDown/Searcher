package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"

	"github.com/RagdollGoDown/Searcher/llm"
	"github.com/RagdollGoDown/Searcher/scraper"
	"github.com/RagdollGoDown/Searcher/source"
	"github.com/RagdollGoDown/Searcher/store"
	"github.com/RagdollGoDown/Searcher/web"
)

// version is set by the release workflow via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	addr := flag.String("addr", "localhost:8080", "HTTP listen address")
	provider := flag.String("provider", "ollama", `chat backend used until one is picked on the settings page: "ollama" or "anthropic"`)
	ollamaModel := flag.String("ollama-model", "phi3:mini", "Ollama model used for chat")
	anthropicModel := flag.String("anthropic-model", "claude-sonnet-5-5", "Anthropic model used for chat (needs ANTHROPIC_API_KEY)")
	embedModel := flag.String("embed-model", "nomic-embed-text", "Ollama model used for embeddings")
	dbPath := flag.String("db", "./data/searcher.db", "SQLite database holding documents, conversations, and settings")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return
	}

	ctx := context.Background()

	// Every provider is registered even if it fails to initialize, so the
	// settings page can show it disabled along with the reason (e.g. no
	// API key) instead of it silently not existing.
	providers := []web.Provider{
		{Name: "ollama", Model: *ollamaModel, Client: llm.NewOllamaClient(*ollamaModel)},
		{Name: "anthropic", Model: *anthropicModel, Client: llm.NewAnthropicClient(*anthropicModel)},
	}
	defaultReason := "unknown provider"
	for i, p := range providers {
		err := p.Client.Init(ctx)
		if err != nil {
			providers[i].Unavailable = err.Error()
		}
		if p.Name == *provider {
			defaultReason = providers[i].Unavailable
		}
	}
	if defaultReason != "" {
		log.Fatalf("-provider %q: %s", *provider, defaultReason)
	}

	// Embeddings stay on Ollama regardless of -provider: Anthropic has no
	// embeddings API, and keeping one embedder means switching chat
	// providers never invalidates already-stored chunk vectors.
	embedder := llm.NewOllamaEmbedder(*embedModel)
	if err := embedder.Init(ctx); err != nil {
		log.Fatalf("initializing embedding client: %s", err)
	}

	db, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("opening database: %s", err)
	}
	defer db.Close()

	ragSource := source.NewRAGSource(db, embedder, 2000, 8000)
	crawler := scraper.NewCrawler(db, ragSource)

	if n, err := db.RecoverInterruptedCrawlJobs(ctx); err != nil {
		log.Printf("recovering interrupted crawl jobs: %s", err)
	} else if n > 0 {
		log.Printf("marked %d interrupted crawl job(s) failed after restart", n)
	}

	sources := map[string]source.Source{
		"Wikipedia":    source.NewWikipediaSource("en", 5),
		"My Documents": ragSource,
		// The scraper deliberately has no entry here: it's not a Source
		// itself (no Fetch/Icon) — crawled pages are ingested into the
		// same store "My Documents" already searches, so that source
		// covers scraped content too, with no separate retrieval path.
	}

	server := web.NewServer(providers, *provider, sources, ragSource, crawler, db)
	handler, err := server.Handler()
	if err != nil {
		log.Fatalf("setting up web server: %s", err)
	}

	log.Printf("listening on http://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, handler))
}
