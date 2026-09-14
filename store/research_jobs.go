package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// ResearchJobStatus is a research job's lifecycle state.
type ResearchJobStatus string

const (
	ResearchJobPending   ResearchJobStatus = "pending"
	ResearchJobRunning   ResearchJobStatus = "running"
	ResearchJobCompleted ResearchJobStatus = "completed"
	ResearchJobFailed    ResearchJobStatus = "failed"
	ResearchJobCancelled ResearchJobStatus = "cancelled"
)

// ResearchJob is a single research/report-generation job's parameters,
// progress, and (once completed) final report.
type ResearchJob struct {
	ID           int64
	Query        string
	Status       ResearchJobStatus
	RoundCount   int
	Sources      []string
	Backend      string
	Model        string
	ErrorMessage string
	Report       string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// CreateResearchJob inserts a new pending research job and returns its ID.
func (s *Store) CreateResearchJob(ctx context.Context, query string, sources []string, backend, model string) (int64, error) {
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO research_jobs (query, sources, backend, model, status)
		VALUES (?, ?, ?, ?, ?)
	`, query, strings.Join(sources, ","), backend, model, ResearchJobPending)
	if err != nil {
		return 0, fmt.Errorf("store: creating research job: %w", err)
	}
	return res.LastInsertId()
}

// ListResearchJobs returns every research job, newest first.
func (s *Store) ListResearchJobs(ctx context.Context) ([]ResearchJob, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, query, status, round_count, sources, backend, model,
			error_message, report, created_at, updated_at
		FROM research_jobs
		ORDER BY created_at DESC, id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("store: listing research jobs: %w", err)
	}
	defer rows.Close()

	var jobs []ResearchJob
	for rows.Next() {
		j, err := scanResearchJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// GetResearchJob returns a single research job by ID.
func (s *Store) GetResearchJob(ctx context.Context, id int64) (ResearchJob, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, query, status, round_count, sources, backend, model,
			error_message, report, created_at, updated_at
		FROM research_jobs
		WHERE id = ?
	`, id)
	return scanResearchJob(row)
}

// researchJobScanner covers both *sql.Row and *sql.Rows.
type researchJobScanner interface {
	Scan(dest ...any) error
}

func scanResearchJob(row researchJobScanner) (ResearchJob, error) {
	var j ResearchJob
	var sources string
	err := row.Scan(&j.ID, &j.Query, &j.Status, &j.RoundCount, &sources, &j.Backend, &j.Model,
		&j.ErrorMessage, &j.Report, &j.CreatedAt, &j.UpdatedAt)
	if err != nil {
		return ResearchJob{}, fmt.Errorf("store: scanning research job: %w", err)
	}
	if sources != "" {
		j.Sources = strings.Split(sources, ",")
	}
	return j, nil
}

// UpdateResearchJobRounds records how many search rounds a running job has
// completed so far.
func (s *Store) UpdateResearchJobRounds(ctx context.Context, id int64, roundCount int) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE research_jobs SET round_count = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, roundCount, id)
	if err != nil {
		return fmt.Errorf("store: updating research job %d rounds: %w", id, err)
	}
	return nil
}

// SetResearchJobStatus transitions a research job to a new (typically
// terminal) status, recording errMsg if non-empty.
func (s *Store) SetResearchJobStatus(ctx context.Context, id int64, status ResearchJobStatus, errMsg string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE research_jobs SET status = ?, error_message = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, status, errMsg, id)
	if err != nil {
		return fmt.Errorf("store: setting research job %d status: %w", id, err)
	}
	return nil
}

// SaveResearchReport stores a completed job's final report markdown. It
// does not itself change status — callers pair it with SetResearchJobStatus
// to mark the job completed.
func (s *Store) SaveResearchReport(ctx context.Context, id int64, report string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE research_jobs SET report = ?, updated_at = CURRENT_TIMESTAMP
		WHERE id = ?
	`, report, id)
	if err != nil {
		return fmt.Errorf("store: saving research job %d report: %w", id, err)
	}
	return nil
}

// DeleteResearchJob removes a research job's row and (via ON DELETE
// CASCADE) its citations.
func (s *Store) DeleteResearchJob(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM research_jobs WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: deleting research job %d: %w", id, err)
	}
	return nil
}

// RecoverInterruptedResearchJobs marks any job still "pending" or "running"
// as failed. Called once at startup, mirroring
// RecoverInterruptedCrawlJobs: a job in either state when the process last
// exited had its goroutine killed with it, so its row would otherwise sit
// showing "running" forever. Returns how many jobs were recovered.
func (s *Store) RecoverInterruptedResearchJobs(ctx context.Context) (int, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE research_jobs SET status = ?, error_message = ?, updated_at = CURRENT_TIMESTAMP
		WHERE status IN (?, ?)
	`, ResearchJobFailed, "interrupted by server restart", ResearchJobPending, ResearchJobRunning)
	if err != nil {
		return 0, fmt.Errorf("store: recovering interrupted research jobs: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// ResearchCitation is a single source cited by a research job's report.
type ResearchCitation struct {
	ID         int64
	JobID      int64
	Number     int
	SourceName string
	Title      string
	Extract    string
	URL        string
}

// AppendCitation records a citation for jobID and returns the citation
// number to use for it in the report. Citations are deduped per job by URL
// (or by source name + title when URL is empty, e.g. a Wikipedia result
// without a stable link): a result already cited in this job reuses its
// existing number instead of getting a new one, so a page surfaced by two
// different search rounds is cited once, not twice. Callers issue
// citations for one job sequentially (the research loop runs one job at a
// time, like Crawler.run), so this isn't safe for concurrent callers on the
// same job.
func (s *Store) AppendCitation(ctx context.Context, jobID int64, sourceName, title, extract, url string) (int, error) {
	var existing int
	var err error
	if url != "" {
		err = s.db.QueryRowContext(ctx, `
			SELECT number FROM research_citations WHERE job_id = ? AND url = ?
		`, jobID, url).Scan(&existing)
	} else {
		err = s.db.QueryRowContext(ctx, `
			SELECT number FROM research_citations
			WHERE job_id = ? AND url = '' AND source_name = ? AND title = ?
		`, jobID, sourceName, title).Scan(&existing)
	}
	if err == nil {
		return existing, nil
	}
	if err != sql.ErrNoRows {
		return 0, fmt.Errorf("store: checking existing citation for job %d: %w", jobID, err)
	}

	var maxNumber sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `
		SELECT MAX(number) FROM research_citations WHERE job_id = ?
	`, jobID).Scan(&maxNumber); err != nil {
		return 0, fmt.Errorf("store: finding next citation number for job %d: %w", jobID, err)
	}
	next := int(maxNumber.Int64) + 1

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO research_citations (job_id, number, source_name, title, extract, url)
		VALUES (?, ?, ?, ?, ?, ?)
	`, jobID, next, sourceName, title, extract, url)
	if err != nil {
		return 0, fmt.Errorf("store: appending citation for job %d: %w", jobID, err)
	}
	return next, nil
}

// ListCitations returns every citation recorded for jobID, ordered by
// citation number.
func (s *Store) ListCitations(ctx context.Context, jobID int64) ([]ResearchCitation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, job_id, number, source_name, title, extract, url
		FROM research_citations
		WHERE job_id = ?
		ORDER BY number ASC
	`, jobID)
	if err != nil {
		return nil, fmt.Errorf("store: listing citations for job %d: %w", jobID, err)
	}
	defer rows.Close()

	var citations []ResearchCitation
	for rows.Next() {
		var c ResearchCitation
		if err := rows.Scan(&c.ID, &c.JobID, &c.Number, &c.SourceName, &c.Title, &c.Extract, &c.URL); err != nil {
			return nil, fmt.Errorf("store: scanning citation: %w", err)
		}
		citations = append(citations, c)
	}
	return citations, rows.Err()
}
