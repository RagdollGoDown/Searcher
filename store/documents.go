package store

import (
	"context"
	"fmt"
	"time"
)

// Document is a single uploaded document's metadata.
type Document struct {
	ID       int64
	Name     string
	MimeType string
	// SourceURL is the URL a scraped page's content was fetched from.
	// Empty for documents that didn't come from a crawl (e.g. browser
	// uploads).
	SourceURL  string
	AddedAt    time.Time
	ChunkCount int
}

// AddDocument inserts a new document row and returns its ID. sourceURL is
// the URL a scraped page was fetched from, or "" for a non-scraped
// document (e.g. a browser upload).
func (s *Store) AddDocument(ctx context.Context, name, mimeType, sourceURL string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO documents (name, mime_type, source_url) VALUES (?, ?, ?)`, name, mimeType, sourceURL)
	if err != nil {
		return 0, fmt.Errorf("store: adding document: %w", err)
	}
	return res.LastInsertId()
}

// ListDocuments returns every stored document, newest first, with each
// one's chunk count.
func (s *Store) ListDocuments(ctx context.Context) ([]Document, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT d.id, d.name, d.mime_type, d.source_url, d.added_at, COUNT(c.id)
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
		if err := rows.Scan(&d.ID, &d.Name, &d.MimeType, &d.SourceURL, &d.AddedAt, &d.ChunkCount); err != nil {
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

// DeleteDocumentsBySourceURL removes any document (and, via ON DELETE
// CASCADE, its chunks) whose SourceURL matches sourceURL. It's a no-op for
// sourceURL == "" so it can never match non-scraped (e.g. uploaded)
// documents, which all have an empty SourceURL. Used to replace a page's
// content on re-crawl rather than accumulating duplicates.
func (s *Store) DeleteDocumentsBySourceURL(ctx context.Context, sourceURL string) error {
	if sourceURL == "" {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM documents WHERE source_url = ?`, sourceURL); err != nil {
		return fmt.Errorf("store: deleting documents for %q: %w", sourceURL, err)
	}
	return nil
}
