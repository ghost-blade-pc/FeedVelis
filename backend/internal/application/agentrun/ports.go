// Package agentrun 定义回合事务与执行边界；适配器不得透传模型框架事件。
package agentrun

import (
	"context"
	"time"

	conversationApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentconversation"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	conversation "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentconversation"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentevidence"
	run "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
)

const AcceptOperation = "agent.run.accept"

// Scope 显式携带所有权；仓储不能只用 run_id 读取私密载荷。
type Scope struct{ UserID, ConversationID, RunID string }
type Execution struct {
	Scope
	SessionID, OwnerToken string
}
type Boundary struct {
	CreatedAt time.Time
	ID        string
}
type Candidate struct{ Scope }
type Record struct {
	Run               run.Run
	Answer            *agentevidence.Answer
	Usage             run.Usage
	Partial           bool
	LastEventSequence int64
	EventBytes        int
	EventsExpired     bool
}

// Repository 的写方法只在 TxManager 内调用，并使用数据库当前时间。
// 锁顺序：账户 → 接受幂等 → 会话 → 回合 → 事件；普通执行不取得接受幂等锁。
// 每个 owner 写操作必须再次验证 running、owner_token、租约、deadline 及当前授权。
type AcceptanceRepository interface {
	DatabaseTime(context.Context) (time.Time, error)
	LockUser(context.Context, string) (conversationApp.UserState, error)
	LockConversation(context.Context, string, string) (conversation.Conversation, error)
	LockRun(context.Context, Scope) (Record, error)
	Message(context.Context, string, string) (conversation.Message, error)
	Attempts(context.Context, string, string) ([]run.Attempt, error)
	AccountActive(context.Context, string) (int, error)
	ConversationActive(context.Context, string) (int, error)
	InsertAccepted(context.Context, run.Run) error
	Find(context.Context, Scope) (Record, error)
	List(context.Context, string, string, *Boundary, int) ([]Record, error)
	Cleanup(context.Context, int) (int, error)
}

// Repository 包含生命周期写端口；接受和查询不依赖执行器启动。
type Repository interface {
	AcceptanceRepository
	Candidates(context.Context, int) ([]Candidate, error)
	Claim(context.Context, Scope, string) (run.Run, error)
	Renew(context.Context, Execution) error
	SaveUsage(context.Context, Execution, run.Usage) error
	SaveExecutionMetadata(context.Context, Execution, ExecutionMetadata) error
	AppendBlock(context.Context, Execution, BlockCommit) error
	AppendToolStatus(context.Context, Execution, string, string, run.Code) error
	Complete(context.Context, Execution, SuccessCommit) error
	Terminate(context.Context, Scope, run.State, run.Code) (Record, error)
	TerminateExecution(context.Context, Execution, run.State, run.Code, run.Usage) (Record, error)
	Maintain(context.Context, int) (int, error)
}

// Store 将事务入口与仓储组合，具体 SQL/事务类型不穿过端口。
type Store interface {
	Repository
	ports.TxManager
	conversationApp.ReadSnapshot
}

type BlockCommit struct {
	Block          agentevidence.Block
	Evidence       []agentevidence.Evidence
	References     []agentevidence.Reference
	Cards          []agentevidence.Card
	Events         []Event
	Constraints    run.Constraints
	RequireBody    bool
	MaxAnswerChars int
}
type SuccessCommit struct {
	Answer         agentevidence.Answer
	Assistant      conversation.Message
	Usage          run.Usage
	Terminal       Event
	Constraints    run.Constraints
	MaxMessages    int
	MaxAnswerChars int
}

// CurrentAuthorization 检查当前账户及原登录会话；访问令牌到期不属于执行授权。
// 事务内调用时必须使用同一事务快照，错误不可携带底层凭据或请求正文。
type CurrentAuthorization interface {
	CheckSession(context.Context, string, string) error
}
type ExecutionReader interface {
	ReadExecution(context.Context, Scope) (run.Run, time.Time, error)
}

type ArticleIdentity struct{ ArticleID, RevisionID int64 }
type ArticleFact struct {
	ArticleID, RevisionID int64
	Public                bool
	Keywords, Topics      []string
	SourceID              int64
	Card                  agentevidence.Card
}

// EvidenceFacts 一批在同一当前只读快照获取；缺失项视作不可见，错误时关闭读取。
// 数据库事实是唯一依据，缓存或搜索索引不能充当授权。
type EvidenceFacts interface {
	Current(context.Context, []ArticleIdentity) ([]ArticleFact, error)
}

type CompletedTurn struct {
	RunID    string
	Question conversation.Message
	Answer   agentevidence.Answer
}
type ContextSource interface {
	RecentSuccessful(context.Context, string, string, int) ([]CompletedTurn, error)
}
type PreferenceSource interface {
	Preferences(context.Context, string) ([]string, error)
}
type EmptyPreferences struct{}

func (EmptyPreferences) Preferences(context.Context, string) ([]string, error) {
	return []string{}, nil
}

type Context struct {
	Question         string
	History          []CompletedTurn
	Preferences      []string
	ContextArticleID int64
	Constraints      run.Constraints
}
type ExecutionRequest struct {
	Execution
	Context  Context
	Budget   *run.Budget
	Deadline time.Time
}
type ExecutionResult struct {
	Answer      agentevidence.Answer
	Usage       run.Usage
	Constraints run.Constraints
}

// OutputSink 只接收完整候选片段，由应用完成引用、当前事实及权限校验后保存。
// Runner 返回成功也不代表已提交成功助手；最终原子提交属于应用服务。
type OutputSink interface {
	SubmitBlock(context.Context, BlockCommit) error
}
type ToolProgressSink interface {
	ToolProgress(context.Context, string, string, run.Code) error
}
type UsageSink interface {
	UpdateUsage(context.Context, run.Usage) error
}
type ExecutionMetadata struct {
	ProfileVersion string           `json:"profile_version"`
	PromptVersion  string           `json:"prompt_version"`
	ToolVersion    string           `json:"tool_version"`
	Budget         run.BudgetLimits `json:"budget"`
}
type ContextBuilder interface {
	Build(context.Context, string, string, conversation.Message, int64, run.Constraints, int) (Context, error)
}
type Runner interface {
	Execute(context.Context, ExecutionRequest, OutputSink) (ExecutionResult, error)
}

type ToolManifest struct {
	Name, Version             string
	InputSchema, OutputSchema string
	Permission                string
	Effect                    string
	Timeout                   time.Duration
	MaxOutputBytes            int
}
type ToolCall struct {
	Name      string
	Arguments []byte
}
type ToolResult struct {
	Name      string
	Content   []byte
	Evidence  []agentevidence.Evidence
	Cards     []agentevidence.Card
	Empty     bool
	ErrorCode run.Code
}
type ToolInvoker interface {
	Manifests() []ToolManifest
	InvokeBatch(context.Context, Execution, []ToolCall, *run.Budget) ([]ToolResult, error)
}

type EventKind string

const (
	RunStatus     EventKind = "run.status"
	ToolStatus    EventKind = "tool.status"
	AnswerSources EventKind = "answer.sources"
	AnswerBlock   EventKind = "answer.block"
	RunTerminal   EventKind = "run.terminal"
)

func (k EventKind) Valid() bool {
	return k == RunStatus || k == ToolStatus || k == AnswerSources || k == AnswerBlock || k == RunTerminal
}

// Event 使用明确业务字段，避免 any/raw JSON 成为内部推理或原工具输出的旁路。
type Event struct {
	Version    int                       `json:"version"`
	RunID      string                    `json:"run_id"`
	Sequence   int64                     `json:"sequence"`
	Kind       EventKind                 `json:"kind"`
	State      run.State                 `json:"status,omitempty"`
	ToolName   string                    `json:"tool_name,omitempty"`
	ToolState  string                    `json:"tool_status,omitempty"`
	ErrorCode  run.Code                  `json:"error_code,omitempty"`
	Block      *agentevidence.Block      `json:"block,omitempty"`
	References []agentevidence.Reference `json:"references,omitempty"`
	Cards      []agentevidence.Card      `json:"cards,omitempty"`
	Partial    bool                      `json:"partial"`
	Redacted   bool                      `json:"redacted"`
}
type EventBatch struct {
	Events       []Event
	LastSequence int64
	Terminal     bool
	Expired      bool
}

// EventReader 在归属校验后读取有界批次；返回内容须经过当前可见性投影才能投递。
type EventReader interface {
	ReadEvents(context.Context, Scope, int64, int) (EventBatch, error)
}
type VisibilityProjector interface {
	Answer(context.Context, agentevidence.Answer) (agentevidence.Answer, error)
	Events(context.Context, []Event) ([]Event, error)
}

// Observation 不包含用户、回合、文章、Prompt、参数或任意错误文本。
type Observation struct {
	State      run.State
	ErrorCode  run.Code
	Usage      run.Usage
	FirstBlock time.Duration
	Duration   time.Duration
}
type Observer interface {
	ObserveRun(context.Context, Observation)
}
