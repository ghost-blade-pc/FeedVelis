package enrichment

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const GenerationInputVersion = "generation-input-v1"

type PreparedInput struct {
	Normalized RevisionInput
	Hash       string
	Chunks     []string
	Truncated  bool
	RuneCount  int
}

func PrepareGenerationInput(input RevisionInput, chunkChars, maxChunks int) PreparedInput {
	normalized := RevisionInput{ArticleID: input.ArticleID, RevisionID: input.RevisionID,
		Title: normalizeInline(input.Title), Language: normalizeInline(input.Language), PlainText: normalizeParagraphs(input.PlainText)}
	encoded, _ := json.Marshal(struct {
		Version    string `json:"version"`
		ArticleID  int64  `json:"article_id"`
		RevisionID int64  `json:"revision_id"`
		Title      string `json:"title"`
		Language   string `json:"language"`
		PlainText  string `json:"plain_text"`
	}{GenerationInputVersion, normalized.ArticleID, normalized.RevisionID, normalized.Title, normalized.Language, normalized.PlainText})
	chunks := splitParagraphs(normalized.PlainText, chunkChars)
	selected, truncated := selectChunks(chunks, maxChunks)
	return PreparedInput{Normalized: normalized, Hash: sha256Hex(encoded), Chunks: selected, Truncated: truncated, RuneCount: utf8.RuneCountInString(normalized.PlainText)}
}

func normalizeInline(value string) string {
	return strings.Join(strings.Fields(norm.NFC.String(value)), " ")
}

func normalizeParagraphs(value string) string {
	value = strings.ReplaceAll(strings.ReplaceAll(norm.NFC.String(value), "\r\n", "\n"), "\r", "\n")
	paragraphs := strings.Split(value, "\n\n")
	result := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		if normalized := normalizeInline(paragraph); normalized != "" {
			result = append(result, normalized)
		}
	}
	return strings.Join(result, "\n\n")
}

func splitParagraphs(text string, limit int) []string {
	if limit < 1 || text == "" {
		return nil
	}
	var chunks []string
	current := ""
	for _, paragraph := range strings.Split(text, "\n\n") {
		if utf8.RuneCountInString(paragraph) > limit {
			if current != "" {
				chunks = append(chunks, current)
				current = ""
			}
			runes := []rune(paragraph)
			for len(runes) > 0 {
				size := limit
				if len(runes) < size {
					size = len(runes)
				}
				chunks = append(chunks, string(runes[:size]))
				runes = runes[size:]
			}
			continue
		}
		candidate := paragraph
		if current != "" {
			candidate = current + "\n\n" + paragraph
		}
		if utf8.RuneCountInString(candidate) <= limit {
			current = candidate
			continue
		}
		chunks = append(chunks, current)
		current = paragraph
	}
	if current != "" {
		chunks = append(chunks, current)
	}
	return chunks
}

func selectChunks(chunks []string, max int) ([]string, bool) {
	if len(chunks) <= max {
		return chunks, false
	}
	if max <= 0 {
		return nil, len(chunks) > 0
	}
	if max == 1 {
		return []string{chunks[0]}, true
	}
	selected := make([]string, 0, max)
	last := len(chunks) - 1
	for i := 0; i < max; i++ {
		selected = append(selected, chunks[i*last/(max-1)])
	}
	return selected, true
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func containsDisallowedControl(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) >= 0
}
