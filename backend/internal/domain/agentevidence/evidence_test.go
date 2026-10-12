package agentevidence

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

const runID = "81000000-0000-0000-0000-000000000001"
const evidenceID = "81000000-0000-0000-0000-000000000002"
const otherID = "81000000-0000-0000-0000-000000000003"

func fixture(t *testing.T) (*Set, Evidence) {
	t.Helper()
	s, err := NewSet(runID)
	if err != nil {
		t.Fatal(err)
	}
	e := Evidence{Version: 1, ID: evidenceID, RunID: runID, Kind: Article, ArticleID: 1, RevisionID: 2, ObtainedAt: time.Now(), ContentKind: Summary, Content: "已提供的摘要"}
	if err := s.Add(e); err != nil {
		t.Fatal(err)
	}
	return s, e
}

func TestEvidenceIdentityBoundaries(t *testing.T) {
	s, e := fixture(t)
	if err := s.Add(e); !errors.Is(err, ErrCitation) {
		t.Fatal("重复 evidence_id")
	}
	for _, mutate := range []func(*Evidence){
		func(e *Evidence) { e.Kind = "web" }, func(e *Evidence) { e.RunID = otherID }, func(e *Evidence) { e.ContentKind = "unknown" },
		func(e *Evidence) { e.Version = 2 }, func(e *Evidence) { e.RevisionID = 0 }, func(e *Evidence) { e.Content = "\xff" },
	} {
		invalid := e
		invalid.ID = otherID
		mutate(&invalid)
		if err := s.Add(invalid); !errors.Is(err, ErrCitation) {
			t.Fatal("非法证据被接受")
		}
	}
	b := Block{ID: "b1", Text: "解释", EvidenceIDs: []string{evidenceID}}
	if err := s.ValidateBlock(b, false); err != nil {
		t.Fatal(err)
	}
	b.EvidenceIDs = []string{otherID}
	if err := s.ValidateBlock(b, false); !errors.Is(err, ErrCitation) {
		t.Fatal("跨回合/未知 ID 被接受")
	}
	b.EvidenceIDs = []string{evidenceID, evidenceID}
	if err := s.ValidateBlock(b, false); !errors.Is(err, ErrCitation) {
		t.Fatal("重复引用被接受")
	}
}

func TestBodyAndVerbatimQuote(t *testing.T) {
	s, e := fixture(t)
	b := Block{ID: "b1", Text: "解释实现", EvidenceIDs: []string{evidenceID}}
	if err := s.ValidateBlock(b, true); !errors.Is(err, ErrCitation) {
		t.Fatal("仅摘要支持正文细节")
	}
	e.ID = otherID
	e.ContentKind = Body
	e.Content = "正文中实际保留的前半句"
	e.Truncated = true
	if err := s.Add(e); err != nil {
		t.Fatal(err)
	}
	b.EvidenceIDs = []string{otherID}
	b.Quotes = []Quote{{EvidenceID: otherID, Text: "保留的前半句"}}
	if err := s.ValidateBlock(b, true); err != nil {
		t.Fatal(err)
	}
	b.Quotes[0].Text = "已裁掉的后半句"
	if err := s.ValidateBlock(b, true); !errors.Is(err, ErrCitation) {
		t.Fatal("引用了未提供正文")
	}
	b.Quotes[0] = Quote{EvidenceID: evidenceID, Text: "摘要"}
	if err := s.ValidateBlock(b, true); !errors.Is(err, ErrCitation) {
		t.Fatal("引文不在片段引用中")
	}
}

func TestSourceNumberAndPartial(t *testing.T) {
	s, e := fixture(t)
	e.ID = otherID
	e.ContentKind = Body
	if err := s.Add(e); err != nil {
		t.Fatal(err)
	}
	a, err := NewAssembler(s, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Add(Block{ID: "b1", Text: "摘要", EvidenceIDs: []string{evidenceID}}, false); err != nil {
		t.Fatal(err)
	}
	if err := a.Add(Block{ID: "b2", Text: "正文", EvidenceIDs: []string{otherID, evidenceID}}, true); err != nil {
		t.Fatal(err)
	}
	answer, partial := a.Snapshot()
	if !partial || len(answer.References) != 2 || answer.References[0].Number != 1 || answer.References[1].Number != 1 {
		t.Fatalf("同修订重复编号: %+v", answer)
	}
	if _, err := a.Completed(); err == nil {
		t.Fatal("未完成回答可成功提交")
	}
	if err := a.Add(Block{ID: "b3", Text: "非法尾部", EvidenceIDs: []string{runID}}, false); err == nil {
		t.Fatal("伪造引用被接受")
	}
	answer.Blocks[0].EvidenceIDs[0] = "外部修改"
	current, partial := a.Snapshot()
	if !partial || current.Blocks[0].EvidenceIDs[0] != evidenceID || len(current.Blocks) != 2 {
		t.Fatal("拒绝片段或外部修改破坏了已有 partial")
	}
	if err := a.Finish(); err == nil {
		t.Fatal("非法尾部的 partial 被标记成功")
	}
	if _, err := a.Completed(); err == nil {
		t.Fatal("失败 partial 可成功提交")
	}
	// 只有协议解码器确认合法 finish 后才允许调用 Finish。
	a, _ = NewAssembler(s, 100)
	if err := a.Add(Block{ID: "b1", Text: "合法正文", EvidenceIDs: []string{otherID}}, true); err != nil {
		t.Fatal(err)
	}
	if err := a.Finish(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Completed(); err != nil {
		t.Fatal(err)
	}
	if _, partial := a.Snapshot(); partial {
		t.Fatal("完成回答仍标记 partial")
	}
	if err := a.Add(Block{ID: "b3", Text: "尾部", EvidenceIDs: []string{evidenceID}}, false); err == nil {
		t.Fatal("finish 后接受片段")
	}
}

func TestAnswerLimitsAndAtomicRejection(t *testing.T) {
	s, e := fixture(t)
	a, _ := NewAssembler(s, 2)
	if err := a.Add(Block{ID: "1", Text: "中文", EvidenceIDs: []string{evidenceID}}, false); err != nil {
		t.Fatal(err)
	}
	if err := a.Add(Block{ID: "2", Text: "字", EvidenceIDs: []string{evidenceID}}, false); err == nil {
		t.Fatal("Unicode 字符边界")
	}
	a, _ = NewAssembler(s, 1000)
	for i := 0; i < 32; i++ {
		if err := a.Add(Block{ID: fmt.Sprint(i), Text: "字", EvidenceIDs: []string{evidenceID}}, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Add(Block{ID: "33", Text: "字", EvidenceIDs: []string{evidenceID}}, false); err == nil {
		t.Fatal("片段上限")
	}
	a, _ = NewAssembler(s, 1000)
	for i := 1; i <= 11; i++ {
		e.ID = fmt.Sprintf("82000000-0000-0000-0000-%012d", i)
		e.ArticleID = int64(i)
		_ = s.Add(e)
		err := a.Add(Block{ID: fmt.Sprint(i), Text: "字", EvidenceIDs: []string{e.ID}}, false)
		if (err == nil) != (i <= 10) {
			t.Fatalf("来源上限: %d %v", i, err)
		}
	}
	current, _ := a.Snapshot()
	if len(current.Blocks) != 10 || len(current.References) != 10 {
		t.Fatal("拒绝第十一来源时部分写入")
	}
	for _, kind := range []AnswerKind{AnswerArticle, NoResults, Clarification} {
		if !kind.Valid() {
			t.Fatal(kind)
		}
	}
	if AnswerKind("web").Valid() {
		t.Fatal("未知回答类型")
	}
}
