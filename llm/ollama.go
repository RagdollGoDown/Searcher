package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"slices"
	"time"
)

const ollamaDefaultBaseURL = "http://localhost:11434"

// ollamaRequestTimeout bounds how long a single chat request may take,
// including connection setup, so a silent network failure (e.g. a
// firewall dropping packets instead of refusing the connection) fails
// loudly instead of hanging forever.
const ollamaRequestTimeout = 60 * time.Second

// ollamaPingTimeout bounds a single "is the server up" check.
const ollamaPingTimeout = 2 * time.Second

// ollamaStartupTimeout bounds how long Init waits for a freshly launched
// `ollama serve` to start accepting connections.
const ollamaStartupTimeout = 30 * time.Second

// ollamaPollInterval is how often Init re-checks the server while waiting
// for it to come up.
const ollamaPollInterval = 500 * time.Millisecond

// ollamaServer holds the connection details for talking to a local Ollama
// server, and the logic to make sure it's running. Embedded by both
// OllamaClient and OllamaEmbedder, which otherwise differ only in which
// endpoint and model they use.
type ollamaServer struct {
	baseURL string
	http    *http.Client
}

// newOllamaServer resolves the server address, defaulting to
// http://localhost:11434 or the OLLAMA_HOST environment variable if set.
func newOllamaServer() ollamaServer {
	baseURL := ollamaDefaultBaseURL
	if host := os.Getenv("OLLAMA_HOST"); host != "" {
		baseURL = host
	}
	return ollamaServer{baseURL: baseURL, http: &http.Client{Timeout: ollamaRequestTimeout}}
}

// Init checks whether the Ollama server is reachable and, if not, launches
// `ollama serve` and waits for it to become ready. It's safe to call
// multiple times, including from multiple ollamaServer values pointed at
// the same address (e.g. a chat client and an embedder); if the server is
// already running (including via the desktop app's own auto-started
// server) it returns immediately.
func (o *ollamaServer) Init(ctx context.Context) error {
	if o.isRunning(ctx) {
		return nil
	}

	fmt.Println("Ollama server not detected, starting it...")

	cmd := exec.Command("ollama", "serve")
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ollama: starting server: %w", err)
	}

	deadline := time.Now().Add(ollamaStartupTimeout)
	for time.Now().Before(deadline) {
		if o.isRunning(ctx) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(ollamaPollInterval):
		}
	}

	return fmt.Errorf("ollama: server did not become ready within %s", ollamaStartupTimeout)
}

// isRunning reports whether the Ollama server is currently accepting
// requests.
func (o *ollamaServer) isRunning(ctx context.Context) bool {
	pingCtx, cancel := context.WithTimeout(ctx, ollamaPingTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(pingCtx, http.MethodGet, o.baseURL+"/", nil)
	if err != nil {
		return false
	}

	resp, err := o.http.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// OllamaClient talks to a locally (or remotely) hosted Ollama server's
// /api/chat endpoint.
type OllamaClient struct {
	ollamaServer
	model string
}

// NewOllamaClient creates a client for the given model. The server address
// defaults to http://localhost:11434, or the OLLAMA_HOST environment
// variable if set.
func NewOllamaClient(model string) *OllamaClient {
	fmt.Println("Starting ollama client")
	return &OllamaClient{ollamaServer: newOllamaServer(), model: model}
}

// NewOllamaClientWithBaseURL is like NewOllamaClient but lets the caller
// specify the server address explicitly, ignoring OLLAMA_HOST.
func NewOllamaClientWithBaseURL(baseURL, model string) *OllamaClient {
	c := NewOllamaClient(model)
	c.baseURL = baseURL
	return c
}

// Compile-time check that OllamaClient satisfies Client.
var _ Client = (*OllamaClient)(nil)

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
}

type ollamaChatResponse struct {
	Message ollamaMessage `json:"message"`
	Done    bool          `json:"done"`
}

type ollamaTagsResponse struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

// Models lists the models pulled onto the Ollama server that can chat,
// leaving out embedding-only models, which the embedder owns.
func (c *OllamaClient) Models(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, fmt.Errorf("ollama: building request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama: unexpected status %d", resp.StatusCode)
	}

	var tags ollamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, fmt.Errorf("ollama: decoding response: %w", err)
	}

	names := make([]string, 0, len(tags.Models))
	for _, m := range tags.Models {
		if c.canChat(ctx, m.Name) {
			names = append(names, m.Name)
		}
	}
	return names, nil
}

// canChat reports whether model supports chat completion. /api/tags
// doesn't say, so this asks /api/show. Anything it can't determine (older
// servers don't report capabilities) counts as chat-capable, so a model
// is hidden only when the server positively says it can't chat.
func (c *OllamaClient) canChat(ctx context.Context, model string) bool {
	body, err := json.Marshal(map[string]string{"model": model})
	if err != nil {
		return true
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/show", bytes.NewReader(body))
	if err != nil {
		return true
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return true
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return true
	}

	var show struct {
		Capabilities []string `json:"capabilities"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&show); err != nil || len(show.Capabilities) == 0 {
		return true
	}
	return slices.Contains(show.Capabilities, "completion")
}

func (c *OllamaClient) Chat(ctx context.Context, model string, messages []Message) (string, error) {
	if model == "" {
		model = c.model
	}

	fmt.Println("chatting with ollama client")

	reqMessages := make([]ollamaMessage, len(messages))
	for i, m := range messages {
		reqMessages[i] = ollamaMessage{Role: string(m.Role), Content: m.Content}
	}

	body, err := json.Marshal(ollamaChatRequest{
		Model:    model,
		Messages: reqMessages,
		Stream:   false,
	})
	if err != nil {
		return "", fmt.Errorf("ollama: encoding request: %w", err)
	}

	fmt.Println("Sending request to Ollama server...")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("ollama: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama: unexpected status %d", resp.StatusCode)
	}

	fmt.Println("Received response from Ollama server")

	var chatResp ollamaChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("ollama: decoding response: %w", err)
	}

	return chatResp.Message.Content, nil
}

// OllamaEmbedder talks to a locally (or remotely) hosted Ollama server's
// /api/embed endpoint.
type OllamaEmbedder struct {
	ollamaServer
	model string
}

// NewOllamaEmbedder creates an embedder for the given model (e.g.
// "nomic-embed-text"). The server address defaults to
// http://localhost:11434, or the OLLAMA_HOST environment variable if set.
func NewOllamaEmbedder(model string) *OllamaEmbedder {
	return &OllamaEmbedder{ollamaServer: newOllamaServer(), model: model}
}

// Compile-time check that OllamaEmbedder satisfies Embedder.
var _ Embedder = (*OllamaEmbedder)(nil)

type ollamaEmbedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
}

func (e *OllamaEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	body, err := json.Marshal(ollamaEmbedRequest{Model: e.model, Input: text})
	if err != nil {
		return nil, fmt.Errorf("ollama: encoding embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("ollama: building embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := e.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama: embed request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama: unexpected status %d", resp.StatusCode)
	}

	var embedResp ollamaEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&embedResp); err != nil {
		return nil, fmt.Errorf("ollama: decoding embed response: %w", err)
	}
	if len(embedResp.Embeddings) == 0 {
		return nil, fmt.Errorf("ollama: embed response contained no embeddings")
	}

	return embedResp.Embeddings[0], nil
}
