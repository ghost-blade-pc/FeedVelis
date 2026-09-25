package opensearch

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"

	projectionApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/searchprojection"
)

//go:embed templates/articles.v1.json
var templates embed.FS

// templateAsset 返回 schema 版本对应的版本化模板资产。
// 新增 schema 版本必须新增资产文件并递增 search.schema_version，不得原地修改既有模板语义。
func templateAsset(schemaVersion int) (string, error) {
	if schemaVersion != 1 {
		return "", fmt.Errorf("%w: 未注册的 schema 版本 %d", projectionApp.ErrSchemaMismatch, schemaVersion)
	}
	return "templates/articles.v1.json", nil
}

// RenderTemplate 渲染模板：只替换维度与 schema 身份，字段语义由资产文件本身固定。
func RenderTemplate(indexPrefix string, schemaVersion, dimensions int, schemaIdentity string) ([]byte, error) {
	asset, err := templateAsset(schemaVersion)
	if err != nil {
		return nil, err
	}
	if dimensions < 1 || dimensions > 65536 {
		return nil, fmt.Errorf("%w: 向量维度 %d", projectionApp.ErrVectorDimensionMismatch, dimensions)
	}
	raw, err := templates.ReadFile(asset)
	if err != nil {
		return nil, err
	}
	rendered := string(raw)
	rendered = strings.ReplaceAll(rendered, "{{index_prefix}}", indexPrefix)
	rendered = strings.ReplaceAll(rendered, "{{schema_identity}}", schemaIdentity)
	rendered = strings.ReplaceAll(rendered, "{{embedding_dimensions}}", strconv.Itoa(dimensions))
	var decoded map[string]any
	if err := json.Unmarshal([]byte(rendered), &decoded); err != nil {
		return nil, fmt.Errorf("渲染索引模板失败: %w", err)
	}
	return []byte(rendered), nil
}

// ensureTemplate 写入 composable 模板；模板本身不改变已有索引，只影响之后创建的索引。
func (c *Client) ensureTemplate(ctx context.Context, spec projectionApp.IndexSpec) error {
	body, err := RenderTemplate(c.cfg.IndexPrefix, spec.SchemaVersion, spec.EmbeddingDimensions, spec.SchemaIdentity)
	if err != nil {
		return err
	}
	name := c.templateName(spec.SchemaVersion)
	if _, err := c.api.IndexTemplate.Create(ctx, opensearchapi.IndexTemplateCreateReq{
		IndexTemplate: name, Body: strings.NewReader(string(body)),
	}); err != nil {
		return redactError(err)
	}
	return nil
}

func (c *Client) templateName(schemaVersion int) string {
	return fmt.Sprintf("%s-v%d", c.cfg.IndexPrefix, schemaVersion)
}

// Ensure 幂等初始化索引与别名：已满足目标 schema 时返回现状，不创建重复索引或第二个写索引。
func (c *Client) Ensure(ctx context.Context, spec projectionApp.IndexSpec, aliases projectionApp.AliasSwitch) (projectionApp.IndexState, error) {
	if err := c.ensureTemplate(ctx, spec); err != nil {
		return projectionApp.IndexState{}, err
	}
	state, err := c.Inspect(ctx, spec.PhysicalIndex)
	switch {
	case errors.Is(err, projectionApp.ErrIndexMissing):
		if state, err = c.Create(ctx, spec); err != nil {
			return projectionApp.IndexState{}, err
		}
	case err != nil:
		return projectionApp.IndexState{}, err
	}
	if state.SchemaIdentity != spec.SchemaIdentity || state.SchemaVersion != spec.SchemaVersion {
		return projectionApp.IndexState{}, fmt.Errorf("%w: 实际 %s，要求 %s",
			projectionApp.ErrSchemaMismatch, state.SchemaIdentity, spec.SchemaIdentity)
	}
	if err := c.SwitchAliases(ctx, aliases); err != nil {
		return projectionApp.IndexState{}, err
	}
	return c.Inspect(ctx, spec.PhysicalIndex)
}

// Create 创建物理索引；已存在时视为幂等成功。
// 它必须走模板：实际 settings/mapping 是唯一可信的 schema 身份来源。
func (c *Client) Create(ctx context.Context, spec projectionApp.IndexSpec) (projectionApp.IndexState, error) {
	if err := c.ensureTemplate(ctx, spec); err != nil {
		return projectionApp.IndexState{}, err
	}
	exists, err := c.indexExists(ctx, spec.PhysicalIndex)
	if err != nil {
		return projectionApp.IndexState{}, err
	}
	if !exists {
		if _, err := c.api.Indices.Create(ctx, opensearchapi.IndicesCreateReq{Index: spec.PhysicalIndex}); err != nil {
			return projectionApp.IndexState{}, redactError(err)
		}
	}
	state, err := c.Inspect(ctx, spec.PhysicalIndex)
	if err != nil {
		return projectionApp.IndexState{}, err
	}
	if state.SchemaIdentity != spec.SchemaIdentity || state.SchemaVersion != spec.SchemaVersion {
		return projectionApp.IndexState{}, fmt.Errorf("%w: 实际 %s，要求 %s",
			projectionApp.ErrSchemaMismatch, state.SchemaIdentity, spec.SchemaIdentity)
	}
	return state, nil
}

// Inspect 读取真实 mapping/settings 与文档计数；索引不存在时返回 ErrIndexMissing。
// 计数与文档读取是近实时的，因此这里先刷新：否则刚写入的文档会被读成不存在，
// 使重建校验与运维查询得到与索引实际状态不符的结论。
func (c *Client) Inspect(ctx context.Context, index string) (projectionApp.IndexState, error) {
	exists, err := c.indexExists(ctx, index)
	if err != nil {
		return projectionApp.IndexState{}, err
	}
	if !exists {
		return projectionApp.IndexState{}, fmt.Errorf("%w: %s", projectionApp.ErrIndexMissing, index)
	}
	if err := c.Refresh(ctx, index); err != nil {
		return projectionApp.IndexState{}, err
	}
	response, err := c.api.Indices.Get(ctx, opensearchapi.IndicesGetReq{Indices: []string{index}})
	if err != nil {
		return projectionApp.IndexState{}, redactError(err)
	}
	data := (*response.IndicesGetRespData)[index]
	state := projectionApp.IndexState{PhysicalIndex: index}
	if data.Mappings != nil {
		var mapping struct {
			Meta struct {
				SchemaVersion       int    `json:"velis_schema_version"`
				SchemaIdentity      string `json:"velis_schema_identity"`
				EmbeddingDimensions int    `json:"velis_embedding_dimensions"`
			} `json:"_meta"`
		}
		if err := json.Unmarshal(data.Mappings, &mapping); err != nil {
			return projectionApp.IndexState{}, fmt.Errorf("解析索引 mapping: %w", err)
		}
		state.SchemaVersion = mapping.Meta.SchemaVersion
		state.SchemaIdentity = mapping.Meta.SchemaIdentity
	}
	if data.Settings != nil {
		var settings struct {
			Index struct {
				KNN json.RawMessage `json:"knn"`
			} `json:"index"`
		}
		if err := json.Unmarshal(data.Settings, &settings); err != nil {
			return projectionApp.IndexState{}, fmt.Errorf("解析索引 settings: %w", err)
		}
		if len(settings.Index.KNN) > 0 && !strings.Contains(string(settings.Index.KNN), "true") {
			return projectionApp.IndexState{}, fmt.Errorf("%w: %s 未启用 index.knn", projectionApp.ErrSchemaMismatch, index)
		}
	}
	state.HasReadAlias = false
	state.HasWriteAlias = false
	for name := range data.Aliases {
		switch name {
		case c.cfg.ReadAlias():
			state.HasReadAlias = true
		case c.cfg.WriteAlias():
			state.HasWriteAlias = true
		}
	}
	if state.Documents, err = c.count(ctx, index, ""); err != nil {
		return projectionApp.IndexState{}, err
	}
	if state.VisibleDocuments, err = c.count(ctx, index, `{"term":{"visible":true}}`); err != nil {
		return projectionApp.IndexState{}, err
	}
	return state, nil
}

func (c *Client) count(ctx context.Context, index, query string) (int64, error) {
	request := &opensearchapi.IndicesCountReq{Indices: []string{index}}
	// _count 的正文必须是 {"query": ...}；直接发送查询子句会被解析器拒绝。
	if query != "" {
		request.Body = strings.NewReader(`{"query":` + query + `}`)
	}
	response, err := c.api.Indices.Count(ctx, request)
	if err != nil {
		return 0, redactError(err)
	}
	return int64(response.Count), nil
}

func (c *Client) indexExists(ctx context.Context, index string) (bool, error) {
	response, err := c.api.Indices.Exists(ctx, opensearchapi.IndicesExistsReq{Indices: []string{index}})
	if response != nil {
		return response.StatusCode == http.StatusOK, nil
	}
	if err != nil {
		return false, redactError(err)
	}
	return false, nil
}

// SwitchAliases 用一次 _aliases 操作完成切换：读写别名与解除旧索引在同一次请求内生效。
func (c *Client) SwitchAliases(ctx context.Context, switchTo projectionApp.AliasSwitch) error {
	actions := make([]map[string]any, 0, 3)
	if switchTo.DetachIndex != "" && switchTo.DetachIndex != switchTo.Index {
		for _, alias := range []string{switchTo.ReadAlias, switchTo.WriteAlias} {
			actions = append(actions, map[string]any{"remove": map[string]any{"index": switchTo.DetachIndex, "alias": alias}})
		}
	}
	actions = append(actions,
		map[string]any{"add": map[string]any{"index": switchTo.Index, "alias": switchTo.ReadAlias}},
		map[string]any{"add": map[string]any{"index": switchTo.Index, "alias": switchTo.WriteAlias, "is_write_index": true}},
	)
	body, err := json.Marshal(map[string]any{"actions": actions})
	if err != nil {
		return err
	}
	if _, err := c.api.Aliases(ctx, opensearchapi.AliasesReq{Body: strings.NewReader(string(body))}); err != nil {
		return redactError(err)
	}
	// 写别名不唯一会让后续写入目标不确定，必须在切换后立即核对。
	state, err := c.Inspect(ctx, switchTo.Index)
	if err != nil {
		return err
	}
	if !state.HasReadAlias || !state.HasWriteAlias {
		return fmt.Errorf("%w: %s 缺少读写别名", projectionApp.ErrAliasConflict, switchTo.Index)
	}
	return c.requireSingleWriteIndex(ctx, switchTo.Index)
}

// requireSingleWriteIndex 确认写别名只由一个索引承担，避免写入目标不确定。
func (c *Client) requireSingleWriteIndex(ctx context.Context, index string) error {
	response, err := c.api.Indices.Alias.Get(ctx, opensearchapi.AliasGetReq{Alias: []string{c.cfg.WriteAlias()}})
	if err != nil {
		return redactError(err)
	}
	writers := make([]string, 0, 1)
	for name, item := range response.GetIndices() {
		for _, raw := range item.Aliases {
			var alias struct {
				IsWriteIndex *bool `json:"is_write_index"`
			}
			if err := json.Unmarshal(raw, &alias); err != nil {
				return err
			}
			if alias.IsWriteIndex != nil && *alias.IsWriteIndex {
				writers = append(writers, name)
			}
		}
	}
	if len(writers) != 1 || writers[0] != index {
		return fmt.Errorf("%w: 写索引必须唯一且为 %s，实际 %v", projectionApp.ErrAliasConflict, index, writers)
	}
	return nil
}

// Delete 删除精确匹配的物理索引；调用方负责确认它不被别名或活动 delivery 引用。
func (c *Client) Delete(ctx context.Context, index string) error {
	exists, err := c.indexExists(ctx, index)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if state, err := c.Inspect(ctx, index); err == nil && (state.HasReadAlias || state.HasWriteAlias) {
		return fmt.Errorf("%w: %s 仍被读写别名引用", projectionApp.ErrUnsafeIndexTarget, index)
	}
	if _, err := c.api.Indices.Delete(ctx, opensearchapi.IndicesDeleteReq{Indices: []string{index}}); err != nil {
		return redactError(err)
	}
	return nil
}

// Analyze 返回固定样例在指定索引分析器下的 token，供 schema 契约测试核对。
func (c *Client) Analyze(ctx context.Context, index, analyzer, text string) ([]string, error) {
	response, err := c.api.Indices.Analyze(ctx, opensearchapi.IndicesAnalyzeReq{
		Index: index,
		Body:  opensearchapi.IndicesAnalyzeBody{Analyzer: analyzer, Text: []string{text}},
	})
	if err != nil {
		return nil, redactError(err)
	}
	tokens := make([]string, 0, len(response.Tokens))
	for _, token := range response.Tokens {
		tokens = append(tokens, token.Token)
	}
	return tokens, nil
}

// Refresh 让已写入的文档对计数与读取可见。
func (c *Client) Refresh(ctx context.Context, index string) error {
	if _, err := c.api.Indices.Refresh(ctx, &opensearchapi.IndicesRefreshReq{Index: []string{index}}); err != nil {
		return redactError(err)
	}
	return nil
}

// Fetch 读取索引中一篇文档的投影身份与内容指纹；文档不存在时返回 ErrDocumentMissing。
func (c *Client) Fetch(ctx context.Context, index string, articleID int64) (projectionApp.IndexedDocument, error) {
	stored, err := c.api.Document.Get(ctx, opensearchapi.DocumentGetReq{Index: index, DocumentID: strconv.FormatInt(articleID, 10)})
	if err != nil {
		return projectionApp.IndexedDocument{}, redactError(err)
	}
	if !stored.Found || len(stored.Source) == 0 {
		return projectionApp.IndexedDocument{}, fmt.Errorf("%w: %s/%d", projectionApp.ErrDocumentMissing, index, articleID)
	}
	var source struct {
		ArticleID   int64  `json:"article_id"`
		Generation  int64  `json:"projection_generation"`
		LockVersion int64  `json:"lock_version"`
		RevisionID  int64  `json:"revision_id"`
		Visible     bool   `json:"visible"`
		ContentHash string `json:"projection_content_hash"`
	}
	if err := json.Unmarshal(stored.Source, &source); err != nil {
		return projectionApp.IndexedDocument{}, fmt.Errorf("解析索引文档: %w", err)
	}
	return projectionApp.IndexedDocument{ArticleID: source.ArticleID, Generation: source.Generation,
		LockVersion: source.LockVersion, RevisionID: source.RevisionID, Visible: source.Visible,
		ContentHash: source.ContentHash}, nil
}

// DeleteDocument 删除索引中的一篇文档；文档不存在时视为成功。仅供测试与诊断使用。
func (c *Client) DeleteDocument(ctx context.Context, index string, articleID int64) error {
	if _, err := c.api.Document.Delete(ctx, opensearchapi.DocumentDeleteReq{Index: index,
		DocumentID: strconv.FormatInt(articleID, 10)}); err != nil {
		return redactError(err)
	}
	// 计数读取是近实时的：删除后必须刷新，诊断结果才与索引实际状态一致。
	_, _ = c.api.Indices.Refresh(ctx, &opensearchapi.IndicesRefreshReq{Index: []string{index}})
	return nil
}

// RawDocument 返回索引中一篇文档的原始字段；仅供验收测试做字节级断言。
func (c *Client) RawDocument(ctx context.Context, index string, articleID int64) (map[string]any, error) {
	stored, err := c.api.Document.Get(ctx, opensearchapi.DocumentGetReq{Index: index, DocumentID: strconv.FormatInt(articleID, 10)})
	if err != nil {
		return nil, redactError(err)
	}
	if !stored.Found || len(stored.Source) == 0 {
		return nil, fmt.Errorf("%w: %s/%d", projectionApp.ErrDocumentMissing, index, articleID)
	}
	var raw map[string]any
	if err := json.Unmarshal(stored.Source, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}
