package openapi

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestVelisOpenAPIContract(t *testing.T) {
	raw, err := os.ReadFile("velis.yaml")
	if err != nil {
		t.Fatalf("读取 OpenAPI 契约失败: %v", err)
	}

	var document map[string]any
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatalf("OpenAPI YAML 无效: %v", err)
	}
	if got := document["openapi"]; got != "3.1.0" {
		t.Fatalf("OpenAPI 版本 = %v，期望 3.1.0", got)
	}

	paths := objectAt(t, document, "paths")
	requiredPaths := []string{
		"/articles", "/articles/{article_id}", "/search/articles",
		"/me/articles", "/me/articles/preview", "/me/articles/{article_id}",
		"/me/articles/{article_id}/publish", "/me/articles/{article_id}/offline",
		"/me/assets", "/me/assets/{asset_id}/confirm", "/assets/{asset_id}/content",
		"/admin/articles/{article_id}/offline", "/admin/articles/{article_id}/restore",
		"/admin/sources", "/admin/sources/{source_id}",
		"/admin/sources/{source_id}/pause", "/admin/sources/{source_id}/resume",
		"/admin/sources/{source_id}/fetches",
	}
	assertArticleSearchContract(t, document, paths)
	for _, path := range requiredPaths {
		if _, ok := paths[path]; !ok {
			t.Errorf("缺少 I2 路径 %s", path)
		}
	}

	operationIDs := make(map[string]string)
	walkContract(t, document, document, operationIDs, "#")
}

func assertArticleSearchContract(t *testing.T, document, paths map[string]any) {
	t.Helper()
	search := objectAt(t, objectAt(t, paths, "/search/articles"), "get")
	parameters, ok := search["parameters"].([]any)
	if !ok || len(parameters) != 6 {
		t.Fatalf("搜索参数数量错误: %T %v", search["parameters"], search["parameters"])
	}
	byName := make(map[string]map[string]any, len(parameters))
	for _, raw := range parameters {
		parameter := raw.(map[string]any)
		byName[parameter["name"].(string)] = parameter
	}
	if byName["q"]["required"] != true || objectAt(t, byName["q"], "schema")["maxLength"] != 200 {
		t.Fatalf("q 必须 required 且最多 200 字符: %+v", byName["q"])
	}
	if objectAt(t, byName["limit"], "schema")["maximum"] != 50 || objectAt(t, byName["source_id"], "schema")["minimum"] != 1 {
		t.Fatalf("limit/source_id 边界错误: %+v %+v", byName["limit"], byName["source_id"])
	}
	responses := objectAt(t, search, "responses")
	for _, status := range []string{"200", "400", "503"} {
		if _, ok := responses[status]; !ok {
			t.Fatalf("搜索缺少 %s 响应", status)
		}
	}
	schemas := objectAt(t, objectAt(t, document, "components"), "schemas")
	page := objectAt(t, schemas, "ArticleSearchPage")
	properties := objectAt(t, page, "properties")
	items := objectAt(t, objectAt(t, properties, "items"), "items")
	if items["$ref"] != "#/components/schemas/ArticleItem" {
		t.Fatalf("搜索结果必须复用 ArticleItem: %+v", items)
	}
	types, ok := objectAt(t, properties, "next_cursor")["type"].([]any)
	if !ok || len(types) != 2 || types[0] != "string" || types[1] != "null" {
		t.Fatalf("next_cursor 必须 required nullable string: %+v", properties["next_cursor"])
	}
}

func walkContract(t *testing.T, root, value map[string]any, operationIDs map[string]string, location string) {
	t.Helper()
	for key, item := range value {
		itemLocation := location + "/" + key
		switch typed := item.(type) {
		case map[string]any:
			walkContract(t, root, typed, operationIDs, itemLocation)
		case []any:
			for index, child := range typed {
				if object, ok := child.(map[string]any); ok {
					walkContract(t, root, object, operationIDs, fmt.Sprintf("%s/%d", itemLocation, index))
				}
			}
		case string:
			if key == "$ref" {
				if !strings.HasPrefix(typed, "#/") {
					t.Errorf("%s 使用了不受支持的外部引用 %q", itemLocation, typed)
					continue
				}
				if !localReferenceExists(root, typed) {
					t.Errorf("%s 引用了不存在的节点 %q", itemLocation, typed)
				}
			}
			if key == "operationId" {
				if previous, exists := operationIDs[typed]; exists {
					t.Errorf("operationId %q 重复：%s 与 %s", typed, previous, itemLocation)
				} else {
					operationIDs[typed] = itemLocation
				}
			}
		}
	}
}

func localReferenceExists(root map[string]any, reference string) bool {
	var current any = root
	for _, rawPart := range strings.Split(strings.TrimPrefix(reference, "#/"), "/") {
		part := strings.ReplaceAll(strings.ReplaceAll(rawPart, "~1", "/"), "~0", "~")
		object, ok := current.(map[string]any)
		if !ok {
			return false
		}
		current, ok = object[part]
		if !ok {
			return false
		}
	}
	return true
}

func objectAt(t *testing.T, object map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := object[key].(map[string]any)
	if !ok {
		t.Fatalf("OpenAPI %s 必须是对象", key)
	}
	return value
}
