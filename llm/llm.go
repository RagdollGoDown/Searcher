package llm

import "context"

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one turn in a chat-style conversation with an LLM.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// Client is a minimal chat-completion interface. It's deliberately
// provider-agnostic so callers (like a summarizer) can be written against
// this interface and swap the backend without changing call sites.
type Client interface {
	// Init prepares the client for use, starting any local infrastructure
	// it depends on (e.g. a local model server) if it isn't already
	// running. Callers should call it once before Chat. Implementations
	// that need no setup can treat it as a no-op.
	Init(ctx context.Context) error

	Chat(ctx context.Context, messages []Message) (string, error)
}

// Embedder turns text into a vector embedding, for callers that need
// semantic similarity (e.g. RAG retrieval). Kept separate from Client
// since chat completion and embedding are different capabilities, and not
// every backend supports both.
type Embedder interface {
	// Init prepares the embedder for use; see Client.Init.
	Init(ctx context.Context) error

	Embed(ctx context.Context, text string) ([]float32, error)
}
