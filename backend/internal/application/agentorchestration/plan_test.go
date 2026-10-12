package agentorchestration

import "testing"

func TestControlledPlanConstraintsAndCapabilityBoundary(t *testing.T) {
	p, err := DecodePlan([]byte(`{"constraints":{"keyword":" Go ","source_id":7},"clarification":"","require_body":true,"reference_number":1}`))
	if err != nil || p.Constraints.Keyword != "Go" || p.Constraints.SourceID != 7 || !p.RequireBody || p.ReferenceNumber == nil || *p.ReferenceNumber != 1 {
		t.Fatal(p, err)
	}
	for _, reason := range []string{"unsupported_filter", "unsupported_capability", "missing_task", "ambiguous_reference"} {
		p, err := DecodePlan([]byte(`{"constraints":{},"clarification":"` + reason + `","require_body":false}`))
		if err != nil || p.Clarification != ClarificationReason(reason) {
			t.Fatal(p, err)
		}
	}
	for _, raw := range []string{`{"constraints":{"since":"yesterday"},"clarification":"","require_body":false}`, `{"constraints":{},"clarification":"already_saved","require_body":false}`, `{"constraints":{},"clarification":"","require_body":null}`, `{"constraints":{},"clarification":"","require_body":false,"require_body":true}`, `{"constraints":{"keyword":"Go","keyword":"secret"},"clarification":"","require_body":false}`, `{"constraints":{},"clarification":"","require_body":false,"user_id":"other"}`} {
		if _, err := DecodePlan([]byte(raw)); err == nil {
			t.Fatal("不受控候选被接受", raw)
		}
	}
}
