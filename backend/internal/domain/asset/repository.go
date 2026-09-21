package asset

import "context"

// Usage 是用户当前计入额度的字节总量与待确认资产数量。
type Usage struct {
	CountedBytes int64
	PendingCount int
}

// Repository 持久化资产聚合，并提供文章修订引用与匿名授权所需的查询。
// 实现必须在调用方事务内生效，且不得把凭据或对象存储细节暴露给上层。
type Repository interface {
	Create(context.Context, Asset) error
	Get(context.Context, string) (Asset, error)
	Save(context.Context, Asset) error

	// LockOwner 在同一事务内串行化该用户的资产记账，使额度检查与计入不会并发穿透。
	LockOwner(context.Context, string) error
	Usage(context.Context, string) (Usage, error)

	// ListRevisionAssetIDs 返回某个内容修订引用的资产标识。
	ListRevisionAssetIDs(context.Context, int64) ([]string, error)
	// IsPubliclyReferenced 判断资产是否被某篇文章的当前 published 修订引用。
	IsPubliclyReferenced(context.Context, string) (bool, error)
}
