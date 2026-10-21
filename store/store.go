// Package store is the persistent, unified local database for Searcher: it
// holds uploaded documents and their chunk embeddings for RAG retrieval,
// saved chat conversations, and arbitrary key-value settings (e.g.
// per-source configuration), all in the same file.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS documents (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL,
	mime_type TEXT NOT NULL DEFAULT '',
	added_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS crawl_jobs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	root_url TEXT NOT NULL,
	max_depth INTEGER NOT NULL,
	max_pages INTEGER NOT NULL,
	same_domain_only INTEGER NOT NULL DEFAULT 1,
	status TEXT NOT NULL DEFAULT 'pending',
	pages_crawled INTEGER NOT NULL DEFAULT 0,
	pages_failed INTEGER NOT NULL DEFAULT 0,
	error_message TEXT NOT NULL DEFAULT '',
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_crawl_jobs_status ON crawl_jobs(status);

CREATE TABLE IF NOT EXISTS chunks (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	document_id INTEGER NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
	chunk_index INTEGER NOT NULL,
	content TEXT NOT NULL,
	embedding BLOB NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_chunks_document_id ON chunks(document_id);

CREATE TABLE IF NOT EXISTS settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS conversations (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	title TEXT NOT NULL,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS messages (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	conversation_id INTEGER NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
	role TEXT NOT NULL,
	content TEXT NOT NULL,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_messages_conversation_id ON messages(conversation_id);
`

// Store is Searcher's local database: document metadata and their chunk
// embeddings, backed by a single SQLite file.
type Store struct {
	db   *sql.DB
	path string
}

// Open opens (creating if needed, including any missing parent directory)
// the SQLite database at path and ensures its schema exists.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("store: creating %s: %w", dir, err)
		}
	}

	db, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	if err != nil {
		return nil, fmt.Errorf("store: opening %s: %w", path, err)
	}
	// SQLite allows only one writer at a time; a single connection avoids
	// "database is locked" errors under concurrent requests.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: creating schema: %w", err)
	}

	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}

	return s, nil
}

// migrate applies schema changes CREATE TABLE IF NOT EXISTS can't express —
// currently just adding columns to tables that predate them. Each step is
// guarded by hasColumn so it's safe to call every time Open runs. If this
// list grows past a couple of entries, it's worth graduating to a versioned
// migrations table instead.
func (s *Store) migrate() error {
	ok, err := s.hasColumn("documents", "source_url")
	if err != nil {
		return err
	}
	if !ok {
		if _, err := s.db.Exec(`ALTER TABLE documents ADD COLUMN source_url TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("store: adding documents.source_url: %w", err)
		}
	}
	return nil
}

func (s *Store) hasColumn(table, column string) (bool, error) {
	rows, err := s.db.Query(fmt.Sprintf(`PRAGMA table_info(%s)`, table))
	if err != nil {
		return false, fmt.Errorf("store: checking %s schema: %w", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, fmt.Errorf("store: scanning %s schema: %w", table, err)
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// Close closes the underlying database.
func (s *Store) Close() error {
	return s.db.Close()
}

// SizeBytes returns the on-disk size of the database file, covering
// everything it holds (currently document metadata and chunk
// content/embeddings).
func (s *Store) SizeBytes() (int64, error) {
	info, err := os.Stat(s.path)
	if err != nil {
		return 0, fmt.Errorf("store: stat %s: %w", s.path, err)
	}
	return info.Size(), nil
}
