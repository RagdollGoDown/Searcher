package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/RagdollGoDown/Searcher/llm"
)

// settingChatProvider persists the provider picked on the settings page,
// so the choice survives restarts instead of reverting to -provider.
const settingChatProvider = "chat.provider"

// Provider is a chat backend the settings page can switch between at
// runtime.
type Provider struct {
	Name   string
	Model  string
	Client llm.Client
	// Unavailable, if non-empty, is why this provider can't be selected
	// (e.g. a missing API key). It's still listed so the settings page can
	// tell the user what enabling it takes, rather than silently hiding it.
	Unavailable string
}

func (s *Server) provider(name string) (Provider, bool) {
	for _, p := range s.providers {
		if p.Name == name {
			return p, true
		}
	}
	return Provider{}, false
}

// activeProvider returns the saved provider, falling back to the default
// when nothing is saved or the saved one has since become unavailable
// (e.g. the app was restarted without its API key).
func (s *Server) activeProvider(ctx context.Context) (Provider, error) {
	if s.store != nil {
		name, ok, err := s.store.GetSetting(ctx, settingChatProvider)
		if err != nil {
			return Provider{}, err
		}
		if p, found := s.provider(name); ok && found && p.Unavailable == "" {
			return p, nil
		}
	}
	p, _ := s.provider(s.defaultProvider)
	return p, nil
}

type providerInfo struct {
	Name        string `json:"name"`
	Model       string `json:"model"`
	Unavailable string `json:"unavailable,omitempty"`
}

type chatSettingsBody struct {
	Provider  string         `json:"provider,omitempty"`
	Providers []providerInfo `json:"providers,omitempty"`
	Error     string         `json:"error,omitempty"`
}

func (s *Server) chatSettings(ctx context.Context) (chatSettingsBody, error) {
	active, err := s.activeProvider(ctx)
	if err != nil {
		return chatSettingsBody{}, err
	}
	infos := make([]providerInfo, len(s.providers))
	for i, p := range s.providers {
		infos[i] = providerInfo{Name: p.Name, Model: p.Model, Unavailable: p.Unavailable}
	}
	return chatSettingsBody{Provider: active.Name, Providers: infos}, nil
}

func (s *Server) handleGetChatSettings(w http.ResponseWriter, r *http.Request) {
	body, err := s.chatSettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, chatSettingsBody{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handlePutChatSettings(w http.ResponseWriter, r *http.Request) {
	var req chatSettingsBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, chatSettingsBody{Error: "invalid request body"})
		return
	}

	p, ok := s.provider(req.Provider)
	if !ok {
		writeJSON(w, http.StatusBadRequest, chatSettingsBody{Error: fmt.Sprintf("unknown provider %q", req.Provider)})
		return
	}
	if p.Unavailable != "" {
		writeJSON(w, http.StatusBadRequest, chatSettingsBody{Error: p.Unavailable})
		return
	}
	if err := s.store.SetSetting(r.Context(), settingChatProvider, p.Name); err != nil {
		writeJSON(w, http.StatusInternalServerError, chatSettingsBody{Error: err.Error()})
		return
	}
	s.handleGetChatSettings(w, r)
}
