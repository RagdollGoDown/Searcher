package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/RagdollGoDown/Searcher/llm"
	"github.com/RagdollGoDown/Searcher/store"
)

// namedClient replies with its own name, so a test can tell which
// provider answered a chat request.
type namedClient string

func (c namedClient) Init(ctx context.Context) error { return nil }

func (c namedClient) Chat(ctx context.Context, messages []llm.Message) (string, error) {
	return string(c), nil
}

func newProviderTestServer(t *testing.T, st *store.Store, anthropicUnavailable string) *httptest.Server {
	t.Helper()
	providers := []Provider{
		{Name: "ollama", Model: "phi3:mini", Client: namedClient("ollama")},
		{Name: "anthropic", Model: "claude-sonnet-5-5", Client: namedClient("anthropic"), Unavailable: anthropicUnavailable},
	}
	handler, err := NewServer(providers, "ollama", nil, nil, nil, st).Handler()
	if err != nil {
		t.Fatalf("building handler: %s", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("opening store: %s", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func putProvider(t *testing.T, srv *httptest.Server, name string) (int, chatSettingsBody) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/api/chat/settings", strings.NewReader(`{"provider":"`+name+`"}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /api/chat/settings: %s", err)
	}
	defer resp.Body.Close()
	var body chatSettingsBody
	json.NewDecoder(resp.Body).Decode(&body)
	return resp.StatusCode, body
}

func chatReply(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	resp, err := http.Post(srv.URL+"/api/chat", "application/json", strings.NewReader(`{"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("POST /api/chat: %s", err)
	}
	defer resp.Body.Close()
	var body chatResponseBody
	json.NewDecoder(resp.Body).Decode(&body)
	if body.Error != "" {
		t.Fatalf("chat error: %s", body.Error)
	}
	return body.Reply
}

func TestSwitchProvider(t *testing.T) {
	srv := newProviderTestServer(t, openTestStore(t), "")

	if got := chatReply(t, srv); got != "ollama" {
		t.Errorf("before switching, reply came from %q, want the default ollama", got)
	}
	if status, body := putProvider(t, srv, "anthropic"); status != http.StatusOK || body.Provider != "anthropic" {
		t.Fatalf("switching to anthropic: status %d, body %+v", status, body)
	}
	if got := chatReply(t, srv); got != "anthropic" {
		t.Errorf("after switching, reply came from %q, want anthropic", got)
	}
}

func TestAttachmentsAreIncludedAutomatically(t *testing.T) {
	srv := newProviderTestServer(t, openTestStore(t), "")
	body := `{"messages":[{"role":"user","content":"summarize this"}],"files":[{"name":"note.txt","content":"attached text"}]}`
	resp, err := http.Post(srv.URL+"/api/chat", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /api/chat: %s", err)
	}
	defer resp.Body.Close()

	var reply chatResponseBody
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		t.Fatalf("decoding chat response: %s", err)
	}
	if reply.Error != "" {
		t.Fatalf("chat error: %s", reply.Error)
	}
	if len(reply.Sources) != 1 || reply.Sources[0].Source != "Attached files" {
		t.Fatalf("sources = %+v, want one Attached files result", reply.Sources)
	}
	if len(reply.Sources[0].Results) != 1 || reply.Sources[0].Results[0].Extract != "attached text" {
		t.Fatalf("attached results = %+v, want note.txt with its content", reply.Sources[0].Results)
	}
}

func TestRejectsUnknownOrUnavailableProvider(t *testing.T) {
	srv := newProviderTestServer(t, openTestStore(t), "ANTHROPIC_API_KEY is not set")

	for _, name := range []string{"nope", "anthropic"} {
		if status, _ := putProvider(t, srv, name); status != http.StatusBadRequest {
			t.Errorf("PUT %q: status %d, want 400", name, status)
		}
	}
	if got := chatReply(t, srv); got != "ollama" {
		t.Errorf("reply came from %q, want ollama after rejected switches", got)
	}
}

// A provider saved before a restart that left it unavailable (e.g. the API
// key was removed) must fall back to the default rather than fail every chat.
func TestSavedProviderFallsBackWhenUnavailable(t *testing.T) {
	st := openTestStore(t)
	if status, _ := putProvider(t, newProviderTestServer(t, st, ""), "anthropic"); status != http.StatusOK {
		t.Fatalf("switching to anthropic: status %d", status)
	}

	srv := newProviderTestServer(t, st, "ANTHROPIC_API_KEY is not set")
	if got := chatReply(t, srv); got != "ollama" {
		t.Errorf("reply came from %q, want fallback to ollama", got)
	}
}
