package source

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/RagdollGoDown/Searcher/llm"
	"github.com/RagdollGoDown/Searcher/query"
	"github.com/RagdollGoDown/Searcher/store"
)

// reversingReranker reverses its candidates, so a test can tell reranked
// output from embedding order.
type reversingReranker struct {
	gotCandidates int
}

func (r *reversingReranker) Rerank(ctx context.Context, query string, candidates []store.ScoredChunk) ([]store.ScoredChunk, error) {
	r.gotCandidates = len(candidates)
	out := slices.Clone(candidates)
	slices.Reverse(out)
	return out, nil
}

// TestRAGSourceReranks checks that Fetch widens the candidate pool for the
// reranker, keeps the reranker's order, and trims back to TopK.
func TestRAGSourceReranks(t *testing.T) {
	ctx := context.Background()

	st, err := store.Open(filepath.Join(t.TempDir(), "rerank.db"))
	if err != nil {
		t.Fatalf("opening store: %s", err)
	}
	defer st.Close()

	embedder := llm.NewFakeEmbedder(16)
	reranker := &reversingReranker{}
	rag := NewRAGSource(st, embedder, reranker, ragDefaultMaxChunkChars, ragDefaultMaxTotalChars)
	const topK = 2
	if err := rag.SetSettings(ctx, Settings{
		ChunkChars:   store.DefaultChunkChars,
		ChunkOverlap: store.DefaultChunkOverlap,
		TopK:         topK,
	}); err != nil {
		t.Fatalf("setting TopK: %s", err)
	}

	const docs = 20
	for i := range docs {
		a := query.Attachment{Name: fmt.Sprintf("doc-%d", i), Content: fmt.Sprintf("document number %d", i)}
		if _, err := rag.Ingest(ctx, a); err != nil {
			t.Fatalf("ingesting %s: %s", a.Name, err)
		}
	}

	q := "which document?"
	results, err := rag.Fetch(*query.NewQuery(q))
	if err != nil {
		t.Fatalf("fetching: %s", err)
	}

	if want := topK * rerankCandidateFactor; reranker.gotCandidates != want {
		t.Errorf("reranker got %d candidates, want %d", reranker.gotCandidates, want)
	}

	vec, err := embedder.Embed(ctx, q)
	if err != nil {
		t.Fatalf("embedding query: %s", err)
	}
	embeddingOrder, err := st.SearchChunks(ctx, vec, topK*rerankCandidateFactor)
	if err != nil {
		t.Fatalf("searching chunks: %s", err)
	}
	slices.Reverse(embeddingOrder)

	if len(results) != topK {
		t.Fatalf("Fetch returned %d results, want %d", len(results), topK)
	}
	for i, r := range results {
		if r.Extract != embeddingOrder[i].Content {
			t.Errorf("result %d = %q, want reranked %q", i, r.Extract, embeddingOrder[i].Content)
		}
	}
}
