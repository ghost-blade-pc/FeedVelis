package ports

import "context"

// TxManager 为需要跨多个 Repository 的用例提供技术事务边界。
// 当前 ArticleRepository.Upsert 自身保证 Article 与 ArticleContent 原子写入。
type TxManager interface {
	WithinTransaction(context.Context, func(context.Context) error) error
}

// AfterCommitRegistrar 登记到最外层事务；同 actionID 去重，回调失败不改变已提交结果。
type AfterCommitRegistrar interface {
	RegisterAfterCommit(context.Context, string, func(context.Context) error) error
}

type PublicReadInvalidator interface {
	ScheduleLatestInvalidation(context.Context) error
}
