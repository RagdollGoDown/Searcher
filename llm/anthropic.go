package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const anthropicAPIURL = "https://api.anthropic.com/v1/messages"
const anthropicAPIVersion = "2023-06-01"

// anthropicRequestTimeout bounds how long a single chat request may take,
// mirroring ollamaRequestTimeout's purpose for OllamaClient.
const anthropicRequestTimeout = 60 * time.Second

// anthropicMaxTokens bounds a single chat response. This is a
// non-streaming request, so it stays under the API's HTTP timeout rather
// than reaching for the full 128K some current models support.
const anthropicMaxTokens = 16000

// anthropicMaxErrorBodyBytes caps how much of a non-200 response body is
// read into the returned error, so a misbehaving proxy can't inflate it.
const anthropicMaxErrorBodyBytes = 4096

// AnthropicClient talks to the Anthropic Messages API directly over HTTP,
// the same way OllamaClient talks to Ollama's API — no SDK dependency, to
// keep this self-hosted app's footprint small.
type AnthropicClient struct {
	apiKey string
	model  string
	http   *http.Client
}

// NewAnthropicClient creates a client for the given model (e.g.
// "claude-sonnet-5"), reading the API key from the ANTHROPIC_API_KEY
// environment variable.
func NewAnthropicClient(model string) *AnthropicClient {
	return &AnthropicClient{
		apiKey: os.Getenv("ANTHROPIC_API_KEY"),
		model:  model,
		http:   &http.Client{Timeout: anthropicRequestTimeout},
	}
}

// Compile-time check that AnthropicClient satisfies Client.
var _ Client = (*AnthropicClient)(nil)

// Init checks that an API key is configured. Unlike OllamaClient there's
// no local process to start, so this is the only up-front check
// available — an invalid (as opposed to missing) key still only surfaces
// from Chat.
func (c *AnthropicClient) Init(ctx context.Context) error {
	if c.apiKey == "" {
		return fmt.Errorf("anthropic: ANTHROPIC_API_KEY is not set")
	}
	return nil
}

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicChatRequest struct {
	Model     string              `json:"model"`
	MaxTokens int                 `json:"max_tokens"`
	System    string              `json:"system,omitempty"`
	Messages  []anthropicMessage  `json:"messages"`
}

type anthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicChatResponse struct {
	Content []anthropicContentBlock `json:"content"`
}

func (c *AnthropicClient) Chat(ctx context.Context, messages []Message) (string, error) {
	// The Messages API takes "system" as a separate top-level field, not
	// a message role, unlike Ollama's /api/chat — so system-role messages
	// are pulled out of the slice and joined instead of passed through.
	var system []string
	reqMessages := make([]anthropicMessage, 0, len(messages))
	for _, m := range messages {
		if m.Role == RoleSystem {
			system = append(system, m.Content)
			continue
		}
		reqMessages = append(reqMessages, anthropicMessage{Role: string(m.Role), Content: m.Content})
	}

	body, err := json.Marshal(anthropicChatRequest{
		Model:     c.model,
		MaxTokens: anthropicMaxTokens,
		System:    strings.Join(system, "\n\n"),
		Messages:  reqMessages,
	})
	if err != nil {
		return "", fmt.Errorf("anthropic: encoding request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicAPIURL, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("anthropic: building request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", anthropicAPIVersion)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("anthropic: request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, anthropicMaxErrorBodyBytes))
		return "", fmt.Errorf("anthropic: unexpected status %d: %s", resp.StatusCode, errBody)
	}

	var chatResp anthropicChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("anthropic: decoding response: %w", err)
	}

	var text strings.Builder
	for _, block := range chatResp.Content {
		if block.Type == "text" {
			text.WriteString(block.Text)
		}
	}
	return text.String(), nil
}
