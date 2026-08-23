package store

import "strings"

// DefaultChunkChars and DefaultChunkOverlap are the chunk size and overlap
// ChunkText falls back to when called with a non-positive maxChars, or an
// overlap that isn't smaller than maxChars.
const (
	DefaultChunkChars   = 800
	DefaultChunkOverlap = 100
)

// ChunkText splits text into overlapping chunks suitable for embedding. It
// prefers to break on paragraph boundaries so related sentences stay
// together, falling back to a hard split for any single paragraph longer
// than the chunk size. Each chunk is capped at maxChars characters; overlap
// characters of trailing context are carried into the next chunk so
// meaning isn't lost at a cut point. Falls back to
// DefaultChunkChars/DefaultChunkOverlap if maxChars or overlap is out of
// range, so a bad caller-supplied setting can't produce degenerate
// chunking.
func ChunkText(text string, maxChars, overlap int) []string {
	if maxChars <= 0 {
		maxChars = DefaultChunkChars
	}
	if overlap < 0 || overlap >= maxChars {
		overlap = DefaultChunkOverlap
	}
	return chunkText(text, maxChars, overlap)
}

func chunkText(text string, maxChars, overlap int) []string {
	paragraphs := splitParagraphs(text)
	if len(paragraphs) == 0 {
		return nil
	}

	var chunks []string
	current := ""

	for _, p := range paragraphs {
		if len([]rune(p)) > maxChars {
			if strings.TrimSpace(current) != "" {
				chunks = append(chunks, current)
			}
			chunks = append(chunks, splitLong(p, maxChars, overlap)...)
			current = ""
			continue
		}

		next := p
		if current != "" {
			next = current + "\n\n" + p
		}

		if len([]rune(next)) > maxChars {
			chunks = append(chunks, current)
			if seed := lastRunes(current, overlap); seed != "" {
				current = seed + "\n\n" + p
			} else {
				current = p
			}
			continue
		}

		current = next
	}

	if strings.TrimSpace(current) != "" {
		chunks = append(chunks, current)
	}

	return chunks
}

// splitParagraphs splits text on blank lines, trimming and dropping empty
// paragraphs.
func splitParagraphs(text string) []string {
	raw := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n\n")
	paragraphs := make([]string, 0, len(raw))
	for _, p := range raw {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			paragraphs = append(paragraphs, trimmed)
		}
	}
	return paragraphs
}

// splitLong hard-splits a single paragraph too long to fit in one chunk
// into maxChars-sized windows, each overlapping the previous by overlap
// characters.
func splitLong(p string, maxChars, overlap int) []string {
	runes := []rune(p)
	step := maxChars - overlap
	if step <= 0 {
		step = maxChars
	}

	var parts []string
	for start := 0; start < len(runes); start += step {
		end := start + maxChars
		if end > len(runes) {
			end = len(runes)
		}
		parts = append(parts, string(runes[start:end]))
		if end == len(runes) {
			break
		}
	}
	return parts
}

// lastRunes returns the trailing n runes of s (or all of s if shorter).
func lastRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[len(runes)-n:])
}
