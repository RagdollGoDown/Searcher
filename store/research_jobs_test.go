package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "research.db"))
	if err != nil {
		t.Fatalf("opening store: %s", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// TestResearchJobLifecycle exercises the CRUD + status-transition path a
// research loop will drive: create, progress updates, completion with a
// saved report, and the startup-recovery sweep for interrupted jobs.
func TestResearchJobLifecycle(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	sources := []string{"Wikipedia", "My Documents"}
	id, err := st.CreateResearchJob(ctx, "what is quantum computing?", sources, "ollama", "phi3:mini")
	if err != nil {
		t.Fatalf("creating research job: %s", err)
	}

	job, err := st.GetResearchJob(ctx, id)
	if err != nil {
		t.Fatalf("getting research job: %s", err)
	}
	if job.Status != ResearchJobPending {
		t.Errorf("new job status = %q, want %q", job.Status, ResearchJobPending)
	}
	if !reflect.DeepEqual(job.Sources, sources) {
		t.Errorf("job.Sources = %v, want %v", job.Sources, sources)
	}
	if job.Backend != "ollama" || job.Model != "phi3:mini" {
		t.Errorf("job backend/model = %q/%q, want ollama/phi3:mini", job.Backend, job.Model)
	}

	if err := st.SetResearchJobStatus(ctx, id, ResearchJobRunning, ""); err != nil {
		t.Fatalf("setting status running: %s", err)
	}
	if err := st.UpdateResearchJobRounds(ctx, id, 3); err != nil {
		t.Fatalf("updating rounds: %s", err)
	}
	if err := st.SaveResearchReport(ctx, id, "# Report\n\nFindings here [1]."); err != nil {
		t.Fatalf("saving report: %s", err)
	}
	if err := st.SetResearchJobStatus(ctx, id, ResearchJobCompleted, ""); err != nil {
		t.Fatalf("setting status completed: %s", err)
	}

	job, err = st.GetResearchJob(ctx, id)
	if err != nil {
		t.Fatalf("getting research job after completion: %s", err)
	}
	if job.Status != ResearchJobCompleted {
		t.Errorf("job status = %q, want %q", job.Status, ResearchJobCompleted)
	}
	if job.RoundCount != 3 {
		t.Errorf("job.RoundCount = %d, want 3", job.RoundCount)
	}
	if job.Report != "# Report\n\nFindings here [1]." {
		t.Errorf("job.Report = %q, unexpected", job.Report)
	}

	failedID, err := st.CreateResearchJob(ctx, "another question", nil, "ollama", "phi3:mini")
	if err != nil {
		t.Fatalf("creating second research job: %s", err)
	}
	if err := st.SetResearchJobStatus(ctx, failedID, ResearchJobRunning, ""); err != nil {
		t.Fatalf("setting second job running: %s", err)
	}

	jobs, err := st.ListResearchJobs(ctx)
	if err != nil {
		t.Fatalf("listing research jobs: %s", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("ListResearchJobs returned %d jobs, want 2", len(jobs))
	}
	if jobs[0].ID != failedID {
		t.Errorf("ListResearchJobs not newest-first: got ID %d first, want %d", jobs[0].ID, failedID)
	}

	// Simulate a restart: the "running" job should be recovered as failed,
	// but the already-completed job must be left alone.
	n, err := st.RecoverInterruptedResearchJobs(ctx)
	if err != nil {
		t.Fatalf("recovering interrupted research jobs: %s", err)
	}
	if n != 1 {
		t.Errorf("RecoverInterruptedResearchJobs recovered %d jobs, want 1", n)
	}

	recovered, err := st.GetResearchJob(ctx, failedID)
	if err != nil {
		t.Fatalf("getting recovered job: %s", err)
	}
	if recovered.Status != ResearchJobFailed {
		t.Errorf("recovered job status = %q, want %q", recovered.Status, ResearchJobFailed)
	}
	if recovered.ErrorMessage == "" {
		t.Error("recovered job has no error message")
	}

	completed, err := st.GetResearchJob(ctx, id)
	if err != nil {
		t.Fatalf("getting completed job: %s", err)
	}
	if completed.Status != ResearchJobCompleted {
		t.Errorf("completed job status changed to %q after recovery sweep, want unchanged %q", completed.Status, ResearchJobCompleted)
	}

	if err := st.DeleteResearchJob(ctx, id); err != nil {
		t.Fatalf("deleting research job: %s", err)
	}
	if _, err := st.GetResearchJob(ctx, id); err == nil {
		t.Error("expected error getting deleted research job, got nil")
	}
}

// TestAppendCitationDedupes checks the citation-numbering rule the research
// loop depends on: a result already cited in a job reuses its number
// instead of getting a new one, so a page two different SEARCH rounds both
// surface is cited once, not twice. Dedupe key is URL when present, else
// source name + title.
func TestAppendCitationDedupes(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	jobID, err := st.CreateResearchJob(ctx, "q", nil, "ollama", "phi3:mini")
	if err != nil {
		t.Fatalf("creating research job: %s", err)
	}

	n1, err := st.AppendCitation(ctx, jobID, "Wikipedia", "Quantum computing", "extract A", "https://en.wikipedia.org/wiki/Quantum_computing")
	if err != nil {
		t.Fatalf("appending first citation: %s", err)
	}
	if n1 != 1 {
		t.Errorf("first citation number = %d, want 1", n1)
	}

	// Same URL again, from a different round's search results (different
	// extract text) — must dedupe to the same number, not mint a new one.
	n2, err := st.AppendCitation(ctx, jobID, "Wikipedia", "Quantum computing", "extract A again", "https://en.wikipedia.org/wiki/Quantum_computing")
	if err != nil {
		t.Fatalf("appending duplicate-URL citation: %s", err)
	}
	if n2 != n1 {
		t.Errorf("duplicate-URL citation number = %d, want %d (deduped)", n2, n1)
	}

	// Different URL — new citation.
	n3, err := st.AppendCitation(ctx, jobID, "Wikipedia", "Qubit", "extract B", "https://en.wikipedia.org/wiki/Qubit")
	if err != nil {
		t.Fatalf("appending second citation: %s", err)
	}
	if n3 != 2 {
		t.Errorf("second citation number = %d, want 2", n3)
	}

	// No URL (e.g. a RAG chunk with no stable link) — dedupe falls back to
	// source name + title.
	n4, err := st.AppendCitation(ctx, jobID, "My Documents", "notes.txt", "chunk 1", "")
	if err != nil {
		t.Fatalf("appending no-URL citation: %s", err)
	}
	if n4 != 3 {
		t.Errorf("no-URL citation number = %d, want 3", n4)
	}
	n5, err := st.AppendCitation(ctx, jobID, "My Documents", "notes.txt", "chunk 2", "")
	if err != nil {
		t.Fatalf("appending duplicate no-URL citation: %s", err)
	}
	if n5 != n4 {
		t.Errorf("duplicate no-URL citation number = %d, want %d (deduped)", n5, n4)
	}

	citations, err := st.ListCitations(ctx, jobID)
	if err != nil {
		t.Fatalf("listing citations: %s", err)
	}
	if len(citations) != 3 {
		t.Fatalf("ListCitations returned %d citations, want 3", len(citations))
	}
	for i, c := range citations {
		if c.Number != i+1 {
			t.Errorf("citations[%d].Number = %d, want %d (ordered by number)", i, c.Number, i+1)
		}
	}

	// A second job's citations must not collide with the first job's
	// numbering — each job starts back at 1.
	otherJobID, err := st.CreateResearchJob(ctx, "other q", nil, "ollama", "phi3:mini")
	if err != nil {
		t.Fatalf("creating second research job: %s", err)
	}
	nOther, err := st.AppendCitation(ctx, otherJobID, "Wikipedia", "Quantum computing", "extract A", "https://en.wikipedia.org/wiki/Quantum_computing")
	if err != nil {
		t.Fatalf("appending citation to second job: %s", err)
	}
	if nOther != 1 {
		t.Errorf("first citation number in second job = %d, want 1", nOther)
	}
}

// TestDeleteResearchJobCascadesCitations checks that deleting a job also
// removes its citations, via research_citations' ON DELETE CASCADE FK.
func TestDeleteResearchJobCascadesCitations(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	jobID, err := st.CreateResearchJob(ctx, "q", nil, "ollama", "phi3:mini")
	if err != nil {
		t.Fatalf("creating research job: %s", err)
	}
	if _, err := st.AppendCitation(ctx, jobID, "Wikipedia", "Title", "extract", "https://example.invalid/a"); err != nil {
		t.Fatalf("appending citation: %s", err)
	}

	if err := st.DeleteResearchJob(ctx, jobID); err != nil {
		t.Fatalf("deleting research job: %s", err)
	}

	citations, err := st.ListCitations(ctx, jobID)
	if err != nil {
		t.Fatalf("listing citations after job delete: %s", err)
	}
	if len(citations) != 0 {
		t.Errorf("ListCitations after job delete returned %d citations, want 0", len(citations))
	}
}
