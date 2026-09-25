package searchprojection

import "errors"

var (
	// ErrIndexMissing 表示目标物理索引不存在。
	ErrIndexMissing = errors.New("搜索索引不存在")
	// ErrIndexNotInitialized 表示尚未执行索引初始化，没有当前服务索引。
	ErrIndexNotInitialized = errors.New("搜索索引尚未初始化")
	// ErrSchemaMismatch 表示实际 schema 身份与要求不一致；必须建立新索引而不是原地修改。
	ErrSchemaMismatch = errors.New("搜索索引 schema 身份不匹配")
	// ErrAliasConflict 表示别名指向了非预期索引，或写别名不唯一。
	ErrAliasConflict = errors.New("搜索索引别名冲突")
	// ErrRebuildConflict 表示已有活动重建或回滚窗口仍打开，不能开始新的重建。
	ErrRebuildConflict = errors.New("已存在活动索引重建")
	// ErrInvalidRebuildPhase 表示重建阶段转换不符合状态机。
	ErrInvalidRebuildPhase = errors.New("重建阶段转换无效")
	// ErrRebuildNotFound 表示指定的重建记录不存在。
	ErrRebuildNotFound = errors.New("索引重建记录不存在")
	// ErrUnsafeIndexTarget 表示拒绝删除被别名、活动 delivery 或回滚窗口引用的索引。
	ErrUnsafeIndexTarget = errors.New("拒绝操作仍被引用的搜索索引")
	// ErrNotConfigured 表示未配置 OpenSearch；此时投影组件保持禁用。
	ErrNotConfigured = errors.New("OpenSearch 未配置")
	// ErrVectorDimensionMismatch 表示 Embedding 维度与索引映射不一致，必须拒绝写入。
	ErrVectorDimensionMismatch = errors.New("向量维度与索引映射不一致")
	// ErrValidationFailed 表示重建校验未通过，必须拒绝别名切换。
	ErrValidationFailed = errors.New("索引重建校验未通过")
)
