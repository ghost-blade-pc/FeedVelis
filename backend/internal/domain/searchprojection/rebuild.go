package searchprojection

import "time"

// Phase 是索引重建的持久化阶段。
// 重建不覆盖当前读索引：快照与追赶都在候选索引上完成，只有校验通过才做一次别名切换。
type Phase string

const (
	PhaseSnapshot  Phase = "snapshot"
	PhaseCatchUp   Phase = "catchup"
	PhaseValidate  Phase = "validate"
	PhaseValidated Phase = "validated"
	PhaseCutover   Phase = "cutover"
	PhaseServing   Phase = "serving"
	PhaseCompleted Phase = "completed"
	PhaseAbandoned Phase = "abandoned"
	PhaseFailed    Phase = "failed"
)

// phaseRank 只描述线性前进顺序；放弃与失败是任何未结束阶段都能进入的旁路终态。
var phaseRank = map[Phase]int{
	PhaseSnapshot:  0,
	PhaseCatchUp:   1,
	PhaseValidate:  2,
	PhaseValidated: 3,
	PhaseCutover:   4,
	PhaseServing:   5,
	PhaseCompleted: 6,
}

// Active 表示该阶段占用「同一逻辑索引只允许一个活动重建」的配额。
// serving 在回滚窗口内仍持续双写新旧索引，因此同样占用配额，直到窗口被显式关闭。
func (p Phase) Active() bool {
	_, ok := phaseRank[p]
	return ok && p != PhaseCompleted
}

// Terminal 表示阶段已经结束，不会再自动推进。
func (p Phase) Terminal() bool {
	return p == PhaseCompleted || p == PhaseAbandoned || p == PhaseFailed
}

// CanTransition 描述重建阶段状态机。
// 只能逐级前进或原地重复；校验阶段发现落后 delivery 时允许退回 catchup 重新追赶；
// 任何未结束的阶段都能显式放弃或标记失败，已失败的候选只能再被显式放弃。
func (p Phase) CanTransition(next Phase) bool {
	if p.Terminal() {
		return p == PhaseFailed && next == PhaseAbandoned
	}
	if next == PhaseAbandoned || next == PhaseFailed {
		return true
	}
	current, ok := phaseRank[p]
	if !ok {
		return false
	}
	target, ok := phaseRank[next]
	if !ok {
		return false
	}
	switch {
	case target == current:
		return true
	case target == current+1:
		return true
	case p == PhaseValidate && next == PhaseCatchUp:
		return true
	case p == PhaseValidated && (next == PhaseCatchUp || next == PhaseValidate):
		return true
	default:
		return false
	}
}

// ValidationReport 是切换前的校验报告：可见文档数、落后 delivery，以及确定性抽样的比对结果。
// 任一条件不满足都必须拒绝别名切换，并保留当前索引继续服务。
type ValidationReport struct {
	PublicDocuments    int64
	CandidateDocuments int64
	LaggingDeliveries  int64
	Sampled            int
	Mismatches         []string
	ReportedAt         time.Time
}

// Passed 判断候选索引是否满足切换条件。
func (r ValidationReport) Passed() bool {
	return r.PublicDocuments == r.CandidateDocuments && r.LaggingDeliveries == 0 && len(r.Mismatches) == 0
}
