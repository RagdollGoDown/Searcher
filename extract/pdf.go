// Package extract turns the raw bytes of a non-text file format into plain
// text, for callers (such as source.FilesSource) that need to feed file
// content to an LLM as text regardless of the file's original format.
package extract

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	"github.com/ledongthuc/pdf"
)

// MimePDF is the MIME type identifying PDF content.
const MimePDF = "application/pdf"

// ligatureReplacer expands standard Unicode ligature glyphs (U+FB00-FB06)
// into their plain-letter equivalents. PDF fonts commonly encode letter
// pairs like "fi"/"fl" as a single ligature glyph; the underlying PDF
// library correctly resolves these to their Unicode ligature codepoints,
// but leaving them as-is reads as a typo ("conﬁdence") and doesn't match
// plain-text search/embedding input. This doesn't cover ligatures a font
// maps to a non-standard codepoint via a custom encoding — those aren't
// recoverable without per-font, per-document knowledge.
var ligatureReplacer = strings.NewReplacer(
	"ﬀ", "ff",
	"ﬁ", "fi",
	"ﬂ", "fl",
	"ﬃ", "ffi",
	"ﬄ", "ffl",
	"ﬅ", "st",
	"ﬆ", "st",
)

// PDFText reads the plain text content of a PDF file's bytes. The
// underlying PDF library can panic on malformed input, so that's recovered
// into an error here rather than crashing the caller. It won't recover any
// text from scanned/image-only or encrypted PDFs.
func PDFText(data []byte) (text string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("malformed PDF: %v", r)
		}
	}()

	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	reader, err := r.GetPlainText()
	if err != nil {
		return "", err
	}
	b, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	return ligatureReplacer.Replace(string(b)), nil
}
