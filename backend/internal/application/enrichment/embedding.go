package enrichment

import (
	"encoding/json"
	"math"
	"strings"
)

type RetrievalDocument struct{ Version, Document, Hash string }

func BuildRetrievalDocument(version string, revision RevisionInput, content GeneratedContent, bodyChars int) RetrievalDocument {
	body := []rune(normalizeParagraphs(revision.PlainText))
	if len(body) > bodyChars {
		body = body[:bodyChars]
	}
	parts := []string{
		"title: " + normalizeInline(revision.Title),
		"summary: " + strings.TrimSpace(content.Summary),
		"keywords: " + strings.Join(content.Keywords, ", "),
		"topics: " + strings.Join(content.Topics, ", "),
		"body: " + string(body),
	}
	document := strings.Join(parts, "\n")
	encoded, _ := json.Marshal(struct{ Version, Document string }{version, document})
	return RetrievalDocument{Version: version, Document: document, Hash: sha256Hex(encoded)}
}

func ValidateVector(vector []float64, dimensions int) error {
	if len(vector) == 0 || len(vector) != dimensions {
		return invalidOutput(ReasonVectorDimensions, "Embedding 向量为空或维度不符", nil)
	}
	for _, value := range vector {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return invalidOutput(ReasonVectorNonFinite, "Embedding 向量包含非有限数值", nil)
		}
	}
	return nil
}
