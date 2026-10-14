package research

import (
	"context"
	"fmt"

	"github.com/RagdollGoDown/Searcher/llm"
)

// Settings keys for the research loop's persisted, per-feature
// configuration, stored in store.Store's generic settings table alongside
// e.g. RAG's chunking settings and the scraper's crawl defaults.
const (
	settingDefaultBackend = "research.default_backend"
	settingDefaultModel   = "research.default_model"
)

// Backend identifies which llm.Client implementation a research job uses.
// Dual backend support (both must work) is a hard product requirement, so
// this is a closed set of two, not an open string.
const (
	BackendOllama    = "ollama"
	BackendAnthropic = "anthropic"
)

const (
	researchDefaultBackend = BackendOllama
	researchDefaultModel   = "phi3:mini"
)

// Settings are the default backend/model applied to a new research job
// when its request doesn't specify them.
type Settings struct {
	Backend string
	Model   string
}

// GetSettings returns the research loop's currently configured default
// backend/model, falling back to the package defaults for whichever
// hasn't been explicitly set.
func (r *Researcher) GetSettings(ctx context.Context) (Settings, error) {
	backend, ok, err := r.store.GetSetting(ctx, settingDefaultBackend)
	if err != nil {
		return Settings{}, err
	}
	if !ok {
		backend = researchDefaultBackend
	}
	model, ok, err := r.store.GetSetting(ctx, settingDefaultModel)
	if err != nil {
		return Settings{}, err
	}
	if !ok {
		model = researchDefaultModel
	}
	return Settings{Backend: backend, Model: model}, nil
}

// SetSettings persists s as the defaults applied to research jobs started
// from now on.
func (r *Researcher) SetSettings(ctx context.Context, s Settings) error {
	if s.Backend != BackendOllama && s.Backend != BackendAnthropic {
		return fmt.Errorf("research: unknown backend %q", s.Backend)
	}
	if s.Model == "" {
		return fmt.Errorf("research: model must not be empty")
	}
	if err := r.store.SetSetting(ctx, settingDefaultBackend, s.Backend); err != nil {
		return err
	}
	return r.store.SetSetting(ctx, settingDefaultModel, s.Model)
}

// NewClient returns the llm.Client to use for the given backend/model —
// e.g. the values recorded on a research_jobs row, or a Settings default.
// It's a factory the research loop calls once per job, not itself an
// llm.Client.
func NewClient(backend, model string) (llm.Client, error) {
	switch backend {
	case BackendOllama:
		return llm.NewOllamaClient(model), nil
	case BackendAnthropic:
		return llm.NewAnthropicClient(model), nil
	default:
		return nil, fmt.Errorf("research: unknown backend %q", backend)
	}
}
