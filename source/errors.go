package source

import "errors"

// ErrRateLimited is returned by a Source when the upstream service reports
// that too many requests have been made, so callers can match it with
// errors.Is regardless of which source produced it.
var ErrRateLimited = errors.New("source: rate limited")
