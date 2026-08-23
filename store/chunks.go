package store

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
)

// ChunkInput is a single chunk of a document's text, ready to store
// alongside its embedding vector.
type ChunkInput struct {
	Content   string
	Embedding []float32
}

// AddChunks stores chunks for documentID, in order.
func (s *Store) AddChunks(ctx context.Context, documentID int64, chunks []ChunkInput) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: adding chunks: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `INSERT INTO chunks (document_id, chunk_index, content, embedding) VALUES (?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("store: adding chunks: %w", err)
	}
	defer stmt.Close()

	for i, c := range chunks {
		if _, err := stmt.ExecContext(ctx, documentID, i, c.Content, encodeEmbedding(c.Embedding)); err != nil {
			return fmt.Errorf("store: inserting chunk %d: %w", i, err)
		}
	}

	return tx.Commit()
}

// encodeEmbedding packs a []float32 into raw little-endian bytes for BLOB
// storage.
func encodeEmbedding(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// decodeEmbedding unpacks bytes previously produced by encodeEmbedding.
func decodeEmbedding(b []byte) []float32 {
	v := make([]float32, len(b)/4)
	for i := range v {
		v[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
	}
	return v
}
