package store

import (
	"context"
	"fmt"
	"time"
)

// Document is a single uploaded document's metadata.
type Document struct {
	ID         int64
	Name       string
	MimeType   string
	AddedAt    time.Time
	ChunkCount int
}

// AddDocument inserts a new document row and returns its ID.
func (s *Store) AddDocument(ctx context.Context, name, mimeType string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO documents (name, mime_type) VALUES (?, ?)`, name, mimeType)
	if err != nil {
		return 0, fmt.Errorf("store: adding document: %w", err)
	}
	return res.LastInsertId()
}

// ListDocuments returns every stored document, newest first, with each
// one's chunk count.
func (s *Store) ListDocuments(ctx context.Context) ([]Document, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.id, d.name, d.mime_type, d.added_at, COUNT(c.id)
		FROM documents d
		LEFT JOIN chunks c ON c.document_id = d.id
		GROUP BY d.id
		ORDER BY d.added_at DESC, d.id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("store: listing documents: %w", err)
	}
	defer rows.Close()

	var docs []Document
	for rows.Next() {
		var d Document
		if err := rows.Scan(&d.ID, &d.Name, &d.MimeType, &d.AddedAt, &d.ChunkCount); err != nil {
			return nil, fmt.Errorf("store: scanning document: %w", err)
		}
		docs = append(docs, d)
	}
	return docs, rows.Err()
}

// DeleteDocument removes a document and (via ON DELETE CASCADE) its chunks.
func (s *Store) DeleteDocument(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM documents WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: deleting document %d: %w", id, err)
	}
	return nil
}
