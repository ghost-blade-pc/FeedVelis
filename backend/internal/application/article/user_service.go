package article

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"time"

	idempotencyApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/idempotency"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/ports"
	articleDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/article"
)

var ErrAssetOwnership = ports.ErrArticleAssetUnavailable

type UserArticleRepository interface {
	CreateUserArticle(context.Context, string, articleDomain.RevisionData, bool, time.Time) (articleDomain.StoredArticle, error)
	GetUserArticle(context.Context, int64, string) (articleDomain.StoredArticle, error)
	ListUserArticles(context.Context, string, int64, int) ([]articleDomain.StoredArticle, error)
	UpdateUserRevision(context.Context, int64, string, int64, articleDomain.RevisionData, time.Time) (articleDomain.StoredArticle, bool, error)
	SetArticleState(context.Context, int64, int64, articleDomain.Status, *articleDomain.OfflineReason, *string, *time.Time, *time.Time, time.Time) (articleDomain.StoredArticle, error)
}

// AssetPolicy 是单篇文章的图片引用上限；由配置注入，在绑定边界强制执行。
type AssetPolicy struct {
	MaxImages     int
	MaxTotalBytes int64
}

type UserService struct {
	repository  UserArticleRepository
	renderer    ports.UserContentRenderer
	assets      ports.ArticleAssets
	idempotency *idempotencyApp.Service
	clock       ports.Clock
	assetPolicy AssetPolicy
}

func NewUserService(repository UserArticleRepository, renderer ports.UserContentRenderer, assets ports.ArticleAssets,
	idempotency *idempotencyApp.Service, clock ports.Clock, policy AssetPolicy) *UserService {
	return &UserService{repository: repository, renderer: renderer, assets: assets,
		idempotency: idempotency, clock: clock, assetPolicy: policy}
}

// bindAssets 把本次修订引用的资产并入文章事务；数量与总量上限由绑定边界复核。
func (s *UserService) bindAssets(ctx context.Context, ownerUserID string, articleID, revisionID int64,
	assetIDs []string, now time.Time) error {
	if len(assetIDs) == 0 {
		return nil
	}
	if s.assets == nil {
		return ErrAssetOwnership
	}
	return s.assets.BindArticleAssets(ctx, ports.ArticleAssetBinding{
		OwnerUserID: ownerUserID, ArticleID: articleID, RevisionID: revisionID, AssetIDs: assetIDs,
		MaxImages: s.assetPolicy.MaxImages, MaxTotalBytes: s.assetPolicy.MaxTotalBytes, Now: now,
	})
}

type CreateUserArticleCommand struct {
	AuthorUserID   string
	IdempotencyKey string
	Title          string
	Markdown       string
	InitialStatus  articleDomain.Status
}

type UserArticleResult struct {
	Article  articleDomain.StoredArticle `json:"article"`
	AssetIDs []string                    `json:"asset_ids"`
}

func (s *UserService) Create(ctx context.Context, command CreateUserArticleCommand) (UserArticleResult, bool, error) {
	mode := ports.UserContentDraft
	publish := false
	switch command.InitialStatus {
	case articleDomain.StatusDraft:
	case articleDomain.StatusPublished:
		mode, publish = ports.UserContentPublish, true
	default:
		return UserArticleResult{}, false, articleDomain.ErrInvalidState
	}
	rendered, err := s.renderer.RenderUserContent(command.Title, command.Markdown, mode)
	if err != nil {
		return UserArticleResult{}, false, err
	}
	payload := struct {
		Title    string   `json:"title"`
		Markdown string   `json:"markdown"`
		Status   string   `json:"initial_status"`
		Assets   []string `json:"asset_ids"`
	}{rendered.Title, rendered.Markdown, string(command.InitialStatus), rendered.AssetIDs}
	now := s.clock.Now().UTC()
	outcome, err := s.idempotency.Execute(ctx, idempotencyApp.Command{
		Identity: idempotencyApp.Identity{ActorUserID: command.AuthorUserID, Operation: "article.create", Key: command.IdempotencyKey},
		Payload:  payload, Now: now,
	}, func(txContext context.Context) (any, string, string, error) {
		markdown := rendered.Markdown
		html := rendered.HTML
		stored, createErr := s.repository.CreateUserArticle(txContext, command.AuthorUserID, articleDomain.RevisionData{
			Title: rendered.Title, Markdown: &markdown, SanitizedHTML: &html, PlainText: rendered.PlainText,
			Excerpt: rendered.Excerpt, Language: "zh-CN", ContentHash: rendered.Hash, SanitizerVersion: rendered.Sanitizer,
		}, publish, now)
		if createErr != nil {
			return nil, "", "", createErr
		}
		if bindErr := s.bindAssets(txContext, command.AuthorUserID, stored.ID, stored.RevisionID, rendered.AssetIDs, now); bindErr != nil {
			return nil, "", "", bindErr
		}
		result, resultErr := s.result(txContext, stored)
		if resultErr != nil {
			return nil, "", "", resultErr
		}
		return result, "article", jsonNumber(stored.ID), nil
	})
	if err != nil {
		return UserArticleResult{}, false, err
	}
	return decodeUserArticleOutcome(outcome)
}

// UserArticleDetail 是作者私有详情：当前修订内容，以及该修订实际引用的资产标识。
type UserArticleDetail struct {
	Article  articleDomain.StoredArticle
	AssetIDs []string
}

func (s *UserService) Get(ctx context.Context, authorID string, articleID int64) (UserArticleDetail, error) {
	if articleID <= 0 {
		return UserArticleDetail{}, articleDomain.ErrInvalidArgument
	}
	stored, err := s.repository.GetUserArticle(ctx, articleID, authorID)
	if err != nil {
		return UserArticleDetail{}, err
	}
	assetIDs, err := s.listRevisionAssets(ctx, stored.RevisionID)
	if err != nil {
		return UserArticleDetail{}, err
	}
	return UserArticleDetail{Article: stored, AssetIDs: assetIDs}, nil
}

// result 以数据库中的修订引用为准构造写命令结果，使创建、编辑和状态变更返回同一视图。
func (s *UserService) result(ctx context.Context, stored articleDomain.StoredArticle) (UserArticleResult, error) {
	assetIDs, err := s.listRevisionAssets(ctx, stored.RevisionID)
	if err != nil {
		return UserArticleResult{}, err
	}
	return UserArticleResult{Article: stored, AssetIDs: assetIDs}, nil
}

// listRevisionAssets 在资产适配器未装配时返回空引用，避免详情读取依赖资产能力。
func (s *UserService) listRevisionAssets(ctx context.Context, revisionID int64) ([]string, error) {
	if s.assets == nil {
		return []string{}, nil
	}
	return s.assets.ListRevisionAssetIDs(ctx, revisionID)
}

// UserArticlePage 是本人文章的 ID 倒序游标分页结果。
type UserArticlePage struct {
	Items      []articleDomain.StoredArticle
	NextCursor *string
	HasMore    bool
}

func (s *UserService) List(ctx context.Context, authorID, encodedCursor string, limit int) (UserArticlePage, error) {
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 50 {
		return UserArticlePage{}, articleDomain.ErrInvalidArgument
	}
	var beforeID int64
	if encodedCursor != "" {
		decoded, err := decodeUserArticleCursor(encodedCursor)
		if err != nil {
			return UserArticlePage{}, err
		}
		beforeID = decoded
	}
	items, err := s.repository.ListUserArticles(ctx, authorID, beforeID, limit+1)
	if err != nil {
		return UserArticlePage{}, err
	}
	page := UserArticlePage{Items: items}
	if len(items) > limit {
		page.Items = items[:limit]
		page.HasMore = true
		encoded := encodeUserArticleCursor(page.Items[len(page.Items)-1].ID)
		page.NextCursor = &encoded
	}
	return page, nil
}

// userArticleCursorPayload 只携带稳定排序键：本人文章按 ID 倒序，游标因此只需记住上界。
type userArticleCursorPayload struct {
	Version  int   `json:"v"`
	BeforeID int64 `json:"before_id"`
}

func encodeUserArticleCursor(beforeID int64) string {
	data, _ := json.Marshal(userArticleCursorPayload{Version: 1, BeforeID: beforeID})
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeUserArticleCursor(value string) (int64, error) {
	if len(value) > 1024 {
		return 0, articleDomain.ErrInvalidCursor
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return 0, articleDomain.ErrInvalidCursor
	}
	var payload userArticleCursorPayload
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || payload.Version != 1 || payload.BeforeID <= 0 {
		return 0, articleDomain.ErrInvalidCursor
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return 0, articleDomain.ErrInvalidCursor
	}
	return payload.BeforeID, nil
}

// PreviewArticleCommand 是无状态预览输入；预览不创建修订，也不绑定资产。
type PreviewArticleCommand struct {
	Title    string
	Markdown string
}

// ArticlePreview 是渲染后的预览结果，字段与本人文章详情的可见内容部分一致。
type ArticlePreview struct {
	ContentHTML string   `json:"content_html"`
	PlainText   string   `json:"plain_text"`
	Excerpt     string   `json:"excerpt"`
	AssetIDs    []string `json:"asset_ids"`
}

// Preview 复用投稿渲染管线，按草稿宽松校验渲染，不写入任何状态。
func (s *UserService) Preview(_ context.Context, command PreviewArticleCommand) (ArticlePreview, error) {
	rendered, err := s.renderer.RenderUserContent(command.Title, command.Markdown, ports.UserContentDraft)
	if err != nil {
		return ArticlePreview{}, err
	}
	return ArticlePreview{
		ContentHTML: rendered.HTML, PlainText: rendered.PlainText,
		Excerpt: rendered.Excerpt, AssetIDs: rendered.AssetIDs,
	}, nil
}

type UpdateUserArticleCommand struct {
	AuthorUserID    string
	ArticleID       int64
	ExpectedVersion int64
	IdempotencyKey  string
	Title           string
	Markdown        string
}

func (s *UserService) Update(ctx context.Context, command UpdateUserArticleCommand) (UserArticleResult, bool, error) {
	rendered, err := s.renderer.RenderUserContent(command.Title, command.Markdown, ports.UserContentDraft)
	if err != nil {
		return UserArticleResult{}, false, err
	}
	payload := struct {
		ArticleID int64  `json:"article_id"`
		Expected  int64  `json:"expected_version"`
		Title     string `json:"title"`
		Markdown  string `json:"markdown"`
	}{command.ArticleID, command.ExpectedVersion, rendered.Title, rendered.Markdown}
	now := s.clock.Now().UTC()
	outcome, err := s.idempotency.Execute(ctx, idempotencyApp.Command{
		Identity: idempotencyApp.Identity{ActorUserID: command.AuthorUserID, Operation: "article.update", Key: command.IdempotencyKey},
		Payload:  payload, Now: now,
	}, func(txContext context.Context) (any, string, string, error) {
		current, getErr := s.repository.GetUserArticle(txContext, command.ArticleID, command.AuthorUserID)
		if getErr != nil {
			return nil, "", "", getErr
		}
		if current.LockVersion != command.ExpectedVersion {
			return nil, "", "", articleDomain.ErrVersionConflict
		}
		if current.Status != articleDomain.StatusDraft {
			rendered, err = s.renderer.RenderUserContent(command.Title, command.Markdown, ports.UserContentPublish)
			if err != nil {
				return nil, "", "", err
			}
		}
		markdown, html := rendered.Markdown, rendered.HTML
		stored, changed, updateErr := s.repository.UpdateUserRevision(txContext, command.ArticleID, command.AuthorUserID,
			command.ExpectedVersion, articleDomain.RevisionData{Title: rendered.Title, Markdown: &markdown,
				SanitizedHTML: &html, PlainText: rendered.PlainText, Excerpt: rendered.Excerpt, Language: "zh-CN",
				ContentHash: rendered.Hash, SanitizerVersion: rendered.Sanitizer}, now)
		if updateErr != nil {
			return nil, "", "", updateErr
		}
		if changed {
			if bindErr := s.bindAssets(txContext, command.AuthorUserID, stored.ID, stored.RevisionID, rendered.AssetIDs, now); bindErr != nil {
				return nil, "", "", bindErr
			}
		}
		result, resultErr := s.result(txContext, stored)
		if resultErr != nil {
			return nil, "", "", resultErr
		}
		return result, "article", jsonNumber(stored.ID), nil
	})
	if err != nil {
		return UserArticleResult{}, false, err
	}
	return decodeUserArticleOutcome(outcome)
}

type UserArticleStateCommand struct {
	AuthorUserID    string
	ArticleID       int64
	ExpectedVersion int64
	IdempotencyKey  string
}

func (s *UserService) Publish(ctx context.Context, command UserArticleStateCommand) (UserArticleResult, bool, error) {
	return s.changeState(ctx, "article.publish", command, func(txContext context.Context, current articleDomain.StoredArticle, now time.Time) (articleDomain.StoredArticle, error) {
		if current.Status == articleDomain.StatusOffline && current.OfflineReason != nil && *current.OfflineReason == articleDomain.OfflineByAdmin {
			return articleDomain.StoredArticle{}, articleDomain.ErrAdminOffline
		}
		if current.Status != articleDomain.StatusDraft && !(current.Status == articleDomain.StatusOffline && current.OfflineReason != nil && *current.OfflineReason == articleDomain.OfflineByAuthor) {
			return articleDomain.StoredArticle{}, articleDomain.ErrInvalidState
		}
		title, markdown := current.Revision.Title, ""
		if current.Revision.Markdown != nil {
			markdown = *current.Revision.Markdown
		}
		if _, err := s.renderer.RenderUserContent(title, markdown, ports.UserContentPublish); err != nil {
			return articleDomain.StoredArticle{}, err
		}
		return s.repository.SetArticleState(txContext, current.ID, command.ExpectedVersion, articleDomain.StatusPublished, nil, nil, &now, nil, now)
	})
}

func (s *UserService) Offline(ctx context.Context, command UserArticleStateCommand) (UserArticleResult, bool, error) {
	return s.changeState(ctx, "article.offline", command, func(txContext context.Context, current articleDomain.StoredArticle, now time.Time) (articleDomain.StoredArticle, error) {
		if current.Status != articleDomain.StatusPublished {
			return articleDomain.StoredArticle{}, articleDomain.ErrInvalidState
		}
		reason := articleDomain.OfflineByAuthor
		return s.repository.SetArticleState(txContext, current.ID, command.ExpectedVersion, articleDomain.StatusOffline, &reason, nil, nil, nil, now)
	})
}

func (s *UserService) Delete(ctx context.Context, command UserArticleStateCommand) (UserArticleResult, bool, error) {
	return s.changeState(ctx, "article.delete", command, func(txContext context.Context, current articleDomain.StoredArticle, now time.Time) (articleDomain.StoredArticle, error) {
		deletedAt := now
		return s.repository.SetArticleState(txContext, current.ID, command.ExpectedVersion, articleDomain.StatusDeleted, nil, nil, nil, &deletedAt, now)
	})
}

type stateChanger func(context.Context, articleDomain.StoredArticle, time.Time) (articleDomain.StoredArticle, error)

func (s *UserService) changeState(ctx context.Context, operation string, command UserArticleStateCommand, change stateChanger) (UserArticleResult, bool, error) {
	payload := struct {
		ArticleID int64 `json:"article_id"`
		Expected  int64 `json:"expected_version"`
	}{command.ArticleID, command.ExpectedVersion}
	now := s.clock.Now().UTC()
	outcome, err := s.idempotency.Execute(ctx, idempotencyApp.Command{
		Identity: idempotencyApp.Identity{ActorUserID: command.AuthorUserID, Operation: operation, Key: command.IdempotencyKey}, Payload: payload, Now: now,
	}, func(txContext context.Context) (any, string, string, error) {
		current, getErr := s.repository.GetUserArticle(txContext, command.ArticleID, command.AuthorUserID)
		if getErr != nil {
			return nil, "", "", getErr
		}
		if current.LockVersion != command.ExpectedVersion {
			return nil, "", "", articleDomain.ErrVersionConflict
		}
		stored, changeErr := change(txContext, current, now)
		if changeErr != nil {
			return nil, "", "", changeErr
		}
		result, resultErr := s.result(txContext, stored)
		if resultErr != nil {
			return nil, "", "", resultErr
		}
		return result, "article", jsonNumber(stored.ID), nil
	})
	if err != nil {
		return UserArticleResult{}, false, err
	}
	return decodeUserArticleOutcome(outcome)
}

func decodeUserArticleOutcome(outcome idempotencyApp.Outcome) (UserArticleResult, bool, error) {
	var result UserArticleResult
	if err := json.Unmarshal(outcome.Result, &result); err != nil {
		return UserArticleResult{}, false, err
	}
	return result, outcome.Replayed, nil
}

func jsonNumber(value int64) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
