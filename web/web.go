// Package web serves a simple browser chat UI for interacting with an
// llm.Client, either as a plain conversation or backed by one or more
// search Sources.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"searcher/llm"
	"searcher/query"
	"searcher/source"
	"searcher/store"
)

//go:embed static
var staticFS embed.FS

const chatSystemPrompt = "You are a helpful assistant."

const summarizerSystemPrompt = "You are a research assistant. You will be given a search query " +
	"and a set of article extracts related to it. Write a concise summary, in your own words, " +
	"that answers the query using only information from the extracts. If the extracts don't " +
	"contain enough information, say so. You shouldn't write more than what is required to answer the query." +
	"You shouldn't make up information. Use markdown formatting for your responses."

// conversationTitleMaxChars caps how much of a conversation's first
// message is used as its title.
const conversationTitleMaxChars = 50

type chatRequest struct {
	// Messages is the full conversation so far, oldest first, ending with
	// the newest user message.
	Messages []llm.Message `json:"messages"`
	// Sources, when non-empty, names which registered Sources to query for
	// the newest message; results are fetched and summarized instead of
	// chatting directly with the LLM. Empty means a plain LLM chat.
	Sources []string `json:"sources"`
	// Files, when non-empty, are attachments (e.g. picked in the browser)
	// made available to Sources via the Query, such as the "Local Files"
	// source. Has no effect unless a Source that reads attachments is also
	// named in Sources.
	Files []fileAttachment `json:"files"`
	// ConversationID identifies which saved conversation this turn belongs
	// to. 0 means "start a new conversation", titled from this message.
	// Ignored if conversation persistence isn't enabled.
	ConversationID int64 `json:"conversationId"`
}

// fileAttachment is a single file's content supplied by the client.
type fileAttachment struct {
	Name string `json:"name"`
	// Content is the file's content: plain text by default, or the value
	// described by Encoding/MimeType (e.g. base64-encoded PDF bytes).
	Content  string `json:"content"`
	Encoding string `json:"encoding"`
	MimeType string `json:"mimeType"`
}

type chatResponseBody struct {
	Reply string `json:"reply,omitempty"`
	Error string `json:"error,omitempty"`
	// Sources holds what was actually retrieved from each queried Source,
	// so the frontend can show it alongside the summarized Reply. Empty
	// unless the request named at least one Source.
	Sources []sourceResults `json:"sources,omitempty"`
	// ConversationID is the conversation this turn was saved to — either
	// echoing the request's ConversationID, or a newly created one if the
	// request's was 0. Omitted if conversation persistence isn't enabled.
	ConversationID int64 `json:"conversationId,omitempty"`
}

// sourceResults is one queried Source's results, as sent to the frontend.
type sourceResults struct {
	Source  string             `json:"source"`
	Results []sourceResultInfo `json:"results"`
}

type sourceResultInfo struct {
	Title   string `json:"title"`
	Extract string `json:"extract"`
}

// Server serves the chat UI and its backing API.
type Server struct {
	client  llm.Client
	sources map[string]source.Source
	// rag is the RAG document store's Source, used directly (beyond the
	// Source interface used for chat) to serve the document management
	// page. nil if RAG document management isn't enabled.
	rag *source.RAGSource
	// store persists chat conversations. nil if conversation persistence
	// isn't enabled, in which case chats are never saved (today's
	// behavior). Conversations aren't a Source (they're not a retrievable
	// tool), so unlike rag this is used directly, with no Source-interface
	// wrapper.
	store *store.Store
}

// NewServer creates a Server. sources maps a display/toggle name to the
// Source it queries; a chat request selects among them by name. rag, if
// non-nil, is additionally used to serve the /api/rag/documents management
// endpoints; it's typically also present in sources so it can be toggled
// on for chat like any other Source. convStore, if non-nil, enables saving
// and retrieving past conversations; it's typically the same store.Store
// backing rag.
func NewServer(client llm.Client, sources map[string]source.Source, rag *source.RAGSource, convStore *store.Store) *Server {
	return &Server{client: client, sources: sources, rag: rag, store: convStore}
}

// Handler returns the HTTP handler for the chat UI and API.
func (s *Server) Handler() (http.Handler, error) {
	staticRoot, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, fmt.Errorf("web: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(staticRoot)))
	mux.HandleFunc("/api/sources", s.handleSources)
	mux.HandleFunc("/api/chat", s.handleChat)
	if s.rag != nil {
		mux.HandleFunc("GET /api/rag/documents", s.handleListDocuments)
		mux.HandleFunc("POST /api/rag/documents", s.handleUploadDocument)
		mux.HandleFunc("DELETE /api/rag/documents/{id}", s.handleDeleteDocument)
		mux.HandleFunc("GET /api/rag/settings", s.handleGetRAGSettings)
		mux.HandleFunc("PUT /api/rag/settings", s.handlePutRAGSettings)
	}
	if s.store != nil {
		mux.HandleFunc("GET /api/conversations", s.handleListConversations)
		mux.HandleFunc("GET /api/conversations/{id}/messages", s.handleGetConversationMessages)
		mux.HandleFunc("DELETE /api/conversations/{id}", s.handleDeleteConversation)
	}
	return mux, nil
}

type sourceInfo struct {
	Name string `json:"name"`
	Icon string `json:"icon"`
}

// handleSources lists the Sources available for the frontend to present as
// toggles, along with each one's icon.
func (s *Server) handleSources(w http.ResponseWriter, r *http.Request) {
	names := make([]string, 0, len(s.sources))
	for name := range s.sources {
		names = append(names, name)
	}
	sort.Strings(names)

	infos := make([]sourceInfo, 0, len(names))
	for _, name := range names {
		infos = append(infos, sourceInfo{Name: name, Icon: s.sources[name].Icon()})
	}
	writeJSON(w, http.StatusOK, map[string][]sourceInfo{"sources": infos})
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, chatResponseBody{Error: "invalid request body"})
		return
	}
	if len(req.Messages) == 0 {
		writeJSON(w, http.StatusBadRequest, chatResponseBody{Error: "messages must not be empty"})
		return
	}

	userText := req.Messages[len(req.Messages)-1].Content

	var reply string
	var grouped []sourceResults
	var err error
	if len(req.Sources) > 0 || len(req.Files) > 0 {
		reply, grouped, err = s.searchAndSummarize(r.Context(), userText, req.Sources, req.Files)
	} else {
		reply, err = s.chat(r.Context(), req.Messages)
	}
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, chatResponseBody{Error: err.Error()})
		return
	}

	// Only create/touch a conversation once we know the reply succeeded, so
	// a failed turn never leaves behind an empty conversation the frontend
	// doesn't know the ID of (it can't, since none was returned) and would
	// otherwise orphan on retry.
	conversationID := req.ConversationID
	if s.store != nil {
		if conversationID == 0 {
			conversationID, err = s.store.CreateConversation(r.Context(), conversationTitle(userText))
			if err != nil {
				writeJSON(w, http.StatusInternalServerError, chatResponseBody{Error: err.Error()})
				return
			}
		}
		// The reply already succeeded; a persistence hiccup here shouldn't
		// cost the user a good answer, so this is logged rather than
		// failing the request.
		if err := s.store.AddMessage(r.Context(), conversationID, string(llm.RoleUser), userText); err != nil {
			fmt.Printf("web: saving user message: %s\n", err)
		}
		if err := s.store.AddMessage(r.Context(), conversationID, string(llm.RoleAssistant), reply); err != nil {
			fmt.Printf("web: saving assistant message: %s\n", err)
		}
	}

	writeJSON(w, http.StatusOK, chatResponseBody{Reply: reply, Sources: grouped, ConversationID: conversationID})
}

// conversationTitle derives a conversation's title from its first message:
// the message text, trimmed and capped at conversationTitleMaxChars.
func conversationTitle(firstMessage string) string {
	title := strings.TrimSpace(firstMessage)
	if title == "" {
		return "New conversation"
	}
	if runes := []rune(title); len(runes) > conversationTitleMaxChars {
		title = string(runes[:conversationTitleMaxChars]) + "…"
	}
	return title
}

// searchAndSummarize fetches search results for queryText from each named
// source and asks the LLM for a single summary answering it. files are made
// available to sources (e.g. "Local Files") as query attachments. It
// returns the summary along with each source's raw results, grouped by
// source, so the caller can show the user what was actually retrieved.
func (s *Server) searchAndSummarize(ctx context.Context, queryText string, sourceNames []string, files []fileAttachment) (string, []sourceResults, error) {
	attachments := make([]query.Attachment, len(files))
	for i, f := range files {
		attachments[i] = query.Attachment{Name: f.Name, Content: f.Content, Encoding: f.Encoding, MimeType: f.MimeType}
	}
	q := query.NewQuery(queryText, attachments...)

	var results []source.Result
	grouped := make([]sourceResults, 0, len(sourceNames))
	for _, name := range sourceNames {
		src, ok := s.sources[name]
		if !ok {
			return "", nil, fmt.Errorf("unknown source %q", name)
		}
		r, err := src.Fetch(*q)
		if err != nil {
			return "", nil, fmt.Errorf("fetching from %s: %w", name, err)
		}
		results = append(results, r...)

		infos := make([]sourceResultInfo, len(r))
		for i, res := range r {
			infos[i] = sourceResultInfo{Title: res.Title, Extract: res.Extract}
		}
		grouped = append(grouped, sourceResults{Source: name, Results: infos})
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Query: %s\n\n", queryText)
	for _, result := range results {
		fmt.Fprintf(&b, "## %s\n%s\n\n", result.Title, result.Extract)
	}

	messages := []llm.Message{
		{Role: llm.RoleSystem, Content: summarizerSystemPrompt},
		{Role: llm.RoleUser, Content: b.String()},
	}
	reply, err := s.client.Chat(ctx, messages)
	if err != nil {
		return "", nil, err
	}
	return reply, grouped, nil
}

// chat relays the conversation directly to the LLM.
func (s *Server) chat(ctx context.Context, history []llm.Message) (string, error) {
	messages := make([]llm.Message, 0, len(history)+1)
	messages = append(messages, llm.Message{Role: llm.RoleSystem, Content: chatSystemPrompt})
	messages = append(messages, history...)
	return s.client.Chat(ctx, messages)
}

// ragDocumentInfo is a stored document's metadata as sent to the frontend.
type ragDocumentInfo struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	MimeType   string `json:"mimeType"`
	AddedAt    string `json:"addedAt"`
	ChunkCount int    `json:"chunkCount"`
}

// ragListResponse is the RAG store's contents plus its total on-disk size,
// covering both document metadata and chunk content/embeddings.
type ragListResponse struct {
	Documents    []ragDocumentInfo `json:"documents"`
	StorageBytes int64             `json:"storageBytes"`
}

func (s *Server) handleListDocuments(w http.ResponseWriter, r *http.Request) {
	docs, err := s.rag.ListDocuments(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	infos := make([]ragDocumentInfo, len(docs))
	for i, d := range docs {
		infos[i] = ragDocumentInfo{
			ID:         d.ID,
			Name:       d.Name,
			MimeType:   d.MimeType,
			AddedAt:    d.AddedAt.Format(time.RFC3339),
			ChunkCount: d.ChunkCount,
		}
	}

	storageBytes, err := s.rag.StorageSizeBytes()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, ragListResponse{Documents: infos, StorageBytes: storageBytes})
}

// ragUploadResponse is the result of uploading a document, or an error
// (e.g. an unreadable file) that didn't prevent the request itself from
// being handled.
type ragUploadResponse struct {
	ID    int64  `json:"id,omitempty"`
	Error string `json:"error,omitempty"`
}

func (s *Server) handleUploadDocument(w http.ResponseWriter, r *http.Request) {
	var req fileAttachment
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ragUploadResponse{Error: "invalid request body"})
		return
	}
	if req.Name == "" {
		writeJSON(w, http.StatusBadRequest, ragUploadResponse{Error: "name is required"})
		return
	}

	attachment := query.Attachment{Name: req.Name, Content: req.Content, Encoding: req.Encoding, MimeType: req.MimeType}
	id, err := s.rag.Ingest(r.Context(), attachment)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ragUploadResponse{Error: err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, ragUploadResponse{ID: id})
}

func (s *Server) handleDeleteDocument(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid document id", http.StatusBadRequest)
		return
	}

	if err := s.rag.DeleteDocument(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ragSettingsBody is the RAG chunking settings, as sent to and from the
// frontend. Changing them only affects documents ingested afterward.
type ragSettingsBody struct {
	ChunkChars   int    `json:"chunkChars"`
	ChunkOverlap int    `json:"chunkOverlap"`
	Error        string `json:"error,omitempty"`
}

func (s *Server) handleGetRAGSettings(w http.ResponseWriter, r *http.Request) {
	cs, err := s.rag.GetChunkSettings(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, ragSettingsBody{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ragSettingsBody{ChunkChars: cs.ChunkChars, ChunkOverlap: cs.ChunkOverlap})
}

func (s *Server) handlePutRAGSettings(w http.ResponseWriter, r *http.Request) {
	var req ragSettingsBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, ragSettingsBody{Error: "invalid request body"})
		return
	}

	cs := source.ChunkSettings{ChunkChars: req.ChunkChars, ChunkOverlap: req.ChunkOverlap}
	if err := s.rag.SetChunkSettings(r.Context(), cs); err != nil {
		writeJSON(w, http.StatusBadRequest, ragSettingsBody{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, ragSettingsBody{ChunkChars: cs.ChunkChars, ChunkOverlap: cs.ChunkOverlap})
}

// conversationInfo is a saved conversation's metadata, as sent to the
// frontend for the conversation-switcher list.
type conversationInfo struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updatedAt"`
}

func (s *Server) handleListConversations(w http.ResponseWriter, r *http.Request) {
	convos, err := s.store.ListConversations(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	infos := make([]conversationInfo, len(convos))
	for i, c := range convos {
		infos[i] = conversationInfo{ID: c.ID, Title: c.Title, UpdatedAt: c.UpdatedAt.Format(time.RFC3339)}
	}
	writeJSON(w, http.StatusOK, map[string][]conversationInfo{"conversations": infos})
}

func (s *Server) handleGetConversationMessages(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid conversation id", http.StatusBadRequest)
		return
	}

	msgs, err := s.store.GetMessages(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	out := make([]llm.Message, len(msgs))
	for i, m := range msgs {
		out[i] = llm.Message{Role: llm.Role(m.Role), Content: m.Content}
	}
	writeJSON(w, http.StatusOK, map[string][]llm.Message{"messages": out})
}

func (s *Server) handleDeleteConversation(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid conversation id", http.StatusBadRequest)
		return
	}

	if err := s.store.DeleteConversation(r.Context(), id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
