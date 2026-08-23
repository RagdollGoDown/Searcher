package source

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"searcher/llm"
	"searcher/query"
	"searcher/store"
)

// Settings keys for RAGSource's persisted, per-source configuration
// (store.Store's settings table is a generic key-value store shared by
// whatever else ends up needing settings, e.g. other sources or general
// interface preferences).
const (
	settingChunkChars   = "rag.chunk_chars"
	settingChunkOverlap = "rag.chunk_overlap"
)

const ragDefaultMaxChunkChars = 2000
const ragDefaultMaxTotalChars = 8000

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
	topK          int
	maxChunkChars int
	maxTotalChars int
}

// NewRAGSource creates a RAGSource retrieving up to topK chunks per query
// from st, embedding the query text with embedder.
func NewRAGSource(st *store.Store, embedder llm.Embedder, topK, maxChunkChars, maxTotalChars int) *RAGSource {
	return &RAGSource{store: st, embedder: embedder, topK: topK, maxChunkChars: maxChunkChars, maxTotalChars: maxTotalChars}
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

	vec, err := s.embedder.Embed(ctx, q.GetQuery())
	if err != nil {
		return nil, fmt.Errorf("rag: embedding query: %w", err)
	}

	chunks, err := s.store.SearchChunks(ctx, vec, s.topK)
	if err != nil {
		return nil, fmt.Errorf("rag: searching chunks: %w", err)
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
	AddedAt    time.Time
	ChunkCount int
}

// Ingest turns an attachment (e.g. a file uploaded through the RAG
// management page) into plain text, chunks it, embeds each chunk, and
// stores it as a new document. It returns the new document's ID. Chunking
// uses the currently configured ChunkSettings; changing them only affects
// documents ingested afterward.
func (s *RAGSource) Ingest(ctx context.Context, a query.Attachment) (int64, error) {
	content := resolveContent(a)

	cs, err := s.GetChunkSettings(ctx)
	if err != nil {
		return 0, fmt.Errorf("rag: reading chunk settings: %w", err)
	}

	chunks := store.ChunkText(content, cs.ChunkChars, cs.ChunkOverlap)
	if len(chunks) == 0 {
		return 0, fmt.Errorf("rag: document has no extractable text")
	}

	docID, err := s.store.AddDocument(ctx, a.Name, a.MimeType)
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
		infos[i] = DocumentInfo{ID: d.ID, Name: d.Name, MimeType: d.MimeType, AddedAt: d.AddedAt, ChunkCount: d.ChunkCount}
	}
	return infos, nil
}

// DeleteDocument removes a stored document and its chunks.
func (s *RAGSource) DeleteDocument(ctx context.Context, id int64) error {
	return s.store.DeleteDocument(ctx, id)
}

// StorageSizeBytes returns the on-disk size of the RAG store's database
// file, covering both document metadata and chunk content/embeddings.
func (s *RAGSource) StorageSizeBytes() (int64, error) {
	return s.store.SizeBytes()
}

// ChunkSettings are the parameters used to split newly ingested documents
// into chunks. Changing them (via SetChunkSettings) only affects documents
// ingested afterward — existing chunks are never rebuilt.
type ChunkSettings struct {
	ChunkChars   int
	ChunkOverlap int
}

// GetChunkSettings returns the chunk settings currently configured,
// falling back to store.DefaultChunkChars/DefaultChunkOverlap for any
// value that hasn't been explicitly set. It always reads through to the
// store rather than caching, since it's only called once per document
// upload — negligible next to that upload's embedding calls — and a cached
// copy would risk going stale relative to the stored value.
func (s *RAGSource) GetChunkSettings(ctx context.Context) (ChunkSettings, error) {
	chars, err := s.getIntSetting(ctx, settingChunkChars, store.DefaultChunkChars)
	if err != nil {
		return ChunkSettings{}, err
	}
	overlap, err := s.getIntSetting(ctx, settingChunkOverlap, store.DefaultChunkOverlap)
	if err != nil {
		return ChunkSettings{}, err
	}
	return ChunkSettings{ChunkChars: chars, ChunkOverlap: overlap}, nil
}

// SetChunkSettings persists cs for documents ingested from now on.
func (s *RAGSource) SetChunkSettings(ctx context.Context, cs ChunkSettings) error {
	if cs.ChunkChars <= 0 {
		return fmt.Errorf("rag: chunk size must be positive")
	}
	if cs.ChunkOverlap < 0 || cs.ChunkOverlap >= cs.ChunkChars {
		return fmt.Errorf("rag: chunk overlap must be non-negative and smaller than chunk size")
	}
	if err := s.store.SetSetting(ctx, settingChunkChars, strconv.Itoa(cs.ChunkChars)); err != nil {
		return err
	}
	return s.store.SetSetting(ctx, settingChunkOverlap, strconv.Itoa(cs.ChunkOverlap))
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
