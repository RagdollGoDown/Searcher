// Package research implements the iterative search loop behind Searcher's
// research/report-generation feature: given a question, it drives repeated
// rounds of "does the model want to search more, or is it done" against any
// llm.Client, then synthesizes a cited report from what was found.
//
// Round control uses a plain-text "SEARCH: <query>" / "DONE" convention,
// parsed by this package's own Go code, rather than native tool-calling, so
// the same loop works against any llm.Client backend, including ones
// without reliable tool-calling (e.g. phi3:mini via Ollama).
package research

import (
	"fmt"
	"strings"
)

// searchPrefix is matched case-insensitively at the start of a trimmed
// line to identify a search-query line.
const searchPrefix = "search:"

// RoundReply is one round's parsed reply from the model, per the
// SEARCH:/DONE convention.
type RoundReply struct {
	// Queries are the search queries to run this round, in the order they
	// appeared in the reply. Empty (nil) when Done is true.
	Queries []string
	// Done is true if the loop should stop after this round and proceed
	// to final synthesis.
	Done bool
}

// ParseRoundReply parses reply per the round protocol:
//
//   - Any line starting with "SEARCH:" (case-insensitive, after trimming
//     leading whitespace) contributes its remainder, trimmed, as a query.
//     Lines that reduce to an empty query are dropped.
//   - If at least one query was found, Done is false and the loop runs
//     another round — this holds even if the reply also contains a "DONE"
//     line, so SEARCH always wins over DONE in the same reply.
//   - Otherwise (no SEARCH lines — whether the reply is exactly "DONE" or
//     is malformed/unparseable text) Done is true with no queries. A model
//     that never produces a valid SEARCH line fails toward stopping rather
//     than looping forever.
func ParseRoundReply(reply string) RoundReply {
	var queries []string
	for _, line := range strings.Split(strings.ReplaceAll(reply, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if len(line) < len(searchPrefix) || !strings.EqualFold(line[:len(searchPrefix)], searchPrefix) {
			continue
		}
		q := strings.TrimSpace(line[len(searchPrefix):])
		if q != "" {
			queries = append(queries, q)
		}
	}

	if len(queries) > 0 {
		return RoundReply{Queries: queries}
	}
	return RoundReply{Done: true}
}

// DedupeQueries returns the subset of queries not already present
// (case-insensitively, trimmed) in issued, in order, and records each
// returned query in issued. Used by the research loop to skip re-fetching
// a query already issued earlier in the same job — including a repeat
// within one round's own Queries, since issued is updated as it goes.
func DedupeQueries(queries []string, issued map[string]bool) []string {
	var fresh []string
	for _, q := range queries {
		key := strings.ToLower(strings.TrimSpace(q))
		if key == "" || issued[key] {
			continue
		}
		issued[key] = true
		fresh = append(fresh, q)
	}
	return fresh
}

// BuildRoundPrompt renders one round's prompt: the original question plus
// what's been found so far. foundSoFar is caller-supplied, already-formatted
// text (a condensed list of titles/gists, or "nothing yet") — building that
// condensed history from raw results is a separate concern, not this
// function's job.
func BuildRoundPrompt(question, foundSoFar string) string {
	return fmt.Sprintf(
		"Original question: %s\n"+
			"Found so far: %s\n"+
			"If you need more information, reply with one search per line, each starting\n"+
			"with \"SEARCH: \". Otherwise reply with exactly \"DONE\".",
		question, foundSoFar,
	)
}
