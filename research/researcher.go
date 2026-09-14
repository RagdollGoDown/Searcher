package research

import "searcher/store"

// Researcher runs research/report-generation jobs as background jobs,
// mirroring scraper.Crawler's shape: job state (status/progress/report)
// lives in store.Store's research_jobs table so it survives a restart,
// this struct holds what a running job needs beyond that.
type Researcher struct {
	store *store.Store
}

// NewResearcher creates a Researcher backed by st.
func NewResearcher(st *store.Store) *Researcher {
	return &Researcher{store: st}
}
