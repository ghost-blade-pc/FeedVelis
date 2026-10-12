package agentrun

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/strictjson"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
)

type ConstraintsInput struct {
	Keyword  *string `json:"keyword,omitempty"`
	Topic    *string `json:"topic,omitempty"`
	SourceID *int64  `json:"source_id,omitempty"`
}
type Command struct {
	MessageID        string            `json:"message_id"`
	ContextArticleID *int64            `json:"context_article_id,omitempty"`
	Constraints      *ConstraintsInput `json:"constraints,omitempty"`
	RetryOf          *string           `json:"retry_of,omitempty"`
}

func normalizedID(raw string) (string, error) {
	id, err := account.ParseUUID(raw)
	if err != nil || id.IsZero() {
		return "", run.ErrInvalidInput
	}
	return id.String(), nil
}
func DecodeCommand(body []byte) (Command, error) {
	fields, err := strictjson.Object(body, 16384, "message_id", "context_article_id", "constraints", "retry_of")
	if err != nil {
		return Command{}, run.ErrInvalidInput
	}
	if raw, ok := fields["constraints"]; ok {
		var constraints ConstraintsInput
		if strictjson.Decode(raw, 16384, &constraints, "keyword", "topic", "source_id") != nil {
			return Command{}, run.ErrInvalidInput
		}
	}
	var command Command
	if json.Unmarshal(body, &command) != nil {
		return Command{}, run.ErrInvalidInput
	}
	if _, err := NormalizeCommand(command); err != nil {
		return Command{}, err
	}
	return command, nil
}

func NormalizeCommand(command Command) (run.Input, error) {
	id, err := normalizedID(command.MessageID)
	if err != nil {
		return run.Input{}, err
	}
	input := run.Input{MessageID: id}
	if command.ContextArticleID != nil {
		if *command.ContextArticleID < 1 {
			return run.Input{}, run.ErrInvalidInput
		}
		input.ContextArticleID = *command.ContextArticleID
	}
	if command.RetryOf != nil {
		input.RetryOf, err = normalizedID(*command.RetryOf)
		if err != nil {
			return run.Input{}, err
		}
	}
	if c := command.Constraints; c != nil {
		for _, value := range []*string{c.Keyword, c.Topic} {
			if value != nil && (!utf8.ValidString(*value) || strings.ContainsRune(*value, 0)) {
				return run.Input{}, run.ErrInvalidInput
			}
		}
		query, err := articlesearch.Normalize(articlesearch.Request{Q: "条件规范化", Keyword: c.Keyword, Topic: c.Topic, SourceID: c.SourceID})
		if err != nil {
			return run.Input{}, run.ErrInvalidInput
		}
		input.Constraints = run.Constraints{Keyword: query.Filters.Keyword, Topic: query.Filters.Topic, SourceID: query.Filters.SourceID}
	}
	return input, nil
}
