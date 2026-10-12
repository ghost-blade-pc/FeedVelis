package agentorchestration

import (
	"bytes"
	"encoding/json"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/strictjson"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentevidence"
)

const MaxLineBytes = 64 * 1024
const MaxStreamBytes = 256 * 1024

// Decoder 只在完整行通过协议及引用校验后调用提交函数；EOF 后才允许完成。
// 成文模型只能产生 answer；无结果和澄清由服务端决策模板产生。
type Decoder struct {
	a                      *agentevidence.Assembler
	requireBody            bool
	onBlock                func(agentevidence.Answer, agentevidence.Block) error
	buffer                 []byte
	total                  int
	header, finish, closed bool
	err                    error
}

func NewDecoder(set *agentevidence.Set, maxChars int, requireBody bool, onBlock func(agentevidence.Answer, agentevidence.Block) error) (*Decoder, error) {
	a, err := agentevidence.NewAssembler(set, maxChars)
	if err != nil {
		return nil, err
	}
	return &Decoder{a: a, requireBody: requireBody, onBlock: onBlock}, nil
}
func (d *Decoder) fail(err error) error {
	if d.err == nil {
		d.err = err
	}
	d.a.Abort()
	return d.err
}
func (d *Decoder) Feed(chunk []byte) error {
	if d.err != nil {
		return d.err
	}
	if d.closed {
		return d.fail(agentevidence.ErrOutput)
	}
	if len(chunk) > MaxStreamBytes-d.total {
		return d.fail(agentevidence.ErrOutput)
	}
	d.total += len(chunk)
	// 限制每次追加到单行缓冲的大小，避免大 chunk 引起无界临时缓冲。
	for len(chunk) > 0 {
		i := bytes.IndexByte(chunk, '\n')
		end := len(chunk)
		if i >= 0 {
			end = i
		}
		if end > MaxLineBytes-len(d.buffer) {
			return d.fail(agentevidence.ErrOutput)
		}
		d.buffer = append(d.buffer, chunk[:end]...)
		if i < 0 {
			break
		}
		if err := d.line(d.buffer); err != nil {
			return d.fail(err)
		}
		d.buffer = d.buffer[:0]
		chunk = chunk[i+1:]
	}
	return nil
}

func (d *Decoder) line(line []byte) error {
	if d.finish {
		return agentevidence.ErrOutput
	}
	fields, err := strictjson.Object(line, MaxLineBytes, "type", "version", "kind", "id", "text", "evidence_ids", "quotes")
	if err != nil {
		return agentevidence.ErrOutput
	}
	var typ string
	if json.Unmarshal(fields["type"], &typ) != nil {
		return agentevidence.ErrOutput
	}
	if !d.header {
		var version int
		var kind string
		if typ != "header" || len(fields) != 3 || json.Unmarshal(fields["version"], &version) != nil || version != 1 || json.Unmarshal(fields["kind"], &kind) != nil || kind != "answer" {
			return agentevidence.ErrOutput
		}
		d.header = true
		return nil
	}
	if typ == "finish" {
		if len(fields) != 1 {
			return agentevidence.ErrOutput
		}
		d.finish = true
		return nil
	}
	if typ != "block" || len(fields) < 4 || len(fields) > 5 || fields["version"] != nil || fields["kind"] != nil {
		return agentevidence.ErrOutput
	}
	var b agentevidence.Block
	if json.Unmarshal(fields["id"], &b.ID) != nil || json.Unmarshal(fields["text"], &b.Text) != nil || json.Unmarshal(fields["evidence_ids"], &b.EvidenceIDs) != nil || b.EvidenceIDs == nil {
		return agentevidence.ErrOutput
	}
	// 数组元素也必须按严格 schema 校验，避免嵌套重复键和大小写宽松匹配。
	if raw, ok := fields["quotes"]; ok {
		var quotes []json.RawMessage
		if json.Unmarshal(raw, &quotes) != nil || quotes == nil {
			return agentevidence.ErrOutput
		}
		for _, raw := range quotes {
			qf, e := strictjson.Object(raw, MaxLineBytes, "evidence_id", "text")
			if e != nil || len(qf) != 2 {
				return agentevidence.ErrOutput
			}
			var q agentevidence.Quote
			if json.Unmarshal(qf["evidence_id"], &q.EvidenceID) != nil || json.Unmarshal(qf["text"], &q.Text) != nil {
				return agentevidence.ErrOutput
			}
			b.Quotes = append(b.Quotes, q)
		}
	}
	if err := d.a.Add(b, d.requireBody); err != nil {
		return err
	}
	if d.onBlock != nil {
		a, _ := d.a.Snapshot()
		if err := d.onBlock(a, b); err != nil {
			return err
		}
	}
	return nil
}

func (d *Decoder) Close() (agentevidence.Answer, error) {
	if d.err != nil {
		return agentevidence.Answer{}, d.err
	}
	if d.closed {
		return agentevidence.Answer{}, d.fail(agentevidence.ErrOutput)
	}
	d.closed = true
	if len(d.buffer) > 0 {
		if err := d.line(d.buffer); err != nil {
			return agentevidence.Answer{}, d.fail(err)
		}
	}
	if !d.header || !d.finish {
		return agentevidence.Answer{}, d.fail(agentevidence.ErrOutput)
	}
	if err := d.a.Finish(); err != nil {
		return agentevidence.Answer{}, d.fail(err)
	}
	return d.a.Completed()
}
func (d *Decoder) Snapshot() (agentevidence.Answer, bool) { return d.a.Snapshot() }
