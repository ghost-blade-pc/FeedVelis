package agentrun

import "sync"

const ModelProtocolOverhead = 256

type BudgetLimits struct {
	ModelCalls    int
	ToolCalls     int
	PerCallOutput int64
	TotalOutput   int64
	AuditTokens   int64
	InputBytes    int
}

func DefaultBudgetLimits() BudgetLimits {
	return BudgetLimits{ModelCalls: 4, ToolCalls: 6, PerCallOutput: 2048, TotalOutput: 8192, AuditTokens: 65536, InputBytes: 49152}
}

type Usage struct {
	ModelCalls   int    `json:"model_calls"`
	ToolCalls    int    `json:"tool_calls"`
	InputTokens  *int64 `json:"input_tokens"`
	OutputTokens *int64 `json:"output_tokens"`
	TotalTokens  *int64 `json:"total_tokens"`
	Complete     bool   `json:"usage_complete"`
}

type ModelUsage struct{ Input, Output, Total *int64 }
type ModelReservation struct {
	ID            int
	Input, Output int64
}

// Budget 先预留再执行。调用失败或未报告完整 usage 不退还预留；一个预算只属于一个回合。
type Budget struct {
	mu                                  sync.Mutex
	limits                              BudgetLimits
	models, tools                       int
	input, output, audit                int64
	knownInput, knownOutput, knownTotal int64
	complete                            bool
	exhausted                           bool
	pending                             map[int]ModelReservation
}

func NewBudget(limits BudgetLimits) (*Budget, error) {
	if limits.ModelCalls < 2 || limits.ToolCalls < 1 || limits.PerCallOutput < 1 || limits.TotalOutput < limits.PerCallOutput ||
		limits.AuditTokens <= limits.PerCallOutput || limits.InputBytes < 1 {
		return nil, ErrInvalidInput
	}
	return &Budget{limits: limits, complete: true, pending: make(map[int]ModelReservation)}, nil
}

// ReserveModel 的 discovery=true 为独立成文保留最后一次模型调用。
func (b *Budget) ReserveModel(inputBytes int, discovery bool) (ModelReservation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if inputBytes < 0 {
		return ModelReservation{}, ErrInvalidInput
	}
	if inputBytes > b.limits.InputBytes {
		return ModelReservation{}, &Error{CodeContextLimit}
	}
	maxCalls := b.limits.ModelCalls
	if discovery {
		maxCalls--
	}
	input := int64(inputBytes) + ModelProtocolOverhead
	output := b.limits.PerCallOutput
	if b.exhausted || b.models >= maxCalls || output > b.limits.TotalOutput-b.output ||
		input > b.limits.AuditTokens-b.audit || output > b.limits.AuditTokens-b.audit-input {
		return ModelReservation{}, ErrBudget
	}
	b.models++
	reservation := ModelReservation{ID: b.models, Input: input, Output: output}
	b.pending[reservation.ID] = reservation
	b.input += input
	b.output += output
	b.audit += input + output
	return reservation, nil
}

// SettleModel 只接受本预算尚未结算的预留；nil usage 表示未知，并保留全部预留。
func (b *Budget) SettleModel(reservation ModelReservation, usage *ModelUsage) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	original, exists := b.pending[reservation.ID]
	if !exists || original != reservation {
		return ErrInvalidInput
	}
	delete(b.pending, reservation.ID)
	if usage == nil || usage.Input == nil || usage.Output == nil || usage.Total == nil {
		b.complete = false
		// 部分 usage 仍须检测 Provider 超额，不能用缺少字段绕过限制。
		if usage != nil && ((usage.Input != nil && (*usage.Input < 0 || *usage.Input > reservation.Input)) ||
			(usage.Output != nil && (*usage.Output < 0 || *usage.Output > reservation.Output)) ||
			(usage.Total != nil && (*usage.Total < 0 || *usage.Total > reservation.Input+reservation.Output))) {
			b.exhausted = true
			return ErrBudget
		}
		return nil
	}
	input, output, total := *usage.Input, *usage.Output, *usage.Total
	if input < 0 || output < 0 || total < input || total-input != output {
		b.complete = false
		b.exhausted = true
		return &Error{CodeModelIncompatible}
	}
	b.input += input - reservation.Input
	b.output += output - reservation.Output
	b.audit += total - reservation.Input - reservation.Output
	b.knownInput += input
	b.knownOutput += output
	b.knownTotal += total
	if output > reservation.Output || b.output > b.limits.TotalOutput || b.audit > b.limits.AuditTokens {
		b.exhausted = true
		return ErrBudget
	}
	return nil
}

// ReserveTools 在整批开始前检查并计数；参数非法、未登记工具及失败调用也消耗名额。
func (b *Budget) ReserveTools(count int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if count < 1 {
		return ErrInvalidInput
	}
	if b.exhausted || count > b.limits.ToolCalls-b.tools {
		return ErrBudget
	}
	b.tools += count
	return nil
}

func (b *Budget) Usage() Usage {
	b.mu.Lock()
	defer b.mu.Unlock()
	usage := Usage{ModelCalls: b.models, ToolCalls: b.tools, Complete: b.complete && len(b.pending) == 0}
	if usage.Complete {
		input, output, total := b.knownInput, b.knownOutput, b.knownTotal
		usage.InputTokens, usage.OutputTokens, usage.TotalTokens = &input, &output, &total
	}
	return usage
}
