package query

// Attachment is a piece of content supplied directly by the caller (e.g. a
// file picked in the browser) rather than fetched by a Source.
type Attachment struct {
	Name    string
	Content string
	// Encoding indicates how Content is encoded: "" (or "text") means
	// Content is ready-to-use text; "base64" means Content is raw bytes
	// (e.g. a binary file) base64-encoded.
	Encoding string
	// MimeType is the attachment's content type (e.g. "application/pdf"),
	// used by Sources that need to know how to interpret Content. Empty
	// means plain text.
	MimeType string
	// SourceURL is the URL a scraped page's content was fetched from.
	// Empty for attachments that didn't come from a crawl (e.g. browser
	// uploads).
	SourceURL string
}

type Query struct {
	query       string
	attachments []Attachment
}

// GetQuery returns the query string; it's unexported on the struct
// so callers can't mutate it after construction.
func (q *Query) GetQuery() string {
	return q.query
}

// GetAttachments returns any attachments supplied alongside the query.
func (q *Query) GetAttachments() []Attachment {
	return q.attachments
}

func NewQuery(query string, attachments ...Attachment) *Query {
	return &Query{
		query:       query,
		attachments: attachments,
	}
}
