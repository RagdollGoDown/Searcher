package main

import (
	"context"
	"flag"
	"log"
	"net/http"

	"github.com/RagdollGoDown/Searcher/llm"
	"github.com/RagdollGoDown/Searcher/scraper"
	"github.com/RagdollGoDown/Searcher/source"
	"github.com/RagdollGoDown/Searcher/store"
	"github.com/RagdollGoDown/Searcher/web"
)

func main() {
	addr := flag.String("addr", "localhost:8080", "HTTP listen address")
	chatModel := flag.String("chat-model", "phi3:mini", "Ollama model used for chat")
	embedModel := flag.String("embed-model", "nomic-embed-text", "Ollama model used for embeddings")
	dbPath := flag.String("db", "./data/searcher.db", "SQLite database holding documents, conversations, and settings")
	flag.Parse()

	ctx := context.Background()

	client := llm.NewOllamaClient(*chatModel)
	if err := client.Init(ctx); err != nil {
		log.Fatalf("initializing LLM client: %s", err)
	}

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
		"Local Files":  source.NewFilesSource(20000, 60000),
		"My Documents": ragSource,
		// The scraper deliberately has no entry here: it's not a Source
		// itself (no Fetch/Icon) — crawled pages are ingested into the
		// same store "My Documents" already searches, so that source
		// covers scraped content too, with no separate retrieval path.
	}

	server := web.NewServer(client, sources, ragSource, crawler, db)
	handler, err := server.Handler()
	if err != nil {
		log.Fatalf("setting up web server: %s", err)
	}

	log.Printf("listening on http://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, handler))
}
