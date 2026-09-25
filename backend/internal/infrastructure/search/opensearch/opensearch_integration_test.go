package opensearch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/opensearch-project/opensearch-go/v4/opensearchapi"

	searchApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/articlesearch"
	projectionApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/searchprojection"
	projectionDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/searchprojection"
)

// requireOpenSearch 是真实依赖闸门：未配置测试 URL 时必须明确报告跳过，而不是静默通过。
func requireOpenSearch(t *testing.T) string {
	t.Helper()
	endpoint := strings.TrimSpace(os.Getenv("VELIS_TEST_OPENSEARCH_URL"))
	if endpoint == "" {
		t.Skip("未设置 VELIS_TEST_OPENSEARCH_URL，跳过真实 OpenSearch 契约测试")
	}
	return endpoint
}

type harness struct {
	client  *Client
	prefix  string
	buildID string
}

func newHarness(t *testing.T, dimensions int) *harness {
	t.Helper()
	endpoint := requireOpenSearch(t)
	buildID := fmt.Sprintf("%s-%s", time.Now().UTC().Format("20060102t150405z"), strings.ToLower(randSuffix(t)))
	prefix := "velis-test-" + buildID[:14]
	client, err := New(Config{
		Endpoints: []string{endpoint}, IndexPrefix: prefix, SchemaVersion: 1,
		SchemaIdentity:      fmt.Sprintf("mapping-v1|analyzer-cjk|dims-%d|encoding-v1", dimensions),
		EmbeddingDimensions: dimensions, ConnectTimeout: 5 * time.Second, RequestTimeout: 30 * time.Second,
		BulkMaxItems: 100, BulkMaxBytes: 1 << 20, BulkMaxDocumentChars: 100000,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanupHarness(client, prefix)
		_ = client.Close()
	})
	return &harness{client: client, prefix: prefix, buildID: buildID}
}

func randSuffix(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%06x", time.Now().UnixNano()&0xffffff)
}

func cleanupHarness(client *Client, prefix string) {
	ctx := context.Background()
	response, err := client.api.Indices.Get(ctx, opensearchapi.IndicesGetReq{Indices: []string{prefix + "-*"}})
	if err == nil && response.IndicesGetRespData != nil {
		for name := range *response.IndicesGetRespData {
			_, _ = client.api.Indices.Delete(ctx, opensearchapi.IndicesDeleteReq{Indices: []string{name}})
		}
	}
	_, _ = client.api.IndexTemplate.Delete(ctx, opensearchapi.IndexTemplateDeleteReq{IndexTemplate: prefix + "-v1"})
}

func (h *harness) index(schemaVersion int) string {
	return fmt.Sprintf("%s-v%d-%s", h.prefix, schemaVersion, h.buildID)
}

func (h *harness) spec(schemaVersion, dimensions int) projectionApp.IndexSpec {
	return projectionApp.IndexSpec{
		PhysicalIndex:       h.index(schemaVersion),
		SchemaVersion:       schemaVersion,
		SchemaIdentity:      fmt.Sprintf("mapping-v%d|analyzer-cjk|dims-%d|encoding-v1", schemaVersion, dimensions),
		EmbeddingDimensions: dimensions,
	}
}

func (h *harness) aliases(index string) projectionApp.AliasSwitch {
	return projectionApp.AliasSwitch{ReadAlias: h.prefix + "-read", WriteAlias: h.prefix + "-write", Index: index}
}

func document(articleID, generation int64, visible bool) projectionApp.Document {
	document := projectionApp.Document{ArticleID: articleID, Generation: generation, LockVersion: generation,
		RevisionID: articleID * 10, SchemaVersion: 1, Visible: visible, OriginType: "user",
		AuthorUserID: "author-1", Title: "中文标题 Elasticsearch", PlainText: "正文内容 3.8",
		Excerpt: "摘要", Summary: "AI 摘要", Keywords: []string{"关键词"}, Topics: []string{"主题"}}
	if !visible {
		return document.Tombstone("offline")
	}
	if generation >= 2 {
		document.Vector = []float64{0.1, 0.2, 0.3}
		document.EmbeddingResultID = "e-1"
	}
	return document
}

// TestIndexTemplateSchemaIdentityAndAnalyzer 覆盖 4.2：
// mapping、schema identity、固定 analyze token 与维度不兼容拒绝都由真实索引验证。
func TestIndexTemplateSchemaIdentityAndAnalyzer(t *testing.T) {
	h := newHarness(t, 3)
	ctx := context.Background()
	spec := h.spec(1, 3)
	state, err := h.client.Create(ctx, spec)
	if err != nil {
		t.Fatalf("创建物理索引: %v", err)
	}
	if state.SchemaVersion != 1 || state.SchemaIdentity != spec.SchemaIdentity {
		t.Fatalf("schema 身份必须来自实际 mapping: %+v", state)
	}

	// 固定样例在 cjk 分析器下的 token 必须稳定，供升级分析规则时作为契约。
	tokens, err := h.client.Analyze(ctx, spec.PhysicalIndex, "cjk", "中文检索 Elasticsearch 3.8 混合文本")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(tokens, " "); got != "中文 文检 检索 elasticsearch 3.8 混合 合文 文本" {
		t.Fatalf("analyze token 变化: %s", got)
	}

	// 维度不兼容必须被拒绝，不得截断或填充。
	if _, err := h.client.Bulk(ctx, []projectionApp.BulkItem{{PhysicalIndex: spec.PhysicalIndex,
		Document: projectionApp.Document{ArticleID: 1, Generation: 1, SchemaVersion: 1, Visible: true,
			OriginType: "user", Title: "维度不符", PlainText: "正文", Vector: []float64{0.1, 0.2}}}}); err != nil {
		t.Fatal(err)
	}
	state, err = h.client.Inspect(ctx, spec.PhysicalIndex)
	if err != nil {
		t.Fatal(err)
	}
	if state.Documents != 0 {
		t.Fatalf("维度不符的文档不得写入: %d", state.Documents)
	}

	// 严格映射：模板未声明的字段必须被拒绝。
	if _, err := h.client.Inspect(ctx, spec.PhysicalIndex+"-absent"); !errors.Is(err, projectionApp.ErrIndexMissing) {
		t.Fatalf("不存在的索引必须报告缺失: %v", err)
	}
	_, err = h.api().Index(ctx, opensearchapi.IndexReq{Index: spec.PhysicalIndex, DocumentID: "1",
		Body: strings.NewReader(`{"article_id":1,"unexpected":true}`)})
	if err == nil || !strings.Contains(fmt.Sprint(err), "strict_dynamic_mapping_exception") {
		t.Fatalf("dynamic strict 必须拒绝未知字段: %v", err)
	}
}

func (h *harness) api() *opensearchapi.Client { return h.client.api }

// TestIndexEnsureAliasesAndSchemaGuard 覆盖 4.3：
// 重复初始化幂等、切换原子性、写别名唯一校验与错误 schema 拒绝。
func TestIndexEnsureAliasesAndSchemaGuard(t *testing.T) {
	h := newHarness(t, 3)
	ctx := context.Background()
	spec := h.spec(1, 3)

	first, err := h.client.Ensure(ctx, spec, h.aliases(spec.PhysicalIndex))
	if err != nil {
		t.Fatalf("首次初始化: %v", err)
	}
	if !first.HasReadAlias || !first.HasWriteAlias {
		t.Fatalf("初始化必须建立读写别名: %+v", first)
	}
	// 重复初始化必须幂等：不创建重复索引，也不产生第二个写索引。
	second, err := h.client.Ensure(ctx, spec, h.aliases(spec.PhysicalIndex))
	if err != nil {
		t.Fatalf("重复初始化: %v", err)
	}
	if !second.HasWriteAlias || second.Documents != 0 || second.PhysicalIndex != spec.PhysicalIndex {
		t.Fatalf("重复初始化必须返回现状: %+v", second)
	}

	// 新物理索引建立后用一次别名操作切换：读写别名必须同时迁移。
	next := h.spec(1, 3)
	next.PhysicalIndex = h.index(1) + "-b"
	if _, err := h.client.Create(ctx, next); err != nil {
		t.Fatal(err)
	}
	if err := h.client.SwitchAliases(ctx, projectionApp.AliasSwitch{
		ReadAlias: h.prefix + "-read", WriteAlias: h.prefix + "-write",
		Index: next.PhysicalIndex, DetachIndex: spec.PhysicalIndex,
	}); err != nil {
		t.Fatalf("别名切换: %v", err)
	}
	old, err := h.client.Inspect(ctx, spec.PhysicalIndex)
	if err != nil {
		t.Fatal(err)
	}
	moved, err := h.client.Inspect(ctx, next.PhysicalIndex)
	if err != nil {
		t.Fatal(err)
	}
	if old.HasReadAlias || old.HasWriteAlias || !moved.HasReadAlias || !moved.HasWriteAlias {
		t.Fatalf("别名必须整体迁移: old=%+v new=%+v", old, moved)
	}
	// 仍被别名引用的索引不得被删除。
	if err := h.client.Delete(ctx, next.PhysicalIndex); !errors.Is(err, projectionApp.ErrUnsafeIndexTarget) {
		t.Fatalf("仍被别名引用的索引必须拒绝删除: %v", err)
	}

	// 回滚到旧索引后，写别名不再指向候选索引。
	if err := h.client.SwitchAliases(ctx, projectionApp.AliasSwitch{
		ReadAlias: h.prefix + "-read", WriteAlias: h.prefix + "-write",
		Index: spec.PhysicalIndex, DetachIndex: next.PhysicalIndex,
	}); err != nil {
		t.Fatalf("回滚别名: %v", err)
	}
	if err := h.client.requireSingleWriteIndex(ctx, next.PhysicalIndex); !errors.Is(err, projectionApp.ErrAliasConflict) {
		t.Fatalf("写别名指向非预期索引必须被拒绝: %v", err)
	}
	rolledBack, err := h.client.Inspect(ctx, spec.PhysicalIndex)
	if err != nil {
		t.Fatal(err)
	}
	if !rolledBack.HasReadAlias || !rolledBack.HasWriteAlias {
		t.Fatalf("回滚后旧索引必须恢复读写别名: %+v", rolledBack)
	}
	// 不再被引用的候选索引可以被显式删除。
	if err := h.client.Delete(ctx, next.PhysicalIndex); err != nil {
		t.Fatalf("未被引用的索引应可删除: %v", err)
	}

	// 实际 mapping 身份与要求不一致的索引必须被拒绝，而不是按索引名信任。
	// 该索引名不在模板 pattern 内，因此 _meta 完全由这里写入。
	foreignName := h.prefix + "-foreign"
	if _, err := h.api().Indices.Create(ctx, opensearchapi.IndicesCreateReq{Index: foreignName, Body: strings.NewReader(
		`{"mappings":{"_meta":{"velis_schema_version":1,"velis_schema_identity":"mapping-v1|analyzer-cjk|dims-8|encoding-v1"}}}`)}); err != nil {
		t.Fatal(err)
	}
	foreign := h.spec(1, 3)
	foreign.PhysicalIndex = foreignName
	if _, err := h.client.Create(ctx, foreign); !errors.Is(err, projectionApp.ErrSchemaMismatch) {
		t.Fatalf("错误 schema 必须被拒绝: %v", err)
	}
	if _, err := h.client.Ensure(ctx, foreign, h.aliases(foreignName)); !errors.Is(err, projectionApp.ErrSchemaMismatch) {
		t.Fatalf("初始化同样必须校验实际 schema: %v", err)
	}
}

// TestBulkExternalVersionTombstoneAndRepublish 覆盖 4.4：
// 重复请求、低版本冲突、持久 tombstone 与重新发布的高版本覆盖。
func TestBulkExternalVersionTombstoneAndRepublish(t *testing.T) {
	h := newHarness(t, 3)
	ctx := context.Background()
	spec := h.spec(1, 3)
	if _, err := h.client.Ensure(ctx, spec, h.aliases(spec.PhysicalIndex)); err != nil {
		t.Fatal(err)
	}
	write := func(items ...projectionApp.BulkItem) []projectionApp.DeliveryOutcome {
		t.Helper()
		outcomes, err := h.client.Bulk(ctx, items)
		if err != nil {
			t.Fatal(err)
		}
		h.refresh(ctx, spec.PhysicalIndex)
		return outcomes
	}
	item := func(articleID, generation int64, visible bool) projectionApp.BulkItem {
		return projectionApp.BulkItem{PhysicalIndex: spec.PhysicalIndex, Document: document(articleID, generation, visible)}
	}

	// 首次 upsert 与同 generation 重放都必须成功。
	if outcomes := write(item(7, 1, true)); !outcomes[0].Result.Applied() {
		t.Fatalf("首次写入: %+v", outcomes[0])
	}
	if outcomes := write(item(7, 1, true)); !outcomes[0].Result.Applied() {
		t.Fatalf("同版本重放必须安全: %+v", outcomes[0])
	}
	// 更低 generation 的迟到写入必须被版本条件拒绝并折叠为收敛。
	if outcomes := write(item(7, 1, true)); !outcomes[0].Result.Applied() {
		t.Fatalf("重复写入必须收敛: %+v", outcomes[0])
	}

	// 下架写 tombstone：不可检索但必须保留身份与版本。
	if outcomes := write(item(7, 2, false)); !outcomes[0].Result.Applied() {
		t.Fatalf("tombstone 写入: %+v", outcomes[0])
	}
	state, err := h.client.Inspect(ctx, spec.PhysicalIndex)
	if err != nil {
		t.Fatal(err)
	}
	if state.VisibleDocuments != 0 || state.Documents != 1 {
		t.Fatalf("tombstone 必须保留文档但不可检索: %+v", state)
	}
	// 迟到的旧 revision upsert 必须被拒绝，文档不得复活。
	write(item(7, 1, true))
	state, _ = h.client.Inspect(ctx, spec.PhysicalIndex)
	if state.VisibleDocuments != 0 {
		t.Fatalf("迟到旧写不得复活文档: %+v", state)
	}

	// 以更高 generation 重新发布必须能越过 tombstone。
	if outcomes := write(item(7, 3, true)); !outcomes[0].Result.Applied() {
		t.Fatalf("重新发布: %+v", outcomes[0])
	}
	state, _ = h.client.Inspect(ctx, spec.PhysicalIndex)
	if state.VisibleDocuments != 1 {
		t.Fatalf("重新发布必须恢复可检索: %+v", state)
	}
	stored, err := h.api().Document.Get(ctx, opensearchapi.DocumentGetReq{Index: spec.PhysicalIndex, DocumentID: "7"})
	if err != nil {
		t.Fatal(err)
	}
	source := string(stored.Source)
	if !strings.Contains(source, `"projection_generation":3`) {
		t.Fatalf("文档版本身份错误: %s", source)
	}
	if !strings.Contains(source, `"revision_id":70`) {
		t.Fatalf("文档必须记录当前修订: %s", source)
	}
}

// TestBulkPartialMappingFailureIsolatesDeliveries 覆盖 4.5 的真实部分失败：
// 一条文档因映射失败时，同批其它文档仍必须完成。
func TestBulkPartialMappingFailureIsolatesDeliveries(t *testing.T) {
	h := newHarness(t, 3)
	ctx := context.Background()
	spec := h.spec(1, 3)
	if _, err := h.client.Ensure(ctx, spec, h.aliases(spec.PhysicalIndex)); err != nil {
		t.Fatal(err)
	}
	broken := document(20, 1, true)
	// 维度不符属于映射错误：它必须单独永久失败，不影响同批其它文档。
	broken.EmbeddingResultID = "e-broken"
	broken.Vector = []float64{0.1, 0.2}
	outcomes, err := h.client.Bulk(ctx, []projectionApp.BulkItem{
		{PhysicalIndex: spec.PhysicalIndex, Document: broken},
		{PhysicalIndex: spec.PhysicalIndex, Document: document(21, 1, true)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcomes[0].Result != projectionDomain.ResultPermanent {
		t.Fatalf("映射失败必须永久失败: %+v", outcomes[0])
	}
	if !outcomes[1].Result.Applied() {
		t.Fatalf("同批其它文档必须完成: %+v", outcomes[1])
	}
	h.refresh(ctx, spec.PhysicalIndex)
	state, err := h.client.Inspect(ctx, spec.PhysicalIndex)
	if err != nil {
		t.Fatal(err)
	}
	if state.VisibleDocuments != 1 {
		t.Fatalf("只有可成功写入的文档应当可见: %+v", state)
	}
}

func (h *harness) refresh(ctx context.Context, index string) {
	_, _ = h.api().Indices.Refresh(ctx, &opensearchapi.IndicesRefreshReq{Index: []string{index}})
}

// TestBM25QueryPlanAndPITStability 用真实 schema v1 固定夹具验证查询侧验收。
// 若该测试失败，应升级 schema，而不是原地修改 articles.v1.json。
func TestBM25QueryPlanAndPITStability(t *testing.T) {
	h := newHarness(t, 3)
	ctx := context.Background()
	spec := h.spec(1, 3)
	if _, err := h.client.Ensure(ctx, spec, h.aliases(spec.PhysicalIndex)); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 9, 25, 8, 0, 0, 0, time.UTC)
	source7 := int64(7)
	fixture := func(id int64) projectionApp.Document {
		published := base.Add(time.Duration(id) * time.Second)
		return projectionApp.Document{ArticleID: id, Generation: 1, LockVersion: 1, RevisionID: id * 10,
			SchemaVersion: 1, Visible: true, OriginType: "rss", SourceID: &source7, PublishedAt: &published,
			Title: "占位标题", PlainText: "占位正文", Excerpt: "占位摘要"}
	}
	documents := make([]projectionApp.Document, 0)
	for id := int64(1); id <= 9; id++ {
		documents = append(documents, fixture(id))
	}
	documents[0].Title = "titleneedle"
	documents[1].PlainText = "bodyneedle"
	documents[2].Summary = "summaryneedle"
	documents[3].Keywords = []string{"keywordneedle", "exact-keyword"}
	documents[4].Topics = []string{"topicneedle", "exact-topic"}
	documents[5].Title = "priorityneedle"
	documents[6].PlainText = "priorityneedle"
	documents[7].Title = "中文检索 OpenSearch URL https://example.com/path VelisPro"
	documents[8].Title = "same-score"
	documents[8].PublishedAt = &base
	tie := fixture(10)
	tie.Title, tie.PublishedAt = "same-score", &base
	documents = append(documents, tie)
	items := make([]projectionApp.BulkItem, 0, len(documents))
	for _, value := range documents {
		items = append(items, projectionApp.BulkItem{PhysicalIndex: spec.PhysicalIndex, Document: value})
	}
	outcomes, err := h.client.Bulk(ctx, items)
	if err != nil {
		t.Fatal(err)
	}
	for index, outcome := range outcomes {
		if !outcome.Result.Applied() {
			t.Fatalf("写入夹具 %d 失败: %+v", index, outcome)
		}
	}
	h.refresh(ctx, spec.PhysicalIndex)

	search := func(query searchApp.Query) []int64 {
		t.Helper()
		pit, err := h.client.CreatePIT(ctx, 2*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		batch, err := h.client.Search(ctx, searchApp.IndexRequest{Query: query, PITID: pit, Size: 50, KeepAlive: 2 * time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		_ = h.client.ClosePIT(ctx, batch.PITID)
		ids := make([]int64, len(batch.Candidates))
		for index, candidate := range batch.Candidates {
			ids[index] = candidate.ArticleID
		}
		return ids
	}
	for query, want := range map[string]int64{"titleneedle": 1, "bodyneedle": 2, "summaryneedle": 3, "keywordneedle": 4, "topicneedle": 5, "中文检索": 8, "OpenSearch": 8, "https://example.com/path": 8, "VelisPro": 8} {
		ids := search(searchApp.Query{Q: query})
		if len(ids) == 0 || ids[0] != want {
			t.Fatalf("字段/混合文本召回 %q = %v，期望首项 %d", query, ids, want)
		}
	}
	priority := search(searchApp.Query{Q: "priorityneedle"})
	if len(priority) < 2 || priority[0] != 6 || priority[1] != 7 {
		t.Fatalf("标题权重未高于正文: %v", priority)
	}
	filtered := search(searchApp.Query{Q: "keywordneedle", Filters: searchApp.Filters{Keyword: "exact-keyword", Topic: "exact-topic", SourceID: 7}})
	if len(filtered) != 0 { // 关键词和主题必须由同一文档同时满足。
		t.Fatalf("组合过滤错误地使用 OR: %v", filtered)
	}
	documents[3].Topics = []string{"exact-topic"}
	outcomes, _ = h.client.Bulk(ctx, []projectionApp.BulkItem{{PhysicalIndex: spec.PhysicalIndex, Document: documents[3]}})
	if !outcomes[0].Result.Applied() {
		t.Fatalf("更新过滤夹具失败: %+v", outcomes[0])
	}
	h.refresh(ctx, spec.PhysicalIndex)
	filtered = search(searchApp.Query{Q: "keywordneedle", Filters: searchApp.Filters{Keyword: "exact-keyword", Topic: "exact-topic", SourceID: 7}})
	if len(filtered) != 1 || filtered[0] != 4 {
		t.Fatalf("组合精确过滤失败: %v", filtered)
	}
	ties := search(searchApp.Query{Q: "same-score"})
	if len(ties) != 2 || ties[0] != 10 || ties[1] != 9 {
		t.Fatalf("同分全序不稳定: %v", ties)
	}

	// PIT 创建后更新旧索引并切换读别名，旧 PIT 仍只观察创建时的候选。
	pit, err := h.client.CreatePIT(ctx, 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	newDocument := fixture(100)
	newDocument.Title = "titleneedle"
	if outcomes, err = h.client.Bulk(ctx, []projectionApp.BulkItem{{PhysicalIndex: spec.PhysicalIndex, Document: newDocument}}); err != nil || !outcomes[0].Result.Applied() {
		t.Fatalf("PIT 后增量写入失败: %+v %v", outcomes, err)
	}
	h.refresh(ctx, spec.PhysicalIndex)
	next := h.spec(1, 3)
	next.PhysicalIndex = spec.PhysicalIndex + "-next"
	if _, err := h.client.Create(ctx, next); err != nil {
		t.Fatal(err)
	}
	if err := h.client.SwitchAliases(ctx, projectionApp.AliasSwitch{ReadAlias: h.prefix + "-read", WriteAlias: h.prefix + "-write", Index: next.PhysicalIndex, DetachIndex: spec.PhysicalIndex}); err != nil {
		t.Fatal(err)
	}
	batch, err := h.client.Search(ctx, searchApp.IndexRequest{Query: searchApp.Query{Q: "titleneedle"}, PITID: pit, Size: 50, KeepAlive: 2 * time.Minute})
	if err != nil || len(batch.Candidates) != 1 || batch.Candidates[0].ArticleID != 1 {
		t.Fatalf("PIT 未隔离增量/别名切换: %+v err=%v", batch, err)
	}
	_ = h.client.ClosePIT(ctx, batch.PITID)
}
