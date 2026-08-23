package source

import (
	"encoding/base64"
	"fmt"
	"strings"

	"searcher/extract"
	"searcher/query"
)

const filesDefaultMaxFileChars = 20000
const filesDefaultMaxTotalChars = 60000

// filesMaxPDFBytes caps how large a (decoded) PDF this will attempt to
// parse, so a single huge upload can't spike CPU/memory on small hardware.
const filesMaxPDFBytes = 20 * 1024 * 1024

// fileExtractor turns the raw bytes of a non-text attachment (decoded from
// base64) into plain text. maxBytes caps how large a decoded file it will
// attempt to process, so a single huge upload can't spike CPU/memory on
// small hardware.
type fileExtractor struct {
	maxBytes int
	extract  func(data []byte) (string, error)
}

// fileExtractors maps an attachment's MimeType to how to turn it into text.
// A MimeType with no entry here is assumed to already be plain text (the
// default for existing .txt/.md/etc. attachments). Add an entry to support
// a new binary format.
var fileExtractors = map[string]fileExtractor{
	extract.MimePDF: {maxBytes: filesMaxPDFBytes, extract: extract.PDFText},
}

// filesIcon is a generic document glyph.
const filesIcon = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` +
	`<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"></path>` +
	`<path d="M14 2v6h6"></path>` +
	`</svg>`

// FilesSource turns Query attachments (e.g. files picked in the browser)
// directly into Results, ignoring the search text. It truncates content to
// stay within maxFileChars per file and maxTotalChars across all files, so a
// large or numerous attachments can't blow out the LLM's context window.
type FilesSource struct {
	maxFileChars  int
	maxTotalChars int
}

func NewFilesSource(maxFileChars, maxTotalChars int) *FilesSource {
	return &FilesSource{maxFileChars: maxFileChars, maxTotalChars: maxTotalChars}
}

// Compile-time check that FilesSource satisfies Source.
var _ Source = (*FilesSource)(nil)

func (s *FilesSource) Icon() string {
	return filesIcon
}

func (s *FilesSource) Fetch(q query.Query) ([]Result, error) {
	maxFileChars := s.maxFileChars
	if maxFileChars <= 0 {
		maxFileChars = filesDefaultMaxFileChars
	}
	maxTotalChars := s.maxTotalChars
	if maxTotalChars <= 0 {
		maxTotalChars = filesDefaultMaxTotalChars
	}

	results := make([]Result, 0, len(q.GetAttachments()))
	remaining := maxTotalChars
	for _, a := range q.GetAttachments() {
		if remaining <= 0 {
			break
		}

		content := resolveContent(a)
		if len(content) > maxFileChars {
			content = content[:maxFileChars]
		}
		if len(content) > remaining {
			content = content[:remaining]
		}
		remaining -= len(content)

		title := a.Name
		if title == "" {
			title = fmt.Sprintf("Attachment %d", len(results)+1)
		}
		results = append(results, Result{Title: title, Extract: content})
	}

	return results, nil
}

// resolveContent turns an Attachment into plain text ready for the LLM.
// Attachments with no registered fileExtractors entry for their MimeType are
// assumed to already be text and pass through unchanged. Otherwise Content
// is base64-decoded and run through that MimeType's extractor. Any failure
// (bad encoding, oversized file, unreadable file) degrades to an
// explanatory message rather than an error, so one bad attachment doesn't
// fail the whole request.
func resolveContent(a query.Attachment) string {
	ext, ok := fileExtractors[a.MimeType]
	if !ok {
		return a.Content
	}

	data, err := base64.StdEncoding.DecodeString(a.Content)
	if err != nil {
		return fmt.Sprintf("[could not read %q: invalid file data]", a.Name)
	}
	if len(data) > ext.maxBytes {
		return fmt.Sprintf("[skipped %q: file exceeds the %d MB size limit]", a.Name, ext.maxBytes/(1024*1024))
	}

	text, err := ext.extract(data)
	if err != nil {
		return fmt.Sprintf("[could not extract text from %q: %v]", a.Name, err)
	}
	if strings.TrimSpace(text) == "" {
		return fmt.Sprintf("[%q contains no extractable text]", a.Name)
	}
	return text
}
