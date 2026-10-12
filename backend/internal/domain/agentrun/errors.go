package agentrun

// Code 只承载固定分类，禁止放入 Provider 文本或私密内容。
type Code string

const (
	CodeInvalidInput          Code = "VALIDATION_FAILED"
	CodeNotFound              Code = "AGENT_RUN_NOT_FOUND"
	CodeMessageNotFound       Code = "AGENT_MESSAGE_NOT_FOUND"
	CodeInputConflict         Code = "AGENT_RUN_INPUT_CONFLICT"
	CodeAttemptLimit          Code = "AGENT_RUN_ATTEMPT_LIMIT_EXCEEDED"
	CodeInProgress            Code = "AGENT_RUN_IN_PROGRESS"
	CodeMessageLimit          Code = "AGENT_MESSAGE_LIMIT_EXCEEDED"
	CodeGenerationUnavailable Code = "AGENT_GENERATION_UNAVAILABLE"
	CodeStateConflict         Code = "AGENT_RUN_STATE_CONFLICT"
	CodeExecutorLost          Code = "AGENT_EXECUTOR_LOST"
	CodeExecutorStopped       Code = "AGENT_EXECUTOR_STOPPED"
	CodeTimeout               Code = "AGENT_TIMEOUT"
	CodeCanceled              Code = "AGENT_CANCELED"
	CodeAuthInvalid           Code = "AUTH_SESSION_INVALID"
	CodeBudgetExceeded        Code = "AGENT_BUDGET_EXCEEDED"
	CodeContextLimit          Code = "AGENT_CONTEXT_LIMIT_EXCEEDED"
	CodeCitationInvalid       Code = "AGENT_CITATION_INVALID"
	CodeEvidenceUnavailable   Code = "AGENT_EVIDENCE_UNAVAILABLE"
	CodeEvidenceStale         Code = "AGENT_EVIDENCE_STALE"
	CodeOutputInvalid         Code = "AGENT_OUTPUT_INVALID"
	CodeModelUnavailable      Code = "AGENT_MODEL_UNAVAILABLE"
	CodeModelIncompatible     Code = "AGENT_MODEL_INCOMPATIBLE"
	CodeToolRejected          Code = "AGENT_TOOL_REJECTED"
	CodeToolFailed            Code = "AGENT_TOOL_FAILED"
	CodeDependencyUnavailable Code = "DEPENDENCY_UNAVAILABLE"
	CodeInternal              Code = "INTERNAL_ERROR"
)

func (c Code) Valid() bool {
	switch c {
	case CodeInvalidInput, CodeNotFound, CodeMessageNotFound, CodeInputConflict, CodeAttemptLimit, CodeInProgress, CodeMessageLimit,
		CodeGenerationUnavailable, CodeStateConflict, CodeExecutorLost, CodeExecutorStopped, CodeTimeout, CodeCanceled,
		CodeAuthInvalid, CodeBudgetExceeded, CodeContextLimit, CodeCitationInvalid, CodeEvidenceUnavailable,
		CodeEvidenceStale, CodeOutputInvalid, CodeModelUnavailable, CodeModelIncompatible, CodeToolRejected,
		CodeToolFailed, CodeDependencyUnavailable, CodeInternal:
		return true
	default:
		return false
	}
}

type Error struct{ Code Code }

func (e *Error) Error() string { return string(e.Code) }

var (
	ErrInvalidInput    = &Error{CodeInvalidInput}
	ErrNotFound        = &Error{CodeNotFound}
	ErrMessageNotFound = &Error{CodeMessageNotFound}
	ErrInputConflict   = &Error{CodeInputConflict}
	ErrAttemptLimit    = &Error{CodeAttemptLimit}
	ErrInProgress      = &Error{CodeInProgress}
	ErrMessageLimit    = &Error{CodeMessageLimit}
	ErrStateConflict   = &Error{CodeStateConflict}
	ErrExecutionLost   = &Error{CodeExecutorLost}
	ErrDeadline        = &Error{CodeTimeout}
	ErrBudget          = &Error{CodeBudgetExceeded}
)
