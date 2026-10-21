package source

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RagdollGoDown/Searcher/llm"
	"github.com/RagdollGoDown/Searcher/query"
	"github.com/RagdollGoDown/Searcher/store"
)

// ragGoldenCase is one golden-set document/query pair for
// TestRAGSourceRecallAtK: content is deliberately on a distinct topic from
// every other case's, so query should retrieve it above all others.
type ragGoldenCase struct {
	docName string
	content string
	query   string
}

var ragGoldenSet = []ragGoldenCase{
	{
		docName: "eiffel-tower",
		content: "The Eiffel Tower is a wrought-iron lattice tower on the Champ de Mars in Paris, France. It was designed by engineer Gustave Eiffel and completed in 1889 as the entrance arch for the World's Fair.",
		query:   "What famous iron tower is located in Paris?",
	},
	{
		docName: "sourdough",
		content: "Sourdough bread is leavened using a fermented mixture of flour and water, known as a starter, which contains wild yeast and lactobacilli. This natural fermentation gives sourdough its characteristic tangy flavor and chewy crumb.",
		query:   "How do you make bread rise without using commercial yeast?",
	},
	{
		docName: "photosynthesis",
		content: "Photosynthesis is the process by which green plants and some other organisms convert light energy into chemical energy stored in glucose, using carbon dioxide and water, and releasing oxygen as a byproduct.",
		query:   "How do plants turn sunlight into usable energy?",
	},
	{
		docName: "jupiter",
		content: "Jupiter is the largest planet in the Solar System, a gas giant primarily composed of hydrogen and helium, famous for its Great Red Spot, a giant storm that has raged for centuries.",
		query:   "Which planet has a storm called the Great Red Spot?",
	},
	{
		docName: "great-wall",
		content: "The Great Wall of China is a series of fortifications built across the historical northern borders of China to protect against invasions, with construction spanning several dynasties over many centuries.",
		query:   "What ancient wall was built to defend China's northern border?",
	},
}

// TestRAGSourceRecallAtK ingests a small, deliberately distinct-topic
// golden set through RAGSource.Ingest and queries it through RAGSource.Fetch
// — the same code path production chat requests use — asserting recall@1:
// for each query, its own document must be the single closest match. TopK
// is forced to 1 so the assertion is meaningful despite the small corpus
// (with the default TopK, a 5-document corpus would just return everything
// regardless of embedding quality).
//
// Uses the real Ollama embedder rather than llm.FakeEmbedder, since
// retrieval quality is meaningless against non-semantic vectors. Skips if
// Ollama isn't reachable, so `go test ./...` stays green with no setup.
func TestRAGSourceRecallAtK(t *testing.T) {
	ctx := context.Background()

	embedder := llm.NewOllamaEmbedder("nomic-embed-text")
	if err := embedder.Init(ctx); err != nil {
		t.Skipf("skipping: ollama embedder not available: %s", err)
	}

	st, err := store.Open(filepath.Join(t.TempDir(), "rag_quality.db"))
	if err != nil {
		t.Fatalf("opening store: %s", err)
	}
	defer st.Close()

	rag := NewRAGSource(st, embedder, nil, ragDefaultMaxChunkChars, ragDefaultMaxTotalChars)
	if err := rag.SetSettings(ctx, Settings{
		ChunkChars:   store.DefaultChunkChars,
		ChunkOverlap: store.DefaultChunkOverlap,
		TopK:         1,
	}); err != nil {
		t.Fatalf("setting TopK: %s", err)
	}

	for _, c := range ragGoldenSet {
		if _, err := rag.Ingest(ctx, query.Attachment{Name: c.docName, Content: c.content}); err != nil {
			t.Fatalf("ingesting %s: %s", c.docName, err)
		}
	}

	var misses []string
	for _, c := range ragGoldenSet {
		q := query.NewQuery(c.query)
		results, err := rag.Fetch(*q)
		if err != nil {
			t.Fatalf("querying %q: %s", c.query, err)
		}

		found := false
		for _, r := range results {
			if strings.Contains(r.Extract, c.content) {
				found = true
				break
			}
		}
		if !found {
			misses = append(misses, fmt.Sprintf("query %q did not retrieve document %q", c.query, c.docName))
		}
	}

	if len(misses) > 0 {
		t.Errorf("recall@1: %d/%d golden queries missed:\n%s", len(misses), len(ragGoldenSet), strings.Join(misses, "\n"))
	}
}
