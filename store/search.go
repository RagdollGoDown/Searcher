package store

import (
	"context"
	"fmt"
	"math"
	"sort"
)

// ScoredChunk is a chunk returned from a similarity search, together with
// its cosine similarity to the query (higher is more similar).
type ScoredChunk struct {
	DocumentID   int64
	DocumentName string
	ChunkIndex   int
	Content      string
	Score        float32
}

// SearchChunks returns the topK chunks most similar to queryVec by cosine
// similarity, computed by scanning every stored chunk (brute force, not an
// ANN index). Kept as this one function so a future index swap (e.g.
// sqlite-vec, or a pure-Go HNSW library) only has to change this file.
func (s *Store) SearchChunks(ctx context.Context, queryVec []float32, topK int) ([]ScoredChunk, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.document_id, d.name, c.chunk_index, c.content, c.embedding
		FROM chunks c
		JOIN documents d ON d.id = c.document_id
	`)
	if err != nil {
		return nil, fmt.Errorf("store: searching chunks: %w", err)
	}
	defer rows.Close()

	var scored []ScoredChunk
	for rows.Next() {
		var sc ScoredChunk
		var embeddingBytes []byte
		if err := rows.Scan(&sc.DocumentID, &sc.DocumentName, &sc.ChunkIndex, &sc.Content, &embeddingBytes); err != nil {
			return nil, fmt.Errorf("store: scanning chunk: %w", err)
		}
		sc.Score = cosineSimilarity(queryVec, decodeEmbedding(embeddingBytes))
		scored = append(scored, sc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: searching chunks: %w", err)
	}

	sort.Slice(scored, func(i, j int) bool { return scored[i].Score > scored[j].Score })
	if topK > 0 && len(scored) > topK {
		scored = scored[:topK]
	}
	return scored, nil
}

// cosineSimilarity returns the cosine similarity of a and b, or 0 if
// they're empty, differently sized, or either has zero magnitude.
func cosineSimilarity(a, b []float32) float32 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}

	var dot, magA, magB float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		magA += float64(a[i]) * float64(a[i])
		magB += float64(b[i]) * float64(b[i])
	}
	if magA == 0 || magB == 0 {
		return 0
	}
	return float32(dot / (math.Sqrt(magA) * math.Sqrt(magB)))
}
