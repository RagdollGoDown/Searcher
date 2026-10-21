package source

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/RagdollGoDown/Searcher/llm"
	"github.com/RagdollGoDown/Searcher/query"
	"github.com/RagdollGoDown/Searcher/store"
)

// Settings keys for RAGSource's persisted, per-source configuration
// (store.Store's settings table is a generic key-value store shared by
// whatever else ends up needing settings, e.g. other sources or general
// interface preferences).
const (
	settingChunkChars   = "rag.chunk_chars"
	settingChunkOverlap = "rag.chunk_overlap"
	settingTopK         = "rag.top_k"
)

const ragDefaultMaxChunkChars = 2000
const ragDefaultMaxTotalChars = 8000

// ragDefaultTopK is how many chunks a query retrieves when TopK hasn't
// been explicitly configured.
const ragDefaultTopK = 5

// ragIcon is a magnifying-glass-over-document glyph.
const ragIcon = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` +
	`<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h6"></path>` +
	`<path d="M14 2v6h6"></path>` +
	`<circle cx="16.5" cy="17.5" r="2.5"></circle>` +
	`<path d="M18.5 19.5 21 22"></path>` +
	`</svg>`

// RAGSource searches a persistent store.Store of previously uploaded
// documents by embedding the query text and retrieving the most similar
// chunks, following the same per-result/total truncation pattern as
// FilesSource so retrieved content can't blow out the LLM's context
// window. Unlike FilesSource (which turns per-request attachments directly
// into Results), RAGSource searches documents uploaded ahead of time via
// the RAG management page.
type RAGSource struct {
	store         *store.Store
	embedder      llm.Embedder
	reranker      Reranker
	maxChunkChars int
	maxTotalChars int
}

// NewRAGSource creates a RAGSource retrieving from st, embedding the query
// text with embedder. A nil reranker keeps the embedding search's order.
// How many chunks a query retrieves is configured via Settings.TopK (see
// GetSettings/SetSettings), not fixed at construction.
func NewRAGSource(st *store.Store, embedder llm.Embedder, reranker Reranker, maxChunkChars, maxTotalChars int) *RAGSource {
	return &RAGSource{store: st, embedder: embedder, reranker: reranker, maxChunkChars: maxChunkChars, maxTotalChars: maxTotalChars}
}

// Compile-time check that RAGSource satisfies Source.
var _ Source = (*RAGSource)(nil)

func (s *RAGSource) Icon() string {
	return ragIcon
}

func (s *RAGSource) Fetch(q query.Query) ([]Result, error) {
	maxChunkChars := s.maxChunkChars
	if maxChunkChars <= 0 {
		maxChunkChars = ragDefaultMaxChunkChars
	}
	maxTotalChars := s.maxTotalChars
	if maxTotalChars <= 0 {
		maxTotalChars = ragDefaultMaxTotalChars
	}

	ctx := context.Background()

	settings, err := s.GetSettings(ctx)
	if err != nil {
		return nil, fmt.Errorf("rag: reading settings: %w", err)
	}

	vec, err := s.embedder.Embed(ctx, q.GetQuery())
	if err != nil {
		return nil, fmt.Errorf("rag: embedding query: %w", err)
	}

	candidates := settings.TopK
	if s.reranker != nil {
		candidates *= rerankCandidateFactor
	}
	chunks, err := s.store.SearchChunks(ctx, vec, candidates)
	if err != nil {
		return nil, fmt.Errorf("rag: searching chunks: %w", err)
	}

	if s.reranker != nil {
		chunks, err = s.reranker.Rerank(ctx, q.GetQuery(), chunks)
		if err != nil {
			return nil, fmt.Errorf("rag: reranking: %w", err)
		}
		if len(chunks) > settings.TopK {
			chunks = chunks[:settings.TopK]
		}
	}

	results := make([]Result, 0, len(chunks))
	remaining := maxTotalChars
	for _, c := range chunks {
		if remaining <= 0 {
			break
		}

		content := c.Content
		if len(content) > maxChunkChars {
			content = content[:maxChunkChars]
		}
		if len(content) > remaining {
			content = content[:remaining]
		}
		remaining -= len(content)

		results = append(results, Result{
			Title:   fmt.Sprintf("%s (chunk %d)", c.DocumentName, c.ChunkIndex+1),
			Extract: content,
		})
	}

	return results, nil
}

// DocumentInfo is a stored document's metadata, as returned by
// ListDocuments. It's a copy of store.Document's fields so callers (e.g.
// the web package) can manage the RAG store through RAGSource alone,
// without importing the store package directly.
type DocumentInfo struct {
	ID         int64
	Name       string
	MimeType   string
	SourceURL  string
	AddedAt    time.Time
	ChunkCount int
}

// Ingest turns an attachment (e.g. a file uploaded through the RAG
// management page) into plain text, chunks it, embeds each chunk, and
// stores it as a new document. It returns the new document's ID. Chunking
// uses the currently configured Settings; changing them only affects
// documents ingested afterward.
func (s *RAGSource) Ingest(ctx context.Context, a query.Attachment) (int64, error) {
	content := resolveContent(a)

	cs, err := s.GetSettings(ctx)
	if err != nil {
		return 0, fmt.Errorf("rag: reading settings: %w", err)
	}

	chunks := store.ChunkText(content, cs.ChunkChars, cs.ChunkOverlap)
	if len(chunks) == 0 {
		return 0, fmt.Errorf("rag: document has no extractable text")
	}

	docID, err := s.store.AddDocument(ctx, a.Name, a.MimeType, a.SourceURL)
	if err != nil {
		return 0, fmt.Errorf("rag: adding document: %w", err)
	}

	inputs := make([]store.ChunkInput, len(chunks))
	for i, c := range chunks {
		vec, err := s.embedder.Embed(ctx, c)
		if err != nil {
			return 0, fmt.Errorf("rag: embedding chunk %d: %w", i, err)
		}
		inputs[i] = store.ChunkInput{Content: c, Embedding: vec}
	}

	if err := s.store.AddChunks(ctx, docID, inputs); err != nil {
		return 0, fmt.Errorf("rag: storing chunks: %w", err)
	}

	return docID, nil
}

// ListDocuments returns metadata for every stored document.
func (s *RAGSource) ListDocuments(ctx context.Context) ([]DocumentInfo, error) {
	docs, err := s.store.ListDocuments(ctx)
	if err != nil {
		return nil, err
	}

	infos := make([]DocumentInfo, len(docs))
	for i, d := range docs {
		infos[i] = DocumentInfo{ID: d.ID, Name: d.Name, MimeType: d.MimeType, SourceURL: d.SourceURL, AddedAt: d.AddedAt, ChunkCount: d.ChunkCount}
	}
	return infos, nil
}

// DeleteDocument removes a stored document and its chunks.
func (s *RAGSource) DeleteDocument(ctx context.Context, id int64) error {
	return s.store.DeleteDocument(ctx, id)
}

// DeleteDocumentsByDomain removes every stored document whose SourceURL's
// host matches domain exactly (e.g. "example.com") — the same host
// comparison the scraper uses for its same-domain crawl restriction.
// Documents with no SourceURL (e.g. browser uploads) are never matched.
// Returns how many documents were deleted.
func (s *RAGSource) DeleteDocumentsByDomain(ctx context.Context, domain string) (int, error) {
	docs, err := s.store.ListDocuments(ctx)
	if err != nil {
		return 0, err
	}

	var deleted int
	for _, d := range docs {
		if d.SourceURL == "" {
			continue
		}
		u, err := url.Parse(d.SourceURL)
		if err != nil || u.Hostname() != domain {
			continue
		}
		if err := s.store.DeleteDocument(ctx, d.ID); err != nil {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

// StorageSizeBytes returns the on-disk size of the RAG store's database
// file, covering both document metadata and chunk content/embeddings.
func (s *RAGSource) StorageSizeBytes() (int64, error) {
	return s.store.SizeBytes()
}

// Settings are RAGSource's persisted, user-configurable parameters:
// ChunkChars/ChunkOverlap control how newly ingested documents are split
// (changing them, via SetSettings, only affects documents ingested
// afterward — existing chunks are never rebuilt), while TopK controls how
// many chunks a query retrieves (changing it takes effect on the very next
// query, since retrieval reads it fresh each time).
type Settings struct {
	ChunkChars   int
	ChunkOverlap int
	TopK         int
}

// GetSettings returns the settings currently configured, falling back to
// store.DefaultChunkChars/DefaultChunkOverlap/ragDefaultTopK for any value
// that hasn't been explicitly set. It always reads through to the store
// rather than caching: for chunk settings that's negligible next to an
// upload's embedding calls, and for TopK it's what makes a change take
// effect immediately for the next query rather than needing a restart.
func (s *RAGSource) GetSettings(ctx context.Context) (Settings, error) {
	chars, err := s.getIntSetting(ctx, settingChunkChars, store.DefaultChunkChars)
	if err != nil {
		return Settings{}, err
	}
	overlap, err := s.getIntSetting(ctx, settingChunkOverlap, store.DefaultChunkOverlap)
	if err != nil {
		return Settings{}, err
	}
	topK, err := s.getIntSetting(ctx, settingTopK, ragDefaultTopK)
	if err != nil {
		return Settings{}, err
	}
	return Settings{ChunkChars: chars, ChunkOverlap: overlap, TopK: topK}, nil
}

// SetSettings persists cs: ChunkChars/ChunkOverlap for documents ingested
// from now on, TopK for queries from now on.
func (s *RAGSource) SetSettings(ctx context.Context, cs Settings) error {
	if cs.ChunkChars <= 0 {
		return fmt.Errorf("rag: chunk size must be positive")
	}
	if cs.ChunkOverlap < 0 || cs.ChunkOverlap >= cs.ChunkChars {
		return fmt.Errorf("rag: chunk overlap must be non-negative and smaller than chunk size")
	}
	if cs.TopK <= 0 {
		return fmt.Errorf("rag: number of chunks retrieved must be positive")
	}
	if err := s.store.SetSetting(ctx, settingChunkChars, strconv.Itoa(cs.ChunkChars)); err != nil {
		return err
	}
	if err := s.store.SetSetting(ctx, settingChunkOverlap, strconv.Itoa(cs.ChunkOverlap)); err != nil {
		return err
	}
	return s.store.SetSetting(ctx, settingTopK, strconv.Itoa(cs.TopK))
}

// getIntSetting returns the int stored under key, or def if it's unset or
// unparseable (so a corrupted setting degrades to the default instead of
// breaking ingestion).
func (s *RAGSource) getIntSetting(ctx context.Context, key string, def int) (int, error) {
	v, ok, err := s.store.GetSetting(ctx, key)
	if err != nil {
		return 0, err
	}
	if !ok {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def, nil
	}
	return n, nil
}
