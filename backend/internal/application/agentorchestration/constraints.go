package agentorchestration

import (
	"encoding/json"
	"sync"

	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentrun"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agenttools"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/strictjson"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
)

// ConstraintPolicy 每轮单独构造；首轮受控候选只能锁定一次，显式条件优先。
type ConstraintPolicy struct {
	mu              sync.Mutex
	explicit, value agentrun.Constraints
	locked          bool
}

func NewConstraintPolicy(explicit agentrun.Constraints) *ConstraintPolicy {
	return &ConstraintPolicy{explicit: explicit, value: explicit}
}
func (p *ConstraintPolicy) Lock(candidate agentrun.Constraints) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.locked {
		return &agentrun.Error{Code: agentrun.CodeToolRejected}
	}
	// 复用接受命令的规范化语义，不接受工具未支持的条件。
	in := agent.ConstraintsInput{}
	if candidate.Keyword != "" {
		in.Keyword = &candidate.Keyword
	}
	if candidate.Topic != "" {
		in.Topic = &candidate.Topic
	}
	if candidate.SourceID != 0 {
		in.SourceID = &candidate.SourceID
	}
	command, err := agent.NormalizeCommand(agent.Command{MessageID: "82000000-0000-0000-0000-000000000001", Constraints: &in})
	if err != nil {
		return err
	}
	v := command.Constraints
	if p.explicit.Keyword != "" {
		v.Keyword = p.explicit.Keyword
	}
	if p.explicit.Topic != "" {
		v.Topic = p.explicit.Topic
	}
	if p.explicit.SourceID != 0 {
		v.SourceID = p.explicit.SourceID
	}
	p.value = v
	p.locked = true
	return nil
}
func (p *ConstraintPolicy) Value() agentrun.Constraints {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.value
}
func (p *ConstraintPolicy) Search(body []byte) ([]byte, error) {
	var in agenttools.SearchInput
	if strictjson.Decode(body, 16384, &in, "q", "keyword", "topic", "source_id", "limit") != nil {
		return nil, &agentrun.Error{Code: agentrun.CodeToolRejected}
	}
	c := p.Value()
	if c.Keyword != "" {
		if in.Keyword != nil && *in.Keyword != c.Keyword {
			return nil, &agentrun.Error{Code: agentrun.CodeToolRejected}
		}
		in.Keyword = &c.Keyword
	}
	if c.Topic != "" {
		if in.Topic != nil && *in.Topic != c.Topic {
			return nil, &agentrun.Error{Code: agentrun.CodeToolRejected}
		}
		in.Topic = &c.Topic
	}
	if c.SourceID != 0 {
		if in.SourceID != nil && *in.SourceID != c.SourceID {
			return nil, &agentrun.Error{Code: agentrun.CodeToolRejected}
		}
		in.SourceID = &c.SourceID
	}
	return json.Marshal(in)
}
func (p *ConstraintPolicy) Hard() bool {
	c := p.Value()
	return c.Keyword != "" || c.Topic != "" || c.SourceID != 0
}
