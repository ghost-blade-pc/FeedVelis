package searchprojection

import "testing"

func TestPhaseActiveAndTerminal(t *testing.T) {
	active := []Phase{PhaseSnapshot, PhaseCatchUp, PhaseValidate, PhaseValidated, PhaseCutover, PhaseServing}
	for _, phase := range active {
		if !phase.Active() {
			t.Fatalf("%s 必须占用活动重建配额", phase)
		}
		if phase.Terminal() {
			t.Fatalf("%s 不是终态", phase)
		}
	}
	for _, phase := range []Phase{PhaseCompleted, PhaseAbandoned, PhaseFailed} {
		if phase.Active() {
			t.Fatalf("%s 不得占用活动重建配额", phase)
		}
		if !phase.Terminal() {
			t.Fatalf("%s 必须是终态", phase)
		}
	}
	if Phase("unknown").Active() || Phase("unknown").Terminal() {
		t.Fatal("未知阶段既不是活动也不是终态")
	}
}

func TestPhaseTransitionsFollowProtocol(t *testing.T) {
	all := []Phase{PhaseSnapshot, PhaseCatchUp, PhaseValidate, PhaseValidated, PhaseCutover, PhaseServing, PhaseCompleted, PhaseAbandoned, PhaseFailed}
	allowed := map[Phase]map[Phase]bool{
		PhaseSnapshot:  {PhaseSnapshot: true, PhaseCatchUp: true, PhaseAbandoned: true, PhaseFailed: true},
		PhaseCatchUp:   {PhaseCatchUp: true, PhaseValidate: true, PhaseAbandoned: true, PhaseFailed: true},
		PhaseValidate:  {PhaseValidate: true, PhaseValidated: true, PhaseCatchUp: true, PhaseAbandoned: true, PhaseFailed: true},
		PhaseValidated: {PhaseValidated: true, PhaseValidate: true, PhaseCutover: true, PhaseCatchUp: true, PhaseAbandoned: true, PhaseFailed: true},
		PhaseCutover:   {PhaseCutover: true, PhaseServing: true, PhaseAbandoned: true, PhaseFailed: true},
		PhaseServing:   {PhaseServing: true, PhaseCompleted: true, PhaseAbandoned: true, PhaseFailed: true},
		PhaseCompleted: {},
		PhaseAbandoned: {},
		PhaseFailed:    {PhaseAbandoned: true},
	}
	for _, from := range all {
		for _, to := range all {
			if got := from.CanTransition(to); got != allowed[from][to] {
				t.Fatalf("%s → %s = %t，期望 %t", from, to, got, allowed[from][to])
			}
		}
	}
}

func TestPhaseRejectsSkippingAndTerminalChanges(t *testing.T) {
	// 不得跳过追赶直接切换别名。
	if PhaseSnapshot.CanTransition(PhaseCutover) {
		t.Fatal("snapshot 不得直接进入 cutover")
	}
	// 已完成的阶段不得再回到活动重建。
	if PhaseCompleted.CanTransition(PhaseServing) || PhaseCompleted.CanTransition(PhaseAbandoned) {
		t.Fatal("已完成阶段不得再转换")
	}
	// 已放弃的候选不得重新激活。
	if PhaseAbandoned.CanTransition(PhaseSnapshot) {
		t.Fatal("已放弃阶段不得重新激活")
	}
}
