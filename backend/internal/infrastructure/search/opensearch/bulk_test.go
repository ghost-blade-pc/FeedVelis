package opensearch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	projectionApp "github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/searchprojection"
	projectionDomain "github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/searchprojection"
)

const testIndex = "velis-articles-v1-20260925t120000z-aaaaaa"

func testConfig(endpoint string) Config {
	return Config{
		Endpoints: []string{endpoint}, IndexPrefix: "velis-articles", SchemaVersion: 1,
		EmbeddingDimensions: 3, BulkMaxItems: 10, BulkMaxBytes: 1 << 20, BulkMaxDocumentChars: 1000,
	}
}

func testItem(articleID int64, generation int64, visible bool) projectionApp.BulkItem {
	return projectionApp.BulkItem{
		PhysicalIndex: testIndex,
		Document: projectionApp.Document{ArticleID: articleID, Generation: generation, SchemaVersion: 1,
			Visible: visible, InvisibleReason: map[bool]string{true: "", false: "offline"}[visible],
			OriginType: "user", Title: "标题", PlainText: "正文"},
	}
}

func newStubClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := New(testConfig(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func writeBulkResponse(status int, body string) http.HandlerFunc {
	return func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(status)
		_, _ = writer.Write([]byte(body))
	}
}

// TestBulkClassifiesPartialSuccessPerItem 覆盖 4.5：一次 HTTP 成功不等于全部 item 成功。
func TestBulkClassifiesPartialSuccessPerItem(t *testing.T) {
	body := `{"took":3,"errors":true,"items":[
{"index":{"_index":"velis-articles-v1-20260925t120000z-aaaaaa","_id":"7","status":201,"result":"created"}},
{"index":{"_index":"velis-articles-v1-20260925t120000z-aaaaaa","_id":"8","status":400,"error":{"type":"mapper_parsing_exception","reason":"failed to parse field [vector]"}}}]}`
	client := newStubClient(t, writeBulkResponse(http.StatusOK, body))
	outcomes, err := client.Bulk(context.Background(), []projectionApp.BulkItem{testItem(7, 3, true), testItem(8, 3, true)})
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 2 {
		t.Fatalf("分类数量 = %d", len(outcomes))
	}
	if !outcomes[0].Result.Applied() {
		t.Fatalf("成功项必须视为已收敛: %+v", outcomes[0])
	}
	if outcomes[0].Result != projectionDomain.ResultUpdated {
		t.Fatalf("可检索文档写入结果 = %s", outcomes[0].Result)
	}
	if outcomes[1].Result != projectionDomain.ResultPermanent || outcomes[1].Code != projectionApp.ErrorMapping {
		t.Fatalf("映射错误必须是永久失败: %+v", outcomes[1])
	}
}

func TestBulkClassifiesTombstoneAndNoop(t *testing.T) {
	body := `{"took":1,"errors":false,"items":[
{"index":{"_index":"velis-articles-v1-20260925t120000z-aaaaaa","_id":"7","status":200,"result":"updated"}},
{"index":{"_index":"velis-articles-v1-20260925t120000z-aaaaaa","_id":"8","status":200,"result":"noop"}}]}`
	client := newStubClient(t, writeBulkResponse(http.StatusOK, body))
	outcomes, err := client.Bulk(context.Background(), []projectionApp.BulkItem{testItem(7, 3, false), testItem(8, 3, true)})
	if err != nil {
		t.Fatal(err)
	}
	if outcomes[0].Result != projectionDomain.ResultTombstoned {
		t.Fatalf("tombstone 写入结果 = %s", outcomes[0].Result)
	}
	// tombstone 已达到同版或更高版时视为已收敛成功。
	if outcomes[1].Result != projectionDomain.ResultNoop || !outcomes[1].Result.Applied() {
		t.Fatalf("同版本重放必须视为收敛: %+v", outcomes[1])
	}
}

func TestBulkTreatsVersionConflictAsConverged(t *testing.T) {
	body := `{"took":1,"errors":true,"items":[
{"index":{"_index":"velis-articles-v1-20260925t120000z-aaaaaa","_id":"7","status":409,"error":{"type":"version_conflict_engine_exception","reason":"[7]: version conflict, current version [9] is higher than the one provided [3]"}}}]}`
	client := newStubClient(t, writeBulkResponse(http.StatusOK, body))
	outcomes, err := client.Bulk(context.Background(), []projectionApp.BulkItem{testItem(7, 3, true)})
	if err != nil {
		t.Fatal(err)
	}
	if outcomes[0].Result != projectionDomain.ResultNoop {
		t.Fatalf("陈旧写入必须折叠为收敛: %+v", outcomes[0])
	}
	// 已收敛的结果不携带错误诊断，未知分类也必须被折叠。
	if normalized := outcomes[0].Normalize(); !normalized.Code.Known() || normalized.Code != projectionApp.ErrorUnclassified {
		t.Fatalf("收敛结果必须折叠为受控分类: %+v", normalized)
	}
	if failure := outcomes[0].Failure(); failure != nil {
		t.Fatalf("已收敛的结果不得留下失败诊断: %+v", failure)
	}
}

func TestBulkRetriesThrottledAndServerErrors(t *testing.T) {
	cases := map[string]struct {
		status   int
		itemBody string
		want     projectionApp.ErrorCode
	}{
		"限流":    {status: 200, itemBody: `{"index":{"_index":"velis-articles-v1-20260925t120000z-aaaaaa","_id":"7","status":429,"error":{"type":"es_rejected_execution_exception","reason":"rejected"}}}`, want: projectionApp.ErrorThrottled},
		"服务端错误": {status: 200, itemBody: `{"index":{"_index":"velis-articles-v1-20260925t120000z-aaaaaa","_id":"7","status":503,"error":{"type":"unavailable_shards_exception","reason":"unavailable"}}}`, want: projectionApp.ErrorConnection},
		"请求超时":  {status: 200, itemBody: `{"index":{"_index":"velis-articles-v1-20260925t120000z-aaaaaa","_id":"7","status":408,"error":{"type":"timeout_exception","reason":"timed out"}}}`, want: projectionApp.ErrorTimeout},
	}
	for name, item := range cases {
		client := newStubClient(t, writeBulkResponse(item.status, `{"took":1,"errors":true,"items":[`+item.itemBody+`]}`))
		outcomes, err := client.Bulk(context.Background(), []projectionApp.BulkItem{testItem(7, 3, true)})
		if err != nil {
			t.Fatal(err)
		}
		if outcomes[0].Result != projectionDomain.ResultRetryable || outcomes[0].Code != item.want {
			t.Fatalf("%s: 必须可重试并带受控分类: %+v", name, outcomes[0])
		}
	}
}

func TestBulkTreatsUnreliableResponsesAsRetryable(t *testing.T) {
	// 只有无法可靠关联的操作才需要重试；同一响应里能关联的项仍按真实结果收敛。
	cases := map[string]struct {
		body     string
		retrying []int
		applied  []int
	}{
		"响应缺项": {
			body:     `{"took":1,"errors":false,"items":[{"index":{"_index":"` + testIndex + `","_id":"7","status":201,"result":"created"}}]}`,
			retrying: []int{0, 1},
		},
		"响应非 JSON": {
			body:     `not-json`,
			retrying: []int{0, 1},
		},
		"动作数量异常": {
			body:     `{"took":1,"errors":false,"items":[{"index":{"_index":"` + testIndex + `","_id":"7","status":201,"result":"created"},"delete":{"_index":"` + testIndex + `","_id":"7","status":200}},{"index":{"_index":"` + testIndex + `","_id":"8","status":201,"result":"created"}}]}`,
			retrying: []int{0},
			applied:  []int{1},
		},
		"身份不一致": {
			body:     `{"took":1,"errors":false,"items":[{"index":{"_index":"` + testIndex + `","_id":"999","status":201,"result":"created"}},{"index":{"_index":"` + testIndex + `","_id":"8","status":201,"result":"created"}}]}`,
			retrying: []int{0},
			applied:  []int{1},
		},
		"索引不一致": {
			body:     `{"took":1,"errors":false,"items":[{"index":{"_index":"other-index","_id":"7","status":201,"result":"created"}},{"index":{"_index":"` + testIndex + `","_id":"8","status":201,"result":"created"}}]}`,
			retrying: []int{0},
			applied:  []int{1},
		},
	}
	for name, item := range cases {
		client := newStubClient(t, writeBulkResponse(http.StatusOK, item.body))
		outcomes, err := client.Bulk(context.Background(), []projectionApp.BulkItem{testItem(7, 3, true), testItem(8, 3, true)})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(outcomes) != 2 {
			t.Fatalf("%s: 分类数量 = %d", name, len(outcomes))
		}
		for _, index := range item.retrying {
			outcome := outcomes[index]
			if outcome.Result.Applied() {
				t.Fatalf("%s: 第 %d 项无法可靠关联时必须可重试: %+v", name, index, outcome)
			}
			if outcome.StatusFor(1, 3) != projectionDomain.StatusRetryWait {
				t.Fatalf("%s: 第 %d 项必须安排重试: %s", name, index, outcome.StatusFor(1, 3))
			}
			if outcome.Code != projectionApp.ErrorResponseInvalid && outcome.Code != projectionApp.ErrorConnection {
				t.Fatalf("%s: 第 %d 项错误分类 = %s", name, index, outcome.Code)
			}
		}
		for _, index := range item.applied {
			if !outcomes[index].Result.Applied() {
				t.Fatalf("%s: 第 %d 项已可靠关联必须收敛: %+v", name, index, outcomes[index])
			}
		}
	}
}

func TestBulkReportsUnknownResultOnConnectionFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		// 在服务端处理期间中断连接：客户端无法判断结果。
		hijacker, ok := writer.(http.Hijacker)
		if !ok {
			t.Error("测试服务不支持连接劫持")
			return
		}
		connection, _, err := hijacker.Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = connection.Close()
	}))
	defer server.Close()
	client, err := New(testConfig(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	outcomes, err := client.Bulk(context.Background(), []projectionApp.BulkItem{testItem(7, 3, true)})
	if err != nil {
		t.Fatalf("连接失败必须按分类返回而不是整体错误: %v", err)
	}
	if outcomes[0].Result != projectionDomain.ResultUnknown || outcomes[0].Result.Applied() {
		t.Fatalf("结果未知必须保留任务: %+v", outcomes[0])
	}
	if outcomes[0].StatusFor(1, 3) != projectionDomain.StatusRetryWait {
		t.Fatalf("结果未知必须可重试: %s", outcomes[0].StatusFor(1, 3))
	}
}

func TestBulkFailsOversizedDocumentsWithoutSendingThem(t *testing.T) {
	var calls int
	client := newStubClient(t, func(writer http.ResponseWriter, _ *http.Request) {
		calls++
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"took":1,"errors":false,"items":[]}`))
	})
	client.cfg.BulkMaxDocumentChars = 1000
	oversized := testItem(7, 3, true)
	oversized.Document.PlainText = strings.Repeat("字", 1200)
	normal := testItem(8, 3, true)
	normal.Document.PlainText = "短正文"
	outcomes, err := client.Bulk(context.Background(), []projectionApp.BulkItem{oversized, normal})
	if err != nil {
		t.Fatal(err)
	}
	if outcomes[0].Result != projectionDomain.ResultPermanent || outcomes[0].Code != projectionApp.ErrorDocumentTooLarge {
		t.Fatalf("超大文档必须单独永久失败: %+v", outcomes[0])
	}
	if calls != 1 {
		t.Fatalf("只应为剩余文档发送一次请求，实际 %d 次", calls)
	}
}

func TestBulkSplitsByIndexCountAndBytes(t *testing.T) {
	var bodies []string
	client := newStubClient(t, func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		bodies = append(bodies, string(body))
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"took":1,"errors":false,"items":[
{"index":{"_index":"velis-articles-v1-20260925t120000z-aaaaaa","_id":"7","status":201,"result":"created"}},
{"index":{"_index":"velis-articles-v2-20260925t130000z-bbbbbb","_id":"8","status":201,"result":"created"}},
{"index":{"_index":"velis-articles-v1-20260925t120000z-aaaaaa","_id":"9","status":201,"result":"created"}}]}`))
	})
	client.cfg.BulkMaxItems = 2
	first, second, third := testItem(7, 1, true), testItem(8, 1, true), testItem(9, 1, true)
	second.PhysicalIndex = "velis-articles-v2-20260925t130000z-bbbbbb"
	third.PhysicalIndex = first.PhysicalIndex
	outcomes, err := client.Bulk(context.Background(), []projectionApp.BulkItem{first, second, third})
	if err != nil {
		t.Fatal(err)
	}
	// 物理索引切换必须切分；同一索引内按 item 数上限切分。
	if len(bodies) != 3 {
		t.Fatalf("切分请求数 = %d，期望 3", len(bodies))
	}
	if len(outcomes) != 3 {
		t.Fatalf("分类数量 = %d", len(outcomes))
	}
	for _, body := range bodies {
		if len(strings.Split(strings.TrimRight(body, "\n"), "\n")) != 2 {
			t.Fatalf("每个请求必须只有一组动作行与文档行: %q", body)
		}
	}
}

func TestBuildBulkBodyUsesExternalGteVersion(t *testing.T) {
	body, err := buildBulkBody("velis-articles-v1-test", []projectionApp.BulkItem{testItem(7, 4, true)})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(body), "\n"), "\n")
	var action struct {
		Index struct {
			ID          string `json:"_id"`
			Index       string `json:"_index"`
			Version     int64  `json:"version"`
			VersionType string `json:"version_type"`
		} `json:"index"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &action); err != nil {
		t.Fatal(err)
	}
	if action.Index.ID != "7" || action.Index.Version != 4 || action.Index.VersionType != "external_gte" {
		t.Fatalf("动作行必须携带文章 ID 与外部版本: %s", lines[0])
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &document); err != nil {
		t.Fatal(err)
	}
	if _, ok := document["vector"]; ok {
		t.Fatal("空向量不得出现在严格映射文档里")
	}
	if document["visible"] != true {
		t.Fatalf("可见性必须始终显式发送: %v", document["visible"])
	}
	for _, forbidden := range []string{"sanitized_html", "user_markdown", "raw_content"} {
		if _, ok := document[forbidden]; ok {
			t.Fatalf("投影文档不得包含 %s", forbidden)
		}
	}
}
