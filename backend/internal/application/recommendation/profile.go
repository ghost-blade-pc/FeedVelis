package recommendation

import (
	"context"
)

const NotInterestedArticle = "not_interested_article"

type Evidence struct {
	Weight           int `json:"weight"`
	PositiveArticles int `json:"positive_articles"`
	NegativeArticles int `json:"negative_articles"`
}

type Profile struct {
	Excluded map[int64]string    `json:"excluded"`
	Topics   map[string]Evidence `json:"topics"`
	Sources  map[int64]Evidence  `json:"sources"`
}

// ProfileReader 是后续 recommend 消费的只读画像端口。
type ProfileReader interface {
	Profile(context.Context, string, []int64) (Profile, error)
}
