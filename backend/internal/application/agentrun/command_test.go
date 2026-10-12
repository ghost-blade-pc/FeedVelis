package agentrun

import (
	"errors"
	"strings"
	"testing"

	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
)

func TestStrictRunCommand(t *testing.T) {
	good := `{"message_id":"` + testID + `","context_article_id":42,"constraints":{"keyword":"  Go  ","topic":"  系统设计  ","source_id":8}}`
	command, err := DecodeCommand([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	input, err := NormalizeCommand(command)
	if err != nil || input.ContextArticleID != 42 || input.Constraints != (run.Constraints{Keyword: "Go", Topic: "系统设计", SourceID: 8}) {
		t.Fatalf("规范化: %+v %v", input, err)
	}
	tests := []string{
		`{}`, `null`, `[]`, good + `{}`,
		`{"message_id":null}`, `{"message_id":1}`, `{"message_id":""}`,
		`{"message_id":"` + testID + `","message_id":"` + testID + `"}`,
		`{"Message_ID":"` + testID + `"}`, `{"message_id":"` + testID + `","role":"assistant"}`,
		`{"message_id":"` + testID + `","context_article_id":null}`, `{"message_id":"` + testID + `","context_article_id":0}`,
		`{"message_id":"` + testID + `","context_article_id":1.5}`, `{"message_id":"` + testID + `","retry_of":""}`,
		`{"message_id":"` + testID + `","retry_of":null}`, `{"message_id":"` + testID + `","constraints":null}`,
		`{"message_id":"` + testID + `","constraints":{"keyword":"Go","keyword":"Go"}}`,
		`{"message_id":"` + testID + `","constraints":{"source_id":0}}`,
		`{"message_id":"` + testID + `","constraints":{"topic":" "}}`,
		`{"message_id":"` + testID + `","constraints":{"topic":"\ud800"}}`,
		`{"message_id":"` + testID + `","constraints":{"keyword":"\u0000"}}`,
		`{"message_id":"` + testID + `","constraints":{"since":"昨天"}}`,
		`{"message_id":"` + testID + `","constraints":{"topic":{}}}`,
		`{"message_id":"` + testID + `","constraints":{"keyword":"` + strings.Repeat("字", 65) + `"}}`,
		`{"message_id":"` + testID + `","constraints":[]}`, string([]byte{'{', '"', 0xff, '"', ':', '1', '}'}), strings.Repeat(" ", 16385),
	}
	for _, raw := range tests {
		if _, err := DecodeCommand([]byte(raw)); !errors.Is(err, run.ErrInvalidInput) {
			t.Fatalf("非法命令被接受: %q %v", raw, err)
		}
	}
	for _, raw := range []string{`{"message_id":"` + testID + `"}`, `{"message_id":"` + testID + `","constraints":{}}`} {
		if _, err := DecodeCommand([]byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInvalidCommandsDoNotAccessPorts(t *testing.T) {
	s := NewService(nil, nil, nil, nil, nil, nil, Config{})
	for _, command := range []Command{{}, {MessageID: "bad"}, {MessageID: testID, ContextArticleID: new(int64)}} {
		if _, err := s.Accept(t.Context(), Caller{testID, testID}, testID, otherID, command); !errors.Is(err, run.ErrInvalidInput) {
			t.Fatal(err)
		}
	}
}
