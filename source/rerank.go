package source

import (
	"context"

	"github.com/RagdollGoDown/Searcher/store"
)

// rerankCandidateFactor is how many times TopK chunks the embedding search
// hands a Reranker. A reranker can only promote what it's shown, so it
// needs a wider pool than the final cut; scoring cost grows with this
// factor, which matters for model-backed rerankers on small hardware.
const rerankCandidateFactor = 4

// Reranker reorders RAGSource's embedding-search candidates by relevance to
// the query. It lives here rather than in llm because not every
// implementation is a model: a keyword-search fusion reranker needs only
// the store.
type Reranker interface {
	// Rerank returns the chunks to use, most relevant first, with Score
	// set to the reranker's own score. It may drop candidates or add
	// chunks it found itself; RAGSource trims the result to TopK.
	Rerank(ctx context.Context, query string, candidates []store.ScoredChunk) ([]store.ScoredChunk, error)
}
