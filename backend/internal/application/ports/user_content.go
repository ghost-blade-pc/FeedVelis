package ports

import (
	"context"
	"errors"
	"time"
)

var ErrArticleAssetUnavailable = errors.New("文章引用的资产不可用")

// ErrContentInvalid 标记用户稿件内容校验失败；接口层据此映射 ARTICLE_CONTENT_INVALID。
var ErrContentInvalid = errors.New("用户稿件内容无效")

// contentInvalidError 保留渲染器的具体校验原因，同时让 errors.Is(err, ErrContentInvalid) 成立。
type contentInvalidError struct{ reason error }

func (e contentInvalidError) Error() string { return e.reason.Error() }

func (e contentInvalidError) Unwrap() error { return e.reason }

func (e contentInvalidError) Is(target error) bool { return target == ErrContentInvalid }

// InvalidContent 把渲染器的校验失败标记为接口层可识别的内容错误；nil 原样返回。
func InvalidContent(reason error) error {
	if reason == nil {
		return nil
	}
	return contentInvalidError{reason: reason}
}

type UserContentMode int

const (
	UserContentDraft UserContentMode = iota
	UserContentPublish
)

type RenderedUserContent struct {
	Title     string
	Markdown  string
	HTML      string
	PlainText string
	Excerpt   string
	Hash      string
	AssetIDs  []string
	Sanitizer int
}

type UserContentRenderer interface {
	RenderUserContent(title, markdown string, mode UserContentMode) (RenderedUserContent, error)
}

// ArticleAssetBinding 描述一次内容修订对资产的引用需求。
// 限制随请求传入：绑定边界不得依赖调用方已经自行限制了数量与总量。
type ArticleAssetBinding struct {
	OwnerUserID   string
	ArticleID     int64
	RevisionID    int64
	AssetIDs      []string
	MaxImages     int
	MaxTotalBytes int64
	Now           time.Time
}

// ArticleAssets 批量校验资产已确认、属于作者且未绑定其他文章，并在同一事务绑定引用；
// 同时提供内容修订的资产引用读取，供作者私有详情展示当前修订实际引用的资产。
type ArticleAssets interface {
	BindArticleAssets(context.Context, ArticleAssetBinding) error
	ListRevisionAssetIDs(context.Context, int64) ([]string, error)
}
