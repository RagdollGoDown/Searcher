package store

import (
	"context"
	"database/sql"
	"fmt"
)

// GetSetting returns the stored value for key and true, or "" and false if
// key has never been set.
func (s *Store) GetSetting(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("store: getting setting %q: %w", key, err)
	}
	return value, true, nil
}

// SetSetting stores value under key, overwriting any existing value.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	if err != nil {
		return fmt.Errorf("store: setting %q: %w", key, err)
	}
	return nil
}
