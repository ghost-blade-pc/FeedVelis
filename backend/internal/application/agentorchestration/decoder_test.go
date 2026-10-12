package agentorchestration

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agenttools"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentevidence"
)

const testRun = "82000000-0000-0000-0000-000000000001"
const testOther = "82000000-0000-0000-0000-000000000002"

func collection(t *testing.T, bytes int) *EvidenceCollection {
	t.Helper()
	c, err := NewEvidenceCollection(testRun, bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Get(agenttools.GetResult{ArticleRef: agenttools.ArticleRef{ArticleID: 1, RevisionID: 2, Title: "可信标题", Path: "/articles/1", Summary: "摘要"}, Content: strings.Repeat("真实正文🙂", 100)}, time.Now()); err != nil {
		t.Fatal(err)
	}
	return c
}
func blockLine(id, text string, refs []string) string {
	b, _ := json.Marshal(struct {
		Type string   `json:"type"`
		ID   string   `json:"id"`
		Text string   `json:"text"`
		IDs  []string `json:"evidence_ids"`
	}{"block", id, text, refs})
	return string(b) + "\n"
}

const header = "{\"type\":\"header\",\"version\":1,\"kind\":\"answer\"}\n"
const finish = "{\"type\":\"finish\"}\n"

func TestRetainedEvidence(t *testing.T) {
	c := collection(t, 200)
	if len(c.Materials) != 1 || !c.Evidence[0].Truncated || c.Materials[0].Text != c.Evidence[0].Content {
		t.Fatal("材料与证据不一致")
	}
	id := c.Evidence[0].ID
	b := agentevidence.Block{ID: "b", Text: "解释", EvidenceIDs: []string{id}, Quotes: []agentevidence.Quote{{EvidenceID: id, Text: strings.Repeat("真实正文🙂", 100)}}}
	if !errors.Is(c.Set.ValidateBlock(b, true), agentevidence.ErrCitation) {
		t.Fatal("裁掉的正文可以引用")
	}
	other, _ := agentevidence.NewSet(testOther)
	if other.Add(c.Evidence[0]) == nil {
		t.Fatal("跨回合登记")
	}
	before := len(c.Evidence)
	if err := c.Search(agenttools.SearchResult{Items: []agenttools.ArticleRef{{ArticleID: 9, RevisionID: 10, Summary: "未保留候选"}}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(c.Evidence) != before {
		t.Fatal("超预算候选登记了证据")
	}
	full := collection(t, 8192)
	if err := full.Search(agenttools.SearchResult{Items: []agenttools.ArticleRef{{ArticleID: 1, RevisionID: 2, Summary: "摘要"}}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	a, _ := agentevidence.NewAssembler(full.Set, 100)
	if err := a.Add(agentevidence.Block{ID: "x", Text: "内容", EvidenceIDs: []string{full.Evidence[0].ID, full.Evidence[1].ID}}, false); err != nil {
		t.Fatal(err)
	}
	v, _ := a.Snapshot()
	if len(v.References) != 2 || v.References[0].Number != 1 || v.References[1].Number != 1 {
		t.Fatal("同修订未去重")
	}
}

func TestDecoderPacketBoundaries(t *testing.T) {
	c := collection(t, 8192)
	wire := header + blockLine("b1", "中文🙂分片", []string{c.Evidence[0].ID}) + blockLine("b2", "另一完整片段", []string{c.Evidence[0].ID}) + finish
	for _, step := range []int{1, 2, 7, len(wire)} {
		t.Run(fmt.Sprint(step), func(t *testing.T) {
			calls := 0
			d, _ := NewDecoder(c.Set, 100, true, func(a agentevidence.Answer, b agentevidence.Block) error {
				calls++
				if len(a.Blocks) != calls || b.ID != fmt.Sprintf("b%d", calls) {
					t.Fatal("回调顺序")
				}
				return nil
			})
			for i := 0; i < len(wire); i += step {
				end := min(i+step, len(wire))
				if err := d.Feed([]byte(wire[i:end])); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := d.Close(); err != nil {
				t.Fatal(err)
			}
			if calls != 2 {
				t.Fatal(calls)
			}
		})
	}
}

func TestDecoderRejectsMalformedAndKeepsPartial(t *testing.T) {
	c := collection(t, 8192)
	valid := blockLine("b", "解释", []string{c.Evidence[0].ID})
	for name, tail := range map[string]string{
		"重复字段": `{"type":"block","id":"x","text":"a","text":"b","evidence_ids":[]}`,
		"未知字段": `{"type":"block","id":"x","text":"a","evidence_ids":[],"url":"https://fake"}`,
		"嵌套重复": fmt.Sprintf(`{"type":"block","id":"x","text":"a","evidence_ids":[%q],"quotes":[{"evidence_id":%q,"text":"a","text":"b"}]}`, c.Evidence[0].ID, c.Evidence[0].ID),
		"重复头":  header, "缺少finish": "", "垃圾": finish + "garbage", "finish之后": finish + valid,
		"重复ID": valid, "空引用": blockLine("x", "a", []string{}), "伪造引用": blockLine("x", "a", []string{testOther}),
		"非法编码": "\xff\n", "未配对代理": `{"type":"block","id":"x","text":"\ud800","evidence_ids":[]}`,
		"null": `{"type":"block","id":"x","text":null,"evidence_ids":[]}`, "空行": "\n",
	} {
		t.Run(name, func(t *testing.T) {
			count := 0
			d, _ := NewDecoder(c.Set, 100, true, func(agentevidence.Answer, agentevidence.Block) error { count++; return nil })
			err := d.Feed([]byte(header + valid + tail))
			if err == nil {
				_, err = d.Close()
			}
			if err == nil {
				t.Fatal("接受了非法流")
			}
			a, p := d.Snapshot()
			if !p || len(a.Blocks) != 1 || count != 1 {
				t.Fatalf("partial 丢失: %+v %v %d", a, p, count)
			}
			if _, err := d.Close(); err == nil {
				t.Fatal("失败后可成功")
			}
		})
	}
	for _, wire := range []string{valid + header + finish, finish, `{"type":"header","version":1,"kind":"no_results"}` + "\n" + finish, header + finish} {
		d, _ := NewDecoder(c.Set, 100, false, nil)
		err := d.Feed([]byte(wire))
		if err == nil {
			_, err = d.Close()
		}
		if err == nil {
			t.Fatal("接受乱序/空回答/模型模板")
		}
	}
}

func TestDecoderBounds(t *testing.T) {
	c := collection(t, 8192)
	for _, n := range []int{MaxLineBytes, MaxLineBytes + 1} {
		d, _ := NewDecoder(c.Set, 100, false, nil)
		err := d.Feed([]byte(strings.Repeat(" ", n)))
		if (err != nil) != (n > MaxLineBytes) {
			t.Fatal("单行边界", n, err)
		}
	}
	d, _ := NewDecoder(c.Set, 100, false, nil)
	if d.Feed(make([]byte, MaxStreamBytes+1)) == nil {
		t.Fatal("累计字节边界")
	}
	// 合法但大量空白的记录仍计入累计预算。
	d, _ = NewDecoder(c.Set, 100, false, nil)
	if err := d.Feed([]byte(header)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		line := blockLine(fmt.Sprint(i), "文", []string{c.Evidence[0].ID})
		line = strings.Repeat(" ", MaxLineBytes-len(line)+1) + line
		err := d.Feed([]byte(line))
		if i < 3 && err != nil {
			t.Fatal(err)
		}
		if i == 3 && err == nil {
			t.Fatal("累计字节未限制")
		}
	}
	for _, chars := range []int{2, 3} {
		d, _ := NewDecoder(c.Set, 2, false, nil)
		err := d.Feed([]byte(header + blockLine("b", strings.Repeat("🙂", chars), []string{c.Evidence[0].ID}) + finish))
		if err == nil {
			_, err = d.Close()
		}
		if (err == nil) != (chars == 2) {
			t.Fatal("code point边界")
		}
	}
	for _, blocks := range []int{32, 33} {
		d, _ := NewDecoder(c.Set, 100, false, nil)
		wire := header
		for i := 0; i < blocks; i++ {
			wire += blockLine(fmt.Sprint(i), "文", []string{c.Evidence[0].ID})
		}
		wire += finish
		err := d.Feed([]byte(wire))
		if err == nil {
			_, err = d.Close()
		}
		if (err == nil) != (blocks == 32) {
			t.Fatal("片段数量边界")
		}
	}
}
