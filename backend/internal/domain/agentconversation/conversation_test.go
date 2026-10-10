package agentconversation

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTitleRules(t *testing.T) {
	if got, err := NormalizeTitle(nil); err != nil || got != DefaultTitle {
		t.Fatalf("默认标题: %q %v", got, err)
	}
	for _, raw := range []string{"", " \u3000\t", "a\nb", "a\rb", "a\tb", "a\x00b", "a\x7fb", "a\u0085b", "a\u2028b", string([]byte{0xff}), strings.Repeat("界", 101)} {
		if _, err := NormalizeTitle(&raw); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("非法标题应拒绝: %q", raw)
		}
	}
	for _, raw := range []string{"  标题\u3000", strings.Repeat("😀", 100), "**Markdown**", "e\u0301"} {
		got, err := NormalizeTitle(&raw)
		if err != nil || got != strings.TrimSpace(raw) {
			t.Errorf("标题规范化错误: %q %v", got, err)
		}
	}
}

func TestContentRules(t *testing.T) {
	input := "  **标题**\r\n    code\r结束  \n"
	want := "  **标题**\n    code\n结束  \n"
	if got, err := NormalizeContent(input, 100); err != nil || got != want {
		t.Fatalf("格式未保留: %q %v", got, err)
	}
	for _, raw := range []string{"", " \t\n\r\u3000\u00a0", "a\x00b", string([]byte{0xff}), "😀e\u0301 "} {
		if _, err := NormalizeContent(raw, 3); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("非法正文应拒绝: %q", raw)
		}
	}
	for _, raw := range []string{"😀e\u0301", " a ", "a\x01b", "a\tb"} {
		if got, err := NormalizeContent(raw, 3); err != nil || got != raw {
			t.Errorf("合法正文应保留: %q %v", got, err)
		}
	}
	if got, err := NormalizeContent("a\r\nb", 3); err != nil || got != "a\nb" {
		t.Fatalf("应按规范化后长度计数: %q %v", got, err)
	}
}

func TestConversationAndMessageRules(t *testing.T) {
	now := time.Date(2026, 10, 10, 1, 0, 0, 0, time.UTC)
	const id = "81000000-0000-0000-0000-000000000001"
	const owner = "81000000-0000-0000-0000-000000000002"
	c, err := New(id, owner, nil, now)
	if err != nil || c.TitleVersion != 1 || c.MessageCount != 0 || c.NextSequence != 1 {
		t.Fatalf("会话初始状态: %+v %v", c, err)
	}
	if err := c.Rename(" 新对话 ", 1, now.Add(time.Hour)); err != nil || c.TitleVersion != 1 || !c.LastActivityAt.Equal(now) {
		t.Fatal("无变化改名不得修改状态")
	}
	if err := c.Rename("新对话", 2, now); !errors.Is(err, ErrTitleVersion) {
		t.Fatal("无变化仍需检查版本")
	}
	if err := c.Rename("新标题", 1, now.Add(-time.Hour)); err != nil || c.TitleVersion != 2 || !c.LastActivityAt.Equal(now) {
		t.Fatal("时钟回拨不得降低活跃时间")
	}
	for _, role := range []Role{RoleUser, RoleAssistant} {
		m, err := NewMessage(owner, id, 1, role, "正文", 2, now)
		if err != nil || m.Role != role || m.Sequence != 1 {
			t.Fatalf("合法消息: %+v %v", m, err)
		}
	}
	for _, role := range []Role{"", "system", "tool", "reasoning"} {
		if _, err := NewMessage(owner, id, 1, role, "正文", 2, now); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("非法角色: %s", role)
		}
	}
	for _, test := range []struct {
		id, owner string
		sequence  int64
		now       time.Time
	}{
		{"bad", owner, 1, now}, {id, "bad", 1, now}, {id, owner, 0, now}, {id, owner, 1, time.Time{}},
	} {
		if _, err := NewMessage(test.id, test.owner, test.sequence, RoleUser, "正文", 2, test.now); err == nil {
			t.Fatal("非法消息身份、序号或时间未拒绝")
		}
	}
	if _, err := New(id, "bad", nil, now); err == nil {
		t.Fatal("非法会话归属未拒绝")
	}
}
