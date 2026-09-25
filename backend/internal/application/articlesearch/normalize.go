package articlesearch

import (
	"errors"
	"unicode/utf8"

	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

const (
	maxQueryRunes  = 200
	maxFilterRunes = 64
	defaultLimit   = 20
	maxLimit       = 50
)

func Normalize(request Request) (Query, error) {
	q := articleDomain.NormalizeText(request.Q)
	if q == "" || utf8.RuneCountInString(q) > maxQueryRunes {
		return Query{}, controlled(CodeValidationFailed, errors.New("q 必须为 1 到 200 个 Unicode 字符"))
	}
	query := Query{Q: q, Cursor: request.Cursor, Limit: request.Limit}
	if query.Limit == 0 {
		query.Limit = defaultLimit
	}
	if query.Limit < 1 || query.Limit > maxLimit {
		return Query{}, controlled(CodeValidationFailed, errors.New("limit 必须介于 1 和 50"))
	}
	var err error
	if query.Filters.Keyword, err = normalizeFilter("keyword", request.Keyword); err != nil {
		return Query{}, err
	}
	if query.Filters.Topic, err = normalizeFilter("topic", request.Topic); err != nil {
		return Query{}, err
	}
	if request.SourceID != nil {
		if *request.SourceID <= 0 {
			return Query{}, controlled(CodeValidationFailed, errors.New("source_id 必须为正整数"))
		}
		query.Filters.SourceID = *request.SourceID
	}
	return query, nil
}

func normalizeFilter(name string, raw *string) (string, error) {
	if raw == nil {
		return "", nil
	}
	value := articleDomain.NormalizeText(*raw)
	if value == "" || utf8.RuneCountInString(value) > maxFilterRunes {
		return "", controlled(CodeValidationFailed, errors.New(name+" 必须为 1 到 64 个 Unicode 字符"))
	}
	return value, nil
}
