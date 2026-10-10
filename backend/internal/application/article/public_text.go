package article

import (
	"context"

	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

// PublicText 的卡片、修订和纯文本必须来自同一当前公开读取视图。
type PublicText struct {
	Item      articleDomain.ListItem
	Content   string
	Truncated bool
}

type PublicArticleTextReader interface {
	ReadPublicText(context.Context, int64, int) (PublicText, error)
}
