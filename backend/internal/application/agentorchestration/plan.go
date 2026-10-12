package agentorchestration

import (
	"encoding/json"

	agent "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentrun"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/strictjson"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentevidence"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
)

type Plan struct {
	Constraints     agentrun.Constraints
	Clarification   ClarificationReason
	RequireBody     bool
	ReferenceNumber *int
}

// DecodePlan 解析第一轮模型给出的受控任务候选；不接受自由文本作为能力或筛选事实。
func DecodePlan(raw []byte) (Plan, error) {
	fields, err := strictjson.Object(raw, 16384, "constraints", "clarification", "require_body", "reference_number")
	if err != nil || len(fields) < 3 {
		return Plan{}, agentevidence.ErrOutput
	}
	var p Plan
	if json.Unmarshal(fields["clarification"], &p.Clarification) != nil || json.Unmarshal(fields["require_body"], &p.RequireBody) != nil {
		return Plan{}, agentevidence.ErrOutput
	}
	if p.Clarification != "" {
		if _, ok := clarifications[p.Clarification]; !ok {
			return Plan{}, agentevidence.ErrOutput
		}
	}
	command := append([]byte(`{"message_id":"82000000-0000-0000-0000-000000000001","constraints":`), fields["constraints"]...)
	command = append(command, '}')
	in, err := agent.DecodeCommand(command)
	if err != nil {
		return Plan{}, agentevidence.ErrOutput
	}
	normalized, err := agent.NormalizeCommand(in)
	if err != nil {
		return Plan{}, agentevidence.ErrOutput
	}
	p.Constraints = normalized.Constraints
	if raw, ok := fields["reference_number"]; ok {
		var n int
		if json.Unmarshal(raw, &n) != nil || n < 0 || n > agentevidence.MaxSources {
			return Plan{}, agentevidence.ErrOutput
		}
		p.ReferenceNumber = &n
	}
	return p, nil
}
