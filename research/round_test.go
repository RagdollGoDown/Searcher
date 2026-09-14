package research

import "testing"

func TestParseRoundReply(t *testing.T) {
	tests := []struct {
		name        string
		reply       string
		wantQueries []string
		wantDone    bool
	}{
		{
			name:     "exact DONE",
			reply:    "DONE",
			wantDone: true,
		},
		{
			name:     "lowercase done with surrounding whitespace",
			reply:    "  done  \n",
			wantDone: true,
		},
		{
			name:     "empty response",
			reply:    "",
			wantDone: true,
		},
		{
			name:     "whitespace-only response",
			reply:    "   \n  \n",
			wantDone: true,
		},
		{
			name:     "malformed explanatory text with no convention",
			reply:    "I think I have enough information now, no more searches needed.",
			wantDone: true,
		},
		{
			name:        "single search line",
			reply:       "SEARCH: quantum computing basics",
			wantQueries: []string{"quantum computing basics"},
		},
		{
			name:  "multiple search lines preserve order",
			reply: "SEARCH: first query\nSEARCH: second query\nSEARCH: third query",
			wantQueries: []string{
				"first query", "second query", "third query",
			},
		},
		{
			name:        "case-insensitive prefix and mixed casing",
			reply:       "search:  lowercase prefix\nSeArCh: mixed case prefix",
			wantQueries: []string{"lowercase prefix", "mixed case prefix"},
		},
		{
			name:        "blank search query is dropped",
			reply:       "SEARCH:   \nSEARCH: real query",
			wantQueries: []string{"real query"},
		},
		{
			name:  "premature DONE alongside SEARCH: SEARCH wins",
			reply: "SEARCH: still need this\nDONE",
			wantQueries: []string{
				"still need this",
			},
		},
		{
			name:  "DONE line before SEARCH lines: SEARCH still wins",
			reply: "DONE\nSEARCH: actually need this too",
			wantQueries: []string{
				"actually need this too",
			},
		},
		{
			name:        "search lines mixed with unrelated prose",
			reply:       "Let me look into a couple things.\nSEARCH: relevant query\nThanks.",
			wantQueries: []string{"relevant query"},
		},
		{
			name:        "CRLF line endings",
			reply:       "SEARCH: first\r\nSEARCH: second\r\n",
			wantQueries: []string{"first", "second"},
		},
		{
			name:     "only blank search queries falls back to done",
			reply:    "SEARCH:\nSEARCH:   ",
			wantDone: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseRoundReply(tt.reply)
			if got.Done != tt.wantDone {
				t.Errorf("Done = %v, want %v", got.Done, tt.wantDone)
			}
			if !stringSlicesEqual(got.Queries, tt.wantQueries) {
				t.Errorf("Queries = %#v, want %#v", got.Queries, tt.wantQueries)
			}
		})
	}
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestDedupeQueries(t *testing.T) {
	issued := map[string]bool{}

	first := DedupeQueries([]string{"Quantum Computing", "qubits"}, issued)
	if !stringSlicesEqual(first, []string{"Quantum Computing", "qubits"}) {
		t.Errorf("first round = %#v, want both queries fresh", first)
	}

	// Case-insensitive, trimmed repeat of an already-issued query, plus one
	// genuinely new query — across a simulated second round.
	second := DedupeQueries([]string{"  quantum computing  ", "quantum error correction"}, issued)
	if !stringSlicesEqual(second, []string{"quantum error correction"}) {
		t.Errorf("second round = %#v, want only the new query", second)
	}

	// A duplicate appearing twice within the same round's own list must
	// also be deduped, keeping only the first occurrence.
	issued2 := map[string]bool{}
	within := DedupeQueries([]string{"same query", "SAME QUERY", "same query"}, issued2)
	if !stringSlicesEqual(within, []string{"same query"}) {
		t.Errorf("within-round dedupe = %#v, want single occurrence", within)
	}

	// Blank/whitespace-only queries are dropped, not tracked.
	issued3 := map[string]bool{}
	blanks := DedupeQueries([]string{"", "   "}, issued3)
	if len(blanks) != 0 {
		t.Errorf("blanks = %#v, want none", blanks)
	}
}

func TestBuildRoundPrompt(t *testing.T) {
	got := BuildRoundPrompt("what is quantum computing?", "nothing yet")
	want := "Original question: what is quantum computing?\n" +
		"Found so far: nothing yet\n" +
		"If you need more information, reply with one search per line, each starting\n" +
		"with \"SEARCH: \". Otherwise reply with exactly \"DONE\"."
	if got != want {
		t.Errorf("BuildRoundPrompt() = %q, want %q", got, want)
	}
}
