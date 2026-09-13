package llm

import (
	"context"
	"hash/fnv"
	"math/rand"
)

// FakeEmbedder is a deterministic Embedder for tests: it needs no running
// model server, and the same text always produces the same vector, so
// callers that only need *some* embedding (not a semantically meaningful
// one, e.g. tests of extraction/crawl-policy correctness rather than
// retrieval quality) can avoid depending on a live Ollama instance.
type FakeEmbedder struct {
	dim int
}

// NewFakeEmbedder creates a FakeEmbedder producing dim-dimensional vectors.
func NewFakeEmbedder(dim int) *FakeEmbedder {
	return &FakeEmbedder{dim: dim}
}

// Compile-time check that FakeEmbedder satisfies Embedder.
var _ Embedder = (*FakeEmbedder)(nil)

func (e *FakeEmbedder) Init(ctx context.Context) error {
	return nil
}

// Embed deterministically hashes text to seed a PRNG, then generates a
// dim-dimensional vector from it — the same text always yields the same
// vector, with no network or model call.
func (e *FakeEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	h := fnv.New64a()
	h.Write([]byte(text))
	rng := rand.New(rand.NewSource(int64(h.Sum64())))

	vec := make([]float32, e.dim)
	for i := range vec {
		vec[i] = rng.Float32()*2 - 1
	}
	return vec, nil
}
