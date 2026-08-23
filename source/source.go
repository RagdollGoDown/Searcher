package source

import "searcher/query"

// Result is a single item returned by a Source. Fields that a particular
// source can't populate are left at their zero value.
type Result struct {
	Title    string
	Extract  string
	ImageURL string
}

type Source interface {
	Fetch(query query.Query) ([]Result, error)

	// Icon returns an inline SVG (24x24 viewBox, using stroke="currentColor"
	// so it inherits text color) representing this source, for display
	// next to its name in the chat UI, or "" if it has no icon. Must be
	// trusted, developer-authored markup — never derive it from user input,
	// since callers render it unsanitized.
	Icon() string
}
