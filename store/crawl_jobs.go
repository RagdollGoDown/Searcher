package store

import (
	"context"
	"fmt"
	"time"
)

// CrawlJobStatus is a crawl job's lifecycle state.
type CrawlJobStatus string

const (
	CrawlJobPending   CrawlJobStatus = "pending"
	CrawlJobRunning   CrawlJobStatus = "running"
	CrawlJobCompleted CrawlJobStatus = "completed"
	CrawlJobFailed    CrawlJobStatus = "failed"
	CrawlJobCancelled CrawlJobStatus = "cancelled"
)

// CrawlJob is a single website-crawl job's parameters and progress.
type CrawlJob struct {
	ID             int64
	RootURL        string
	MaxDepth       int
	MaxPages       int
	SameDomainOnly bool
	Status         CrawlJobStatus
	PagesCrawled   int
	PagesFailed    int
	ErrorMessage   string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// CreateCrawlJob inserts a new pending crawl job and returns its ID.
func (s *Store) CreateCrawlJob(ctx context.Context, rootURL string, maxDepth, maxPages int, sameDomainOnly bool) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO crawl_jobs (root_url, max_depth, max_pages, same_domain_only, status)
		VALUES (?, ?, ?, ?, ?)
	`, rootURL, maxDepth, maxPages, sameDomainOnly, CrawlJobPending)
	if err != nil {
		return 0, fmt.Errorf("store: creating crawl job: %w", err)
	}
	return res.LastInsertId()
}

// ListCrawlJobs returns every crawl job, newest first.
func (s *Store) ListCrawlJobs(ctx context.Context) ([]CrawlJob, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, root_url, max_depth, max_pages, same_domain_only, status,
			pages_crawled, pages_failed, error_message, created_at, updated_at
		FROM crawl_jobs
		ORDER BY created_at DESC, id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("store: listing crawl jobs: %w", err)
	}
	defer rows.Close()

	var jobs []CrawlJob
	for rows.Next() {
		j, err := scanCrawlJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// GetCrawlJob returns a single crawl job by ID.
func (s *Store) GetCrawlJob(ctx context.Context, id int64) (CrawlJob, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, root_url, max_depth, max_pages, same_domain_only, status,
			pages_crawled, pages_failed, error_message, created_at, updated_at
		FROM crawl_jobs
		WHERE id = ?
	`, id)
	return scanCrawlJob(row)
}

// crawlJobScanner covers both *sql.Row and *sql.Rows.
type crawlJobScanner interface {
	Scan(dest ...any) error
}

func scanCrawlJob(row crawlJobScanner) (CrawlJob, error) {
	var j CrawlJob
	err := row.Scan(&j.ID, &j.RootURL, &j.MaxDepth, &j.MaxPages, &j.SameDomainOnly, &j.Status,
		&j.PagesCrawled, &j.PagesFailed, &j.ErrorMessage, &j.CreatedAt, &j.UpdatedAt)
	if err != nil {
		return CrawlJob{}, fmt.Errorf("store: scanning crawl job: %w", err)
	}
	return j, nil
}

// UpdateCrawlJobProgress records how many pages a running job has crawled
// and failed so far.
func (s *Store) UpdateCrawlJobProgress(ctx context.Context, id int64, pagesCrawled, pagesFailed int) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE crawl_jobs SET pages_crawled = ?, pages_failed = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, pagesCrawled, pagesFailed, id)
	if err != nil {
		return fmt.Errorf("store: updating crawl job %d progress: %w", id, err)
	}
	return nil
}

// SetCrawlJobStatus transitions a crawl job to a new (typically terminal)
// status, recording errMsg if non-empty.
func (s *Store) SetCrawlJobStatus(ctx context.Context, id int64, status CrawlJobStatus, errMsg string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE crawl_jobs SET status = ?, error_message = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, status, errMsg, id)
	if err != nil {
		return fmt.Errorf("store: setting crawl job %d status: %w", id, err)
	}
	return nil
}

// DeleteCrawlJob removes a crawl job's row. It does not touch any
// documents the job ingested.
func (s *Store) DeleteCrawlJob(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM crawl_jobs WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: deleting crawl job %d: %w", id, err)
	}
	return nil
}

// RecoverInterruptedCrawlJobs marks any job still "pending" or "running" as
// failed. Called once at startup: a job in either state when the process
// last exited had its goroutine killed with it (there's no graceful
// shutdown path for background jobs), so its row would otherwise sit
// showing "running" forever with nothing left to finish it. Returns how
// many jobs were recovered.
func (s *Store) RecoverInterruptedCrawlJobs(ctx context.Context) (int, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE crawl_jobs SET status = ?, error_message = ?, updated_at = CURRENT_TIMESTAMP
		WHERE status IN (?, ?)
	`, CrawlJobFailed, "interrupted by server restart", CrawlJobPending, CrawlJobRunning)
	if err != nil {
		return 0, fmt.Errorf("store: recovering interrupted crawl jobs: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}
