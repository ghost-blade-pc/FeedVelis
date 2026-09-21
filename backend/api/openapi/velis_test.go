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
		"/articles", "/articles/{article_id}",
		"/me/articles", "/me/articles/preview", "/me/articles/{article_id}",
		"/me/articles/{article_id}/publish", "/me/articles/{article_id}/offline",
		"/me/assets", "/me/assets/{asset_id}/confirm", "/assets/{asset_id}/content",
		"/admin/articles/{article_id}/offline", "/admin/articles/{article_id}/restore",
		"/admin/sources", "/admin/sources/{source_id}",
		"/admin/sources/{source_id}/pause", "/admin/sources/{source_id}/resume",
		"/admin/sources/{source_id}/fetches",
	}
	for _, path := range requiredPaths {
		if _, ok := paths[path]; !ok {
			t.Errorf("缺少 I2 路径 %s", path)
		}
	}

	operationIDs := make(map[string]string)
	walkContract(t, document, document, operationIDs, "#")
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
