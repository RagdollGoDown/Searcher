package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"searcher/llm"
	"searcher/scraper"
	"searcher/source"
	"searcher/store"
	"searcher/web"
)

const addr = "localhost:8080"

// dbPath is where Searcher's unified local database lives on disk: RAG
// documents/chunks, saved conversations, and settings.
const dbPath = "./data/searcher.db"

func main() {
	client := llm.NewOllamaClient("phi3:mini")
	if err := client.Init(context.Background()); err != nil {
		fmt.Printf("Got error initializing LLM client: %s", err)
		return
	}

	embedder := llm.NewOllamaEmbedder("nomic-embed-text")
	if err := embedder.Init(context.Background()); err != nil {
		fmt.Printf("Got error initializing embedding client: %s", err)
		return
	}

	db, err := store.Open(dbPath)
	if err != nil {
		fmt.Printf("Got error opening database: %s", err)
		return
	}
	defer db.Close()

	ragSource := source.NewRAGSource(db, embedder, 2000, 8000)
	crawler := scraper.NewCrawler(db, ragSource)

	if n, err := db.RecoverInterruptedCrawlJobs(context.Background()); err != nil {
		fmt.Printf("Got error recovering interrupted crawl jobs: %s\n", err)
	} else if n > 0 {
		fmt.Printf("Marked %d interrupted crawl job(s) failed after restart\n", n)
	}

	sources := map[string]source.Source{
		"Wikipedia":    source.NewWikipediaSource("en", 5),
		"Fake":         source.NewFakeSource(),
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
		fmt.Printf("Got error setting up web server: %s", err)
		return
	}

	fmt.Printf("Listening on http://%s\n", addr)
	log.Fatal(http.ListenAndServe(addr, handler))
}
