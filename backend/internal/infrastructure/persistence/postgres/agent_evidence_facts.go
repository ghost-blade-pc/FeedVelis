package postgres

import (
	"context"
	"strconv"
	"unicode/utf8"

	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/agentrun"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentevidence"
	"github.com/jackc/pgx/v5/pgxpool"
)

type AgentEvidenceFacts struct{ pool *pgxpool.Pool }

func NewAgentEvidenceFacts(pool *pgxpool.Pool) *AgentEvidenceFacts {
	return &AgentEvidenceFacts{pool: pool}
}

func cardPrefix(s string, n int) (string, bool) {
	if utf8.RuneCountInString(s) <= n {
		return s, false
	}
	return string([]rune(s)[:n]), true
}

// Current 用单条 SQL 读取当前公开性、修订、标签及卡片；事务内自动复用同一快照。
func (r *AgentEvidenceFacts) Current(ctx context.Context, identities []agentrun.ArticleIdentity) ([]agentrun.ArticleFact, error) {
	if len(identities) == 0 {
		return []agentrun.ArticleFact{}, nil
	}
	if len(identities) > agentevidence.MaxSources {
		return nil, agentevidence.ErrOutput
	}
	ids := make([]int64, len(identities))
	for i, id := range identities {
		ids[i] = id.ArticleID
	}
	rows, err := querier(ctx, r.pool).Query(ctx, `SELECT a.id,a.current_revision_id,a.status='published',COALESCE(g.keywords,'{}'::text[]),COALESCE(g.topics,'{}'::text[]),COALESCE(a.source_id,0),
a.origin_type,v.title,s.title,u.id::text,u.nickname,v.source_author_name,a.canonical_url,COALESCE(g.summary,v.excerpt),COALESCE(g.generation_method,'excerpt')`+publicFactJoins+` WHERE a.id=ANY($1::bigint[])`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []agentrun.ArticleFact{}
	for rows.Next() {
		var f agentrun.ArticleFact
		var sourceTitle, authorID, nickname, authorName, url *string
		if err := rows.Scan(&f.ArticleID, &f.RevisionID, &f.Public, &f.Keywords, &f.Topics, &f.SourceID, &f.Card.OriginType, &f.Card.Title, &sourceTitle, &authorID, &nickname, &authorName, &url, &f.Card.Summary, &f.Card.SummarySource); err != nil {
			return nil, err
		}
		if !f.Public {
			f.Card = agentevidence.Card{}
			result = append(result, f)
			continue
		}
		c := &f.Card
		c.ArticleID = f.ArticleID
		c.RevisionID = f.RevisionID
		c.Path = "/articles/" + strconv.FormatInt(f.ArticleID, 10)
		c.Title, c.MetadataTruncated = cardPrefix(c.Title, 256)
		var cut bool
		c.Summary, cut = cardPrefix(c.Summary, 1000)
		c.MetadataTruncated = c.MetadataTruncated || cut
		if c.OriginType == "rss" {
			title, cut := cardPrefix(valueOrEmpty(sourceTitle), 256)
			c.MetadataTruncated = c.MetadataTruncated || cut
			c.Source = &agentevidence.Source{ID: f.SourceID, Title: title}
			c.OriginalURL = url
			if authorName != nil {
				name, cut := cardPrefix(*authorName, 256)
				c.MetadataTruncated = c.MetadataTruncated || cut
				c.Author = &agentevidence.Author{Name: name}
			}
		} else {
			name, cut := cardPrefix(valueOrEmpty(nickname), 256)
			c.MetadataTruncated = c.MetadataTruncated || cut
			c.Author = &agentevidence.Author{ID: valueOrEmpty(authorID), Nickname: name}
		}
		result = append(result, f)
	}
	return result, rows.Err()
}
