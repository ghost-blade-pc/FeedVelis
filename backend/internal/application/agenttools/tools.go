// Package agenttools 提供只接受可信账户身份的三个内部文章只读工具。
package agenttools

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	articleApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/article"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/recommendation"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/strictjson"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/account"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

type Code string

const (
	ValidationFailed      Code = "VALIDATION_FAILED"
	ArticleNotFound       Code = "ARTICLE_NOT_FOUND"
	SearchUnavailable     Code = "SEARCH_UNAVAILABLE"
	DependencyUnavailable Code = "DEPENDENCY_UNAVAILABLE"
	ToolTimeout           Code = "TOOL_TIMEOUT"
	ToolCanceled          Code = "TOOL_CANCELED"
	InternalError         Code = "INTERNAL_ERROR"
)

// Error 不携带底层异常文本，不能将依赖响应或查询带入日志。
type Error struct{ Code Code }

func (e *Error) Error() string { return string(e.Code) }
func fail(code Code) error     { return &Error{Code: code} }
func CodeOf(err error) Code {
	var value *Error
	if errors.As(err, &value) {
		return value.Code
	}
	return InternalError
}

type Caller struct{ UserID string }
type SearchInput struct {
	Q        *string `json:"q"`
	Keyword  *string `json:"keyword,omitempty"`
	Topic    *string `json:"topic,omitempty"`
	SourceID *int64  `json:"source_id,omitempty"`
	Limit    *int    `json:"limit,omitempty"`
}
type RecommendInput struct {
	Limit *int `json:"limit,omitempty"`
}
type GetInput struct {
	ArticleID *int64 `json:"article_id"`
	MaxChars  *int   `json:"max_chars,omitempty"`
}
type Source struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}
type Author struct {
	ID       string `json:"id,omitempty"`
	Nickname string `json:"nickname,omitempty"`
	Name     string `json:"name,omitempty"`
}
type ArticleRef struct {
	ArticleID         int64                    `json:"article_id"`
	RevisionID        int64                    `json:"revision_id"`
	Title             string                   `json:"title"`
	OriginType        articleDomain.OriginType `json:"origin_type"`
	Source            *Source                  `json:"source"`
	Author            *Author                  `json:"author"`
	Path              string                   `json:"path"`
	OriginalURL       *string                  `json:"original_url"`
	Summary           string                   `json:"summary"`
	SummarySource     string                   `json:"summary_source"`
	MetadataTruncated bool                     `json:"metadata_truncated"`
}
type SearchResult struct {
	Items     []ArticleRef `json:"items"`
	Truncated bool         `json:"truncated"`
}
type RecommendedArticle struct {
	ArticleRef
	Reason string `json:"recommendation_reason"`
}
type RecommendResult struct {
	Items         []RecommendedArticle `json:"items"`
	Truncated     bool                 `json:"truncated"`
	Mode          string               `json:"mode"`
	Degraded      bool                 `json:"degraded"`
	DegradeReason *string              `json:"degrade_reason"`
}
type GetResult struct {
	ArticleRef
	Content   string `json:"content"`
	Truncated bool   `json:"truncated"`
}

type Searcher interface {
	SearchOnce(context.Context, articlesearch.Request) (articlesearch.Page, error)
}
type Recommender interface {
	GetOnce(context.Context, string, int) (recommendation.Page, error)
}
type Observer interface {
	ObserveTool(context.Context, string, Code, bool, int, bool, time.Duration)
}
type Config struct {
	Timeout        time.Duration
	MaxOutputBytes int
}
type Service struct {
	search    Searcher
	recommend Recommender
	text      articleApp.PublicArticleTextReader
	config    Config
	observer  Observer
}

func NewService(search Searcher, recommend Recommender, text articleApp.PublicArticleTextReader, config Config) *Service {
	if config.Timeout <= 0 {
		config.Timeout = 5 * time.Second
	}
	if config.MaxOutputBytes <= 0 {
		config.MaxOutputBytes = 128 * 1024
	}
	return &Service{search: search, recommend: recommend, text: text, config: config}
}
func (s *Service) WithObserver(observer Observer) *Service { s.observer = observer; return s }

func callerID(c Caller) (string, error) {
	id, err := account.ParseUUID(c.UserID)
	if err != nil || id.IsZero() {
		return "", fail(ValidationFailed)
	}
	return id.String(), nil
}
func limitValue(value *int) (int, error) {
	if value == nil {
		return 5, nil
	}
	if *value < 1 || *value > 10 {
		return 0, fail(ValidationFailed)
	}
	return *value, nil
}
func validText(text *string) bool {
	return text != nil && utf8.ValidString(*text) && !strings.ContainsRune(*text, 0)
}

func (s *Service) SearchJSON(ctx context.Context, caller Caller, body []byte) (SearchResult, error) {
	var input SearchInput
	if strictjson.Decode(body, 16384, &input, "q", "keyword", "topic", "source_id", "limit") != nil {
		return SearchResult{}, fail(ValidationFailed)
	}
	return s.Search(ctx, caller, input)
}
func (s *Service) RecommendJSON(ctx context.Context, caller Caller, body []byte) (RecommendResult, error) {
	var input RecommendInput
	if strictjson.Decode(body, 1024, &input, "limit") != nil {
		return RecommendResult{}, fail(ValidationFailed)
	}
	return s.Recommend(ctx, caller, input)
}
func (s *Service) GetJSON(ctx context.Context, caller Caller, body []byte) (GetResult, error) {
	var input GetInput
	if strictjson.Decode(body, 1024, &input, "article_id", "max_chars") != nil {
		return GetResult{}, fail(ValidationFailed)
	}
	return s.Get(ctx, caller, input)
}

func (s *Service) observe(ctx context.Context, name string, started time.Time, err error, degraded bool, count int, truncated bool) {
	if s.observer == nil {
		return
	}
	var code Code
	if err != nil {
		code = CodeOf(err)
	}
	s.observer.ObserveTool(ctx, name, code, degraded, count, truncated, time.Since(started))
}
func contextFailure(caller, run context.Context) error {
	if errors.Is(caller.Err(), context.Canceled) {
		return fail(ToolCanceled)
	}
	if run.Err() != nil {
		return fail(ToolTimeout)
	}
	return nil
}

func (s *Service) Search(ctx context.Context, caller Caller, input SearchInput) (result SearchResult, returnedErr error) {
	started := time.Now()
	defer func() {
		s.observe(ctx, "search_articles", started, returnedErr, false, len(result.Items), result.Truncated)
	}()
	if _, err := callerID(caller); err != nil {
		return SearchResult{}, err
	}
	limit, err := limitValue(input.Limit)
	if err != nil {
		return SearchResult{}, err
	}
	if !validText(input.Q) || (input.Keyword != nil && !validText(input.Keyword)) || (input.Topic != nil && !validText(input.Topic)) {
		return SearchResult{}, fail(ValidationFailed)
	}
	request := articlesearch.Request{Q: *input.Q, Keyword: input.Keyword, Topic: input.Topic, SourceID: input.SourceID, Limit: limit}
	if _, err := articlesearch.Normalize(request); err != nil {
		return SearchResult{}, fail(ValidationFailed)
	}
	run, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()
	if err := contextFailure(ctx, run); err != nil {
		return SearchResult{}, err
	}
	if s.search == nil {
		return SearchResult{}, fail(SearchUnavailable)
	}
	page, err := s.search.SearchOnce(run, request)
	if failure := contextFailure(ctx, run); failure != nil {
		return SearchResult{}, failure
	}
	if err != nil {
		switch articlesearch.CodeOf(err) {
		case articlesearch.CodeDependencyUnavailable:
			return SearchResult{}, fail(DependencyUnavailable)
		case articlesearch.CodeSearchUnavailable, articlesearch.CodeInvalidCursor:
			return SearchResult{}, fail(SearchUnavailable)
		default:
			return SearchResult{}, fail(InternalError)
		}
	}
	result = SearchResult{Items: make([]ArticleRef, 0, len(page.Items)), Truncated: page.HasMore}
	for _, item := range page.Items {
		ref, err := reference(item)
		if err != nil {
			return SearchResult{}, err
		}
		result.Items = append(result.Items, ref)
	}
	for !s.fits(result) && len(result.Items) > 0 {
		result.Items = result.Items[:len(result.Items)-1]
		result.Truncated = true
	}
	if !s.fits(result) {
		return SearchResult{}, fail(InternalError)
	}
	return result, nil
}

func (s *Service) Recommend(ctx context.Context, caller Caller, input RecommendInput) (result RecommendResult, returnedErr error) {
	started := time.Now()
	defer func() {
		s.observe(ctx, "recommend_articles", started, returnedErr, result.Degraded, len(result.Items), result.Truncated)
	}()
	user, err := callerID(caller)
	if err != nil {
		return RecommendResult{}, err
	}
	limit, err := limitValue(input.Limit)
	if err != nil {
		return RecommendResult{}, err
	}
	run, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()
	if err := contextFailure(ctx, run); err != nil {
		return RecommendResult{}, err
	}
	if s.recommend == nil {
		return RecommendResult{}, fail(DependencyUnavailable)
	}
	page, err := s.recommend.GetOnce(run, user, limit)
	if failure := contextFailure(ctx, run); failure != nil {
		return RecommendResult{}, failure
	}
	if err != nil {
		if errors.Is(err, recommendation.ErrDependencyUnavailable) {
			return RecommendResult{}, fail(DependencyUnavailable)
		}
		return RecommendResult{}, fail(InternalError)
	}
	result = RecommendResult{Items: make([]RecommendedArticle, 0, len(page.Items)), Truncated: page.HasMore, Mode: page.Mode, Degraded: page.Degraded}
	if page.DegradeReason != "" {
		result.DegradeReason = &page.DegradeReason
	}
	for _, item := range page.Items {
		ref, err := reference(item.Article)
		if err != nil {
			return RecommendResult{}, err
		}
		result.Items = append(result.Items, RecommendedArticle{ArticleRef: ref, Reason: item.Reason})
	}
	for !s.fits(result) && len(result.Items) > 0 {
		result.Items = result.Items[:len(result.Items)-1]
		result.Truncated = true
	}
	if !s.fits(result) {
		return RecommendResult{}, fail(InternalError)
	}
	return result, nil
}

func (s *Service) Get(ctx context.Context, caller Caller, input GetInput) (result GetResult, returnedErr error) {
	started := time.Now()
	defer func() { s.observe(ctx, "get_article", started, returnedErr, false, 1, result.Truncated) }()
	if _, err := callerID(caller); err != nil {
		return GetResult{}, err
	}
	if input.ArticleID == nil || *input.ArticleID < 1 {
		return GetResult{}, fail(ValidationFailed)
	}
	maxChars := 4000
	if input.MaxChars != nil {
		maxChars = *input.MaxChars
	}
	if maxChars < 1 || maxChars > 8000 {
		return GetResult{}, fail(ValidationFailed)
	}
	run, cancel := context.WithTimeout(ctx, s.config.Timeout)
	defer cancel()
	if err := contextFailure(ctx, run); err != nil {
		return GetResult{}, err
	}
	if s.text == nil {
		return GetResult{}, fail(DependencyUnavailable)
	}
	value, err := s.text.ReadPublicText(run, *input.ArticleID, maxChars)
	if failure := contextFailure(ctx, run); failure != nil {
		return GetResult{}, failure
	}
	if errors.Is(err, articleDomain.ErrNotFound) {
		return GetResult{}, fail(ArticleNotFound)
	}
	if err != nil {
		return GetResult{}, fail(DependencyUnavailable)
	}
	ref, err := reference(value.Item)
	if err != nil {
		return GetResult{}, err
	}
	result = GetResult{ArticleRef: ref, Content: value.Content, Truncated: value.Truncated}
	chars := []rune(result.Content)
	// 按最终JSON字节数二分正文前缀；身份及链接不可截断。
	if !s.fits(result) {
		result.Truncated = true
		low, high := 0, len(chars)
		for low < high {
			mid := (low + high + 1) / 2
			result.Content = string(chars[:mid])
			if s.fits(result) {
				low = mid
			} else {
				high = mid - 1
			}
		}
		result.Content = string(chars[:low])
	}
	if !s.fits(result) {
		return GetResult{}, fail(InternalError)
	}
	return result, nil
}

func (s *Service) fits(value any) bool {
	encoded, err := json.Marshal(value)
	return err == nil && len(encoded) <= s.config.MaxOutputBytes
}

func prefix(value string, limit int) (string, bool) {
	if utf8.RuneCountInString(value) <= limit {
		return value, false
	}
	return string([]rune(value)[:limit]), true
}

func reference(item articleDomain.ListItem) (ArticleRef, error) {
	if item.ID < 1 || item.RevisionID < 1 || !utf8.ValidString(item.Title) {
		return ArticleRef{}, fail(InternalError)
	}
	ref := ArticleRef{ArticleID: item.ID, RevisionID: item.RevisionID, OriginType: item.Origin, Path: "/articles/" + strconv.FormatInt(item.ID, 10), SummarySource: "excerpt"}
	ref.Title, ref.MetadataTruncated = prefix(item.Title, 256)
	ref.Summary = item.Excerpt
	if item.Enhancement != nil {
		ref.Summary = item.Enhancement.Summary
		ref.SummarySource = item.Enhancement.Method
	}
	if ref.SummarySource != "model" && ref.SummarySource != "extractive" && ref.SummarySource != "excerpt" {
		return ArticleRef{}, fail(InternalError)
	}
	var cut bool
	ref.Summary, cut = prefix(ref.Summary, 1000)
	ref.MetadataTruncated = ref.MetadataTruncated || cut
	if item.Origin == articleDomain.OriginRSS {
		title, cut := prefix(item.Source.Title, 256)
		ref.MetadataTruncated = ref.MetadataTruncated || cut
		ref.Source = &Source{ID: item.Source.ID, Title: title}
		if item.CanonicalURL != "" {
			url := item.CanonicalURL
			ref.OriginalURL = &url
		}
		if item.AuthorName != nil {
			name, cut := prefix(*item.AuthorName, 256)
			ref.Author = &Author{Name: name}
			ref.MetadataTruncated = ref.MetadataTruncated || cut
		}
	} else if item.Origin == articleDomain.OriginUser && item.Author != nil {
		name, cut := prefix(item.Author.Nickname, 256)
		ref.Author = &Author{ID: item.Author.ID, Nickname: name}
		ref.MetadataTruncated = ref.MetadataTruncated || cut
	} else {
		return ArticleRef{}, fail(InternalError)
	}
	return ref, nil
}
