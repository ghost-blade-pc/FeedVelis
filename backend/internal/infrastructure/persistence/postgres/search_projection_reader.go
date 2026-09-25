package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	projectionApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/searchprojection"
	projectionDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/searchprojection"
)

var _ projectionApp.ProjectionReader = (*SearchProjectionRepository)(nil)

// projectionSQL 构造一篇文档所需的全部当前事实。
// 三处连接条件共同保证投影只使用「当前修订」的当前 AI 选择：
//   - generation 结果必须属于当前修订；
//   - Embedding 结果必须属于当前修订，且依赖当前 generation 选择，否则视为不一致并忽略该向量；
//   - 当前选择行必须指向当前修订，否则整体视为没有 AI 结果。
const projectionSQL = `SELECT a.id,a.lock_version,a.current_revision_id,a.status::text,a.origin_type::text,
a.source_id,s.title,a.author_user_id::text,a.author_name,a.published_at,a.source_published_at,
v.title,v.plain_text,v.excerpt,
cs.generation_result_id::text,cs.embedding_result_id::text,
g.summary,g.keywords,g.topics,
e.vector,(cs.embedding_result_id IS NOT NULL AND e.id IS NULL)
FROM velis.articles a
JOIN velis.article_versions v ON v.id=a.current_revision_id
LEFT JOIN velis.sources s ON s.id=a.source_id
LEFT JOIN velis.ai_current_selections cs ON cs.article_id=a.id AND cs.revision_id=a.current_revision_id
LEFT JOIN velis.ai_generation_results g ON g.id=cs.generation_result_id AND g.article_id=a.id AND g.revision_id=a.current_revision_id
LEFT JOIN velis.ai_embedding_results e ON e.id=cs.embedding_result_id AND e.article_id=a.id
    AND e.revision_id=a.current_revision_id AND e.generation_result_id=cs.generation_result_id`

// Current 批量读取指定文章的当前投影；不存在的文章不会出现在结果里。
func (r *SearchProjectionRepository) Current(ctx context.Context, articleIDs []int64) ([]projectionApp.CurrentProjection, error) {
	if len(articleIDs) == 0 {
		return nil, nil
	}
	return r.queryProjections(ctx, projectionSQL+` WHERE a.id = ANY($1::bigint[]) ORDER BY a.id`, articleIDs)
}

// SnapshotPage 按 article ID 升序分页读取当前公开投影，供重建快照阶段使用。
// 它只返回公开文章：快照不把存量不可见内容写进候选索引。
func (r *SearchProjectionRepository) SnapshotPage(ctx context.Context, request projectionApp.SnapshotRequest) ([]projectionApp.CurrentProjection, bool, error) {
	projections, err := r.queryProjections(ctx, projectionSQL+
		` WHERE a.status='published' AND a.id>$1 ORDER BY a.id LIMIT $2`, request.AfterArticleID, request.Limit+1)
	if err != nil {
		return nil, false, err
	}
	hasMore := len(projections) > request.Limit
	if hasMore {
		projections = projections[:request.Limit]
	}
	return projections, hasMore, nil
}

// SinceChange 按全局 change sequence 扫描槽位，供重建增量追赶使用。
// 槽位只保留最新目标，因此这里只返回目标身份，文档由调用方按当前事实重新构造。
func (r *SearchProjectionRepository) SinceChange(ctx context.Context, request projectionApp.ChangeRequest) (projectionApp.JobPage, error) {
	rows, err := r.pool.Query(ctx, `SELECT j.article_id,j.generation,j.change_seq,j.action::text,
j.article_lock_version,j.revision_id,j.generation_result_id::text,j.embedding_result_id::text
FROM velis.search_projection_jobs j WHERE j.change_seq>$1 ORDER BY j.change_seq,j.article_id LIMIT $2`,
		request.AfterChangeSeq, request.Limit+1)
	if err != nil {
		return projectionApp.JobPage{}, err
	}
	defer rows.Close()
	page := projectionApp.JobPage{Jobs: make([]projectionApp.ClaimedJob, 0, request.Limit)}
	for rows.Next() {
		var job projectionApp.ClaimedJob
		var action string
		var changeSeq, lockVersion, revisionID int64
		var generationResultID, embeddingResultID *string
		if err := rows.Scan(&job.ArticleID, &job.Generation, &changeSeq, &action, &lockVersion, &revisionID,
			&generationResultID, &embeddingResultID); err != nil {
			return projectionApp.JobPage{}, err
		}
		target, err := projectionDomain.NewTarget(projectionDomain.Action(action), lockVersion, revisionID, generationResultID, embeddingResultID)
		if err != nil {
			return projectionApp.JobPage{}, err
		}
		job.Target = target
		job.ChangeSeq = changeSeq
		page.Jobs = append(page.Jobs, job)
		if changeSeq > page.HighWaterSeq {
			page.HighWaterSeq = changeSeq
		}
	}
	if err := rows.Err(); err != nil {
		return projectionApp.JobPage{}, err
	}
	if len(page.Jobs) > request.Limit {
		// 多取一条只用于判断是否还有更多；水位仍取本页最后一条。
		page.Jobs = page.Jobs[:request.Limit]
		page.HighWaterSeq = page.Jobs[len(page.Jobs)-1].ChangeSeq
		page.HasMore = true
	}
	return page, nil
}

// PublicCount 返回 PostgreSQL 中当前公开文章数，供校验与候选索引计数比对。
func (r *SearchProjectionRepository) PublicCount(ctx context.Context) (int64, error) {
	var count int64
	err := r.pool.QueryRow(ctx, `SELECT count(*) FROM velis.articles WHERE status='published'`).Scan(&count)
	return count, err
}

// Sample 以与行顺序无关的确定性顺序抽样公开文章，使校验可以重复执行并得到同一批文章。
func (r *SearchProjectionRepository) Sample(ctx context.Context, limit int) ([]projectionApp.CurrentProjection, error) {
	if limit < 1 {
		return nil, nil
	}
	return r.queryProjections(ctx, projectionSQL+
		` WHERE a.status='published' ORDER BY md5(a.id::text),a.id LIMIT $1`, limit)
}

func (r *SearchProjectionRepository) queryProjections(ctx context.Context, query string, args ...any) ([]projectionApp.CurrentProjection, error) {
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return scanProjections(rows)
}

func scanProjections(rows pgx.Rows) ([]projectionApp.CurrentProjection, error) {
	defer rows.Close()
	projections := make([]projectionApp.CurrentProjection, 0)
	for rows.Next() {
		projection, err := scanProjection(rows)
		if err != nil {
			return nil, err
		}
		projections = append(projections, projection)
	}
	return projections, rows.Err()
}

func scanProjection(row rowScanner) (projectionApp.CurrentProjection, error) {
	var projection projectionApp.CurrentProjection
	var document projectionApp.Document
	var status, originType string
	var sourceID *int64
	var sourceTitle, authorUserID, authorName *string
	var publishedAt, sourcePublishedAt *time.Time
	var revisionTitle, plainText, excerpt string
	var generationResultID, embeddingResultID *string
	var summary *string
	var keywords, topics []string
	var vector []float32
	var vectorInconsistent bool
	err := row.Scan(&document.ArticleID, &document.LockVersion, &document.RevisionID, &status, &originType,
		&sourceID, &sourceTitle, &authorUserID, &authorName, &publishedAt, &sourcePublishedAt,
		&revisionTitle, &plainText, &excerpt, &generationResultID, &embeddingResultID,
		&summary, &keywords, &topics, &vector, &vectorInconsistent)
	if err != nil {
		return projectionApp.CurrentProjection{}, err
	}
	projection.ArticleID = document.ArticleID
	projection.InconsistentSelection = vectorInconsistent
	document.OriginType = originType
	document.SourceID = sourceID
	document.AuthorUserID = valueOrEmpty(authorUserID)
	document.Visible = status == "published"
	document.InvisibleReason = status
	// 生效发布时间与列表排序一致：优先来源发布时间，其次是本平台发布时间。
	if sourcePublishedAt != nil {
		document.PublishedAt = utcTimePointer(sourcePublishedAt)
	} else {
		document.PublishedAt = utcTimePointer(publishedAt)
	}
	// 目标身份使用原始当前选择，与写路径推进槽位时完全一致；
	// 文档内容才使用经过修订与 generation 校验后的结果。
	// 不可见文章的 tombstone 目标不携带任何 AI 结果引用。
	action := projectionDomain.ActionTombstone
	var targetGeneration, targetEmbedding *string
	if document.Visible {
		action = projectionDomain.ActionUpsert
		targetGeneration, targetEmbedding = generationResultID, embeddingResultID
		document.Title = revisionTitle
		document.PlainText = plainText
		document.Excerpt = excerpt
		document.SourceTitle = valueOrEmpty(sourceTitle)
		document.AuthorName = valueOrEmpty(authorName)
		document.GenerationResultID = valueOrEmpty(generationResultID)
		document.EmbeddingResultID = valueOrEmpty(embeddingResultID)
		if summary != nil {
			document.Summary = *summary
			document.Keywords = keywords
			document.Topics = topics
		}
		if len(vector) > 0 && !vectorInconsistent {
			document.Vector = make([]float64, len(vector))
			for index, value := range vector {
				document.Vector[index] = float64(value)
			}
		}
	}
	target, err := projectionDomain.NewTarget(action, document.LockVersion, document.RevisionID,
		targetGeneration, targetEmbedding)
	if err != nil {
		return projectionApp.CurrentProjection{}, err
	}
	projection.Target = target
	projection.Document = document
	return projection, nil
}
func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
