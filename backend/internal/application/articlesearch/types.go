// Package articlesearch 编排公开文章搜索；本包不暴露 SQL、HTTP 或 OpenSearch SDK 类型。
package articlesearch

import (
	"math"
	"time"

	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

const QueryPlanVersion = 1

type Request struct {
	Q        string
	Keyword  *string
	Topic    *string
	SourceID *int64
	Limit    int
	Cursor   string
}

type Filters struct {
	Keyword  string `json:"keyword"`
	Topic    string `json:"topic"`
	SourceID int64  `json:"source_id"`
}

type Query struct {
	Q       string  `json:"q"`
	Filters Filters `json:"filters"`
	Limit   int     `json:"-"`
	Cursor  string  `json:"-"`
}

type SortPosition struct {
	Score       float64   `json:"score"`
	PublishedAt time.Time `json:"published_at"`
	ArticleID   int64     `json:"article_id"`
}

func (p SortPosition) Valid() bool {
	return !math.IsNaN(p.Score) && !math.IsInf(p.Score, 0) && !p.PublishedAt.IsZero() && p.ArticleID > 0
}

type Candidate struct {
	ArticleID int64
	Position  SortPosition
}

type Page struct {
	Items      []articleDomain.ListItem
	NextCursor *string
	HasMore    bool
}

type IndexRequest struct {
	Query     Query
	PITID     string
	After     *SortPosition
	Size      int
	KeepAlive time.Duration
}

type CandidateBatch struct {
	PITID      string
	Candidates []Candidate
	Exhausted  bool
}

type Config struct {
	PITKeepAlive            time.Duration
	CandidateBatchSize      int
	MaxCandidatesPerRequest int
}
