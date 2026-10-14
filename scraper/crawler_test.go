package scraper_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RagdollGoDown/Searcher/llm"
	"github.com/RagdollGoDown/Searcher/scraper"
	"github.com/RagdollGoDown/Searcher/source"
	"github.com/RagdollGoDown/Searcher/store"
)

const (
	homeHTML = `<html><head><title>Home</title></head><body>
<nav>NAVBOILERPLATE Home About Contact</nav>
<header>HEADERBOILERPLATE Site Header</header>
<p>REALCONTENT This is the real home page content, about widgets.</p>
<a href="/page2">Page Two</a>
<a href="/private/secret">Secret</a>
<a href="http://external.example.invalid/other">External</a>
<footer>FOOTERBOILERPLATE Copyright 2024</footer>
</body></html>`

	page2HTML = `<html><head><title>Page Two</title></head><body>
<nav>NAVBOILERPLATE Home About Contact</nav>
<p>REALCONTENT This is the real page two content, about gadgets.</p>
<script>var x = "SCRIPTBOILERPLATE should never appear";</script>
<footer>FOOTERBOILERPLATE Copyright 2024</footer>
</body></html>`

	secretHTML = `<html><head><title>Secret</title></head><body>
<p>REALCONTENT This should never be crawled: robots.txt disallows it.</p>
</body></html>`

	robotsTxt = "User-agent: *\nDisallow: /private/\n"
)

// TestCrawlerExtractsCleanTextAndRespectsPolicy crawls a small fixture
// "site" served by httptest and asserts on what actually lands in the
// store: nav/header/footer/script boilerplate must be stripped from
// ingested text (scraper/html.go's extractText), and a page disallowed by
// robots.txt must never be ingested at all (scraper/robots.go). Uses
// llm.FakeEmbedder since only extraction/policy correctness is under test
// here, not retrieval quality.
func TestCrawlerExtractsCleanTextAndRespectsPolicy(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(homeHTML))
	})
	mux.HandleFunc("/page2", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(page2HTML))
	})
	mux.HandleFunc("/private/secret", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(secretHTML))
	})
	mux.HandleFunc("/robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(robotsTxt))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "crawler.db"))
	if err != nil {
		t.Fatalf("opening store: %s", err)
	}
	defer st.Close()

	const embeddingDim = 8
	rag := source.NewRAGSource(st, llm.NewFakeEmbedder(embeddingDim), 2000, 8000)
	crawler := scraper.NewCrawler(st, rag)

	jobID, err := crawler.StartCrawl(ctx, scraper.Params{
		RootURL:        ts.URL,
		MaxDepth:       2,
		MaxPages:       10,
		SameDomainOnly: true,
	})
	if err != nil {
		t.Fatalf("starting crawl: %s", err)
	}

	deadline := time.Now().Add(10 * time.Second)
	var job store.CrawlJob
	for {
		job, err = st.GetCrawlJob(ctx, jobID)
		if err != nil {
			t.Fatalf("getting crawl job: %s", err)
		}
		if job.Status == store.CrawlJobCompleted || job.Status == store.CrawlJobFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("crawl did not finish in time, last status %q", job.Status)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if job.Status != store.CrawlJobCompleted {
		t.Fatalf("crawl finished with status %q, error %q", job.Status, job.ErrorMessage)
	}

	docs, err := st.ListDocuments(ctx)
	if err != nil {
		t.Fatalf("listing documents: %s", err)
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 ingested documents (home + page2), got %d", len(docs))
	}
	for _, d := range docs {
		if strings.Contains(d.SourceURL, "/private/") {
			t.Errorf("robots.txt-disallowed page was ingested: %s", d.SourceURL)
		}
	}

	// SearchChunks with topK<=0 returns every stored chunk regardless of
	// score, so a zero query vector still gives us every chunk's raw
	// content to inspect — ranking doesn't matter for this assertion.
	zeroVec := make([]float32, embeddingDim)
	chunks, err := st.SearchChunks(ctx, zeroVec, 0)
	if err != nil {
		t.Fatalf("reading chunks: %s", err)
	}
	var all strings.Builder
	for _, c := range chunks {
		all.WriteString(c.Content)
		all.WriteString("\n")
	}
	text := all.String()

	for _, marker := range []string{"NAVBOILERPLATE", "HEADERBOILERPLATE", "FOOTERBOILERPLATE", "SCRIPTBOILERPLATE"} {
		if strings.Contains(text, marker) {
			t.Errorf("extracted content includes boilerplate marker %q, want it stripped", marker)
		}
	}
	if !strings.Contains(text, "widgets") || !strings.Contains(text, "gadgets") {
		t.Errorf("extracted content missing expected real page content: %q", text)
	}
}
