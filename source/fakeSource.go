package source

import "searcher/query"

type FakeSource struct {
}

func NewFakeSource() *FakeSource {
	return &FakeSource{}
}

// Compile-time check that FakeSource satisfies Source.
var _ Source = (*FakeSource)(nil)

func (s *FakeSource) Icon() string {
	return ""
}

func (s *FakeSource) Fetch(q query.Query) ([]Result, error) {
	return []Result{
		{
			Title:    "Fake Result 1",
			Extract:  "This is a fake result for testing purposes.",
			ImageURL: "https://example.com/fake1",
		},
		{
			Title:    "Fake Result 2",
			Extract:  "This is another fake result for testing purposes.",
			ImageURL: "https://example.com/fake2",
		},
	}, nil
}
