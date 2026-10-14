// Package scraper crawls a website starting from a root URL and ingests
// each crawled page's text into the shared RAG store via source.RAGSource,
// so crawled pages become ordinary documents+chunks, searchable through
// the existing "My Documents" source with no separate retrieval path.
// Crawls run as background jobs (see Crawler.StartCrawl), since a
// depth-based crawl can take far longer than a typical HTTP request should
// block for.
package scraper

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/RagdollGoDown/Searcher/query"
	"github.com/RagdollGoDown/Searcher/source"
	"github.com/RagdollGoDown/Searcher/store"
)

const (
	defaultUserAgent = "Searcher/1.0 (self-hosted website scraper)"
	fetchTimeout     = 15 * time.Second
	// maxPageBytes caps how much of a single page's body is read, so one
	// huge page can't spike memory on small hardware (mirrors
	// filesMaxPDFBytes in source/files.go).
	maxPageBytes = 5 * 1024 * 1024
	// storeWriteTimeout bounds the small, frequent progress/status writes
	// made during a crawl.
	storeWriteTimeout = 5 * time.Second
	// ingestTimeout bounds a single page's delete-old-copy+ingest step,
	// which includes embedding every chunk of that page and can
	// legitimately take a while for a long page.
	ingestTimeout = 2 * time.Minute
	// scraperMaxAllowedPages is a hard ceiling on MaxPages regardless of
	// user input, matching the project's small-hardware ethos. StartCrawl
	// rejects a request over this rather than silently clamping it.
	scraperMaxAllowedPages = 1000
)

// Params are the user-supplied parameters for a single crawl job. A
// non-positive MaxDepth/MaxPages falls back to the configured Settings
// default, the same pattern RAGSource uses for maxChunkChars/maxTotalChars.
type Params struct {
	RootURL        string
	MaxDepth       int
	MaxPages       int
	SameDomainOnly bool
}

// Crawler runs website crawls as background jobs. Job state
// (status/progress) is persisted in store.Store's crawl_jobs table so it
// survives a restart (see store.RecoverInterruptedCrawlJobs); cancels is an
// in-memory registry of the live goroutines' cancel funcs, since a DB row
// can't hold a runnable Go value — cancelling a job requires reaching into
// this process's own goroutine.
type Crawler struct {
	store *store.Store
	rag   *source.RAGSource

	client *http.Client

	mu      sync.Mutex
	cancels map[int64]context.CancelFunc
}

// NewCrawler creates a Crawler that ingests crawled pages into rag's
// backing store st.
func NewCrawler(st *store.Store, rag *source.RAGSource) *Crawler {
	return &Crawler{
		store:   st,
		rag:     rag,
		client:  &http.Client{},
		cancels: make(map[int64]context.CancelFunc),
	}
}

// StartCrawl validates p (falling back to configured Settings defaults for
// any unset MaxDepth/MaxPages), creates a crawl_jobs row, and launches the
// crawl in a background goroutine, returning the new job's ID immediately.
// The goroutine runs against its own context, not ctx (the caller's, e.g. an
// HTTP request's), since it must outlive whatever called StartCrawl.
func (c *Crawler) StartCrawl(ctx context.Context, p Params) (int64, error) {
	root, err := url.Parse(p.RootURL)
	if err != nil || (root.Scheme != "http" && root.Scheme != "https") || root.Host == "" {
		return 0, fmt.Errorf("scraper: invalid root URL %q", p.RootURL)
	}

	settings, err := c.GetSettings(ctx)
	if err != nil {
		return 0, err
	}
	if p.MaxDepth <= 0 {
		p.MaxDepth = settings.MaxDepth
	}
	if p.MaxPages <= 0 {
		p.MaxPages = settings.MaxPages
	}
	if p.MaxPages > scraperMaxAllowedPages {
		return 0, fmt.Errorf("scraper: max pages must not exceed %d", scraperMaxAllowedPages)
	}

	jobID, err := c.store.CreateCrawlJob(ctx, root.String(), p.MaxDepth, p.MaxPages, p.SameDomainOnly)
	if err != nil {
		return 0, err
	}

	runCtx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.cancels[jobID] = cancel
	c.mu.Unlock()

	politeDelay := time.Duration(settings.PoliteDelayMillis) * time.Millisecond

	go func() {
		robots := fetchRobots(runCtx, c.client, root)
		c.run(runCtx, jobID, root, p, robots, politeDelay)
	}()

	return jobID, nil
}

// CancelCrawl signals a running/pending job to stop. It returns false if
// the job has no active goroutine in this process (already finished, or an
// unknown ID) — callers should fall back to deleting the row outright in
// that case. Cancellation is asynchronous: the job's status flips to
// "cancelled" once its goroutine observes ctx.Done(), not synchronously
// with this call.
func (c *Crawler) CancelCrawl(jobID int64) bool {
	c.mu.Lock()
	cancel, ok := c.cancels[jobID]
	if ok {
		delete(c.cancels, jobID)
	}
	c.mu.Unlock()

	if !ok {
		return false
	}
	cancel()
	return true
}

type queuedURL struct {
	url   string
	depth int
}

// run performs the BFS crawl for job jobID. Every store write in here uses
// its own context.Background()-derived context (via setStatus/
// updateProgress/the ingest step), never ctx — once cancellation fires, ctx
// is already Done(), and writing the final "cancelled" status against an
// already-cancelled context would itself fail, leaving the job stuck
// showing "running" forever. ctx is used only to bound network fetches and
// to detect cancellation between pages.
func (c *Crawler) run(ctx context.Context, jobID int64, root *url.URL, p Params, robots *robotsRules, politeDelay time.Duration) {
	defer func() {
		c.mu.Lock()
		delete(c.cancels, jobID)
		c.mu.Unlock()
	}()

	c.setStatus(jobID, store.CrawlJobRunning, "")

	queue := []queuedURL{{url: root.String(), depth: 0}}
	visited := map[string]bool{}
	pagesCrawled, pagesFailed := 0, 0

	for len(queue) > 0 && pagesCrawled < p.MaxPages {
		if ctx.Err() != nil {
			c.setStatus(jobID, store.CrawlJobCancelled, "")
			return
		}

		item := queue[0]
		queue = queue[1:]

		if visited[item.url] {
			continue
		}
		visited[item.url] = true

		u, err := url.Parse(item.url)
		if err != nil {
			continue
		}
		if p.SameDomainOnly && !sameDomain(root, item.url) {
			continue
		}
		if !robots.allowed(u.Path) {
			continue
		}

		if item.depth > 0 && politeDelay > 0 {
			time.Sleep(politeDelay)
		}

		body, contentType, err := c.fetch(ctx, item.url)
		if err != nil {
			pagesFailed++
			c.updateProgress(jobID, pagesCrawled, pagesFailed)
			continue
		}
		if !strings.Contains(contentType, "text/html") {
			continue
		}

		if item.depth < p.MaxDepth {
			for _, link := range extractLinks(item.url, body) {
				if !visited[link] {
					queue = append(queue, queuedURL{url: link, depth: item.depth + 1})
				}
			}
		}

		text := extractText(body)
		if strings.TrimSpace(text) == "" {
			continue
		}
		title := extractTitle(body)
		if title == "" {
			title = item.url
		}

		if c.ingestPage(item.url, title, text) {
			pagesCrawled++
		} else {
			pagesFailed++
		}
		c.updateProgress(jobID, pagesCrawled, pagesFailed)
	}

	c.setStatus(jobID, store.CrawlJobCompleted, "")
}

// ingestPage replaces any existing document for pageURL and ingests the
// freshly crawled content in its place, so re-crawling a page updates its
// document rather than accumulating duplicates. Reports whether ingestion
// succeeded.
func (c *Crawler) ingestPage(pageURL, title, text string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), ingestTimeout)
	defer cancel()

	if err := c.store.DeleteDocumentsBySourceURL(ctx, pageURL); err != nil {
		fmt.Printf("scraper: deleting old document for %s: %s\n", pageURL, err)
		return false
	}

	attachment := query.Attachment{Name: title, Content: text, SourceURL: pageURL}
	if _, err := c.rag.Ingest(ctx, attachment); err != nil {
		fmt.Printf("scraper: ingesting %s: %s\n", pageURL, err)
		return false
	}
	return true
}

// fetch retrieves pageURL, returning its body (capped at maxPageBytes) and
// Content-Type header.
func (c *Crawler) fetch(ctx context.Context, pageURL string) ([]byte, string, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", defaultUserAgent)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("scraper: fetching %s: status %d", pageURL, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes))
	if err != nil {
		return nil, "", fmt.Errorf("scraper: reading %s: %w", pageURL, err)
	}

	return body, resp.Header.Get("Content-Type"), nil
}

func (c *Crawler) setStatus(jobID int64, status store.CrawlJobStatus, errMsg string) {
	ctx, cancel := context.WithTimeout(context.Background(), storeWriteTimeout)
	defer cancel()
	if err := c.store.SetCrawlJobStatus(ctx, jobID, status, errMsg); err != nil {
		fmt.Printf("scraper: updating job %d status: %s\n", jobID, err)
	}
}

func (c *Crawler) updateProgress(jobID int64, pagesCrawled, pagesFailed int) {
	ctx, cancel := context.WithTimeout(context.Background(), storeWriteTimeout)
	defer cancel()
	if err := c.store.UpdateCrawlJobProgress(ctx, jobID, pagesCrawled, pagesFailed); err != nil {
		fmt.Printf("scraper: updating job %d progress: %s\n", jobID, err)
	}
}
