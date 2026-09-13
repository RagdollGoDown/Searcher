package store

import (
	"context"
	"fmt"
	"math/rand"
	"path/filepath"
	"testing"
)

// benchEmbeddingDim matches nomic-embed-text's real embedding
// dimensionality, so BenchmarkSearchChunks measures cosine-similarity cost
// at a realistic vector size.
const benchEmbeddingDim = 768

// BenchmarkSearchChunks measures how SearchChunks' brute-force cosine scan
// (search.go) scales with corpus size, to inform when the "swap in an ANN
// index later" tradeoff noted there becomes worth revisiting.
func BenchmarkSearchChunks(b *testing.B) {
	for _, n := range []int{100, 1000, 5000} {
		b.Run(fmt.Sprintf("chunks=%d", n), func(b *testing.B) {
			ctx := context.Background()
			st, err := Open(filepath.Join(b.TempDir(), "bench.db"))
			if err != nil {
				b.Fatalf("opening store: %s", err)
			}
			defer st.Close()

			rng := rand.New(rand.NewSource(1))

			docID, err := st.AddDocument(ctx, "bench doc", "", "")
			if err != nil {
				b.Fatalf("adding document: %s", err)
			}

			chunks := make([]ChunkInput, n)
			for i := range chunks {
				chunks[i] = ChunkInput{
					Content:   fmt.Sprintf("bench chunk %d", i),
					Embedding: randomVector(rng, benchEmbeddingDim),
				}
			}
			if err := st.AddChunks(ctx, docID, chunks); err != nil {
				b.Fatalf("adding chunks: %s", err)
			}

			queryVec := randomVector(rng, benchEmbeddingDim)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := st.SearchChunks(ctx, queryVec, 5); err != nil {
					b.Fatalf("searching chunks: %s", err)
				}
			}
		})
	}
}

func randomVector(rng *rand.Rand, dim int) []float32 {
	v := make([]float32, dim)
	for i := range v {
		v[i] = rng.Float32()*2 - 1
	}
	return v
}
