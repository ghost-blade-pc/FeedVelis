package ports

import "context"

// TxManager 为需要跨多个 Repository 的用例提供技术事务边界。
// 当前 ArticleRepository.Upsert 自身保证 Article 与 ArticleContent 原子写入。
type TxManager interface {
	WithinTransaction(context.Context, func(context.Context) error) error
}
