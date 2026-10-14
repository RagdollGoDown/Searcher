package research

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/RagdollGoDown/Searcher/llm"
	"github.com/RagdollGoDown/Searcher/store"
)

func openTestResearcher(t *testing.T) *Researcher {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "research.db"))
	if err != nil {
		t.Fatalf("opening store: %s", err)
	}
	t.Cleanup(func() { st.Close() })
	return NewResearcher(st)
}

func TestSettingsDefaults(t *testing.T) {
	r := openTestResearcher(t)

	got, err := r.GetSettings(context.Background())
	if err != nil {
		t.Fatalf("getting settings: %s", err)
	}
	want := Settings{Backend: researchDefaultBackend, Model: researchDefaultModel}
	if got != want {
		t.Errorf("default settings = %+v, want %+v", got, want)
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	r := openTestResearcher(t)
	ctx := context.Background()

	set := Settings{Backend: BackendAnthropic, Model: "claude-sonnet-5"}
	if err := r.SetSettings(ctx, set); err != nil {
		t.Fatalf("setting settings: %s", err)
	}

	got, err := r.GetSettings(ctx)
	if err != nil {
		t.Fatalf("getting settings: %s", err)
	}
	if got != set {
		t.Errorf("settings after round trip = %+v, want %+v", got, set)
	}
}

func TestSettingsRejectsInvalidInput(t *testing.T) {
	r := openTestResearcher(t)
	ctx := context.Background()

	if err := r.SetSettings(ctx, Settings{Backend: "not-a-real-backend", Model: "phi3:mini"}); err == nil {
		t.Error("expected error for unknown backend, got nil")
	}
	if err := r.SetSettings(ctx, Settings{Backend: BackendOllama, Model: ""}); err == nil {
		t.Error("expected error for empty model, got nil")
	}

	// A rejected SetSettings call must not have partially persisted —
	// defaults should still be reported.
	got, err := r.GetSettings(ctx)
	if err != nil {
		t.Fatalf("getting settings after rejected writes: %s", err)
	}
	want := Settings{Backend: researchDefaultBackend, Model: researchDefaultModel}
	if got != want {
		t.Errorf("settings after rejected writes = %+v, want unchanged defaults %+v", got, want)
	}
}

func TestNewClient(t *testing.T) {
	if _, err := NewClient("not-a-real-backend", "some-model"); err == nil {
		t.Error("expected error for unknown backend, got nil")
	}

	ollamaClient, err := NewClient(BackendOllama, "phi3:mini")
	if err != nil {
		t.Fatalf("creating ollama client: %s", err)
	}
	if _, ok := ollamaClient.(*llm.OllamaClient); !ok {
		t.Errorf("NewClient(%q, ...) = %T, want *llm.OllamaClient", BackendOllama, ollamaClient)
	}

	anthropicClient, err := NewClient(BackendAnthropic, "claude-sonnet-5")
	if err != nil {
		t.Fatalf("creating anthropic client: %s", err)
	}
	if _, ok := anthropicClient.(*llm.AnthropicClient); !ok {
		t.Errorf("NewClient(%q, ...) = %T, want *llm.AnthropicClient", BackendAnthropic, anthropicClient)
	}
}
