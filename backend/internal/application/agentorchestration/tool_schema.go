package agentorchestration

import "strings"

// ToolOutputSchema 描述适配层交给模型的实际包裹格式；底层三个工具的原 JSON 契约保持独立。
func ToolOutputSchema(name string) string {
	ref := `{"type":"object","additionalProperties":false,"required":["article_id","revision_id","title","origin_type","source","author","path","original_url","summary","summary_source","metadata_truncated"],"properties":{"article_id":{"type":"integer","minimum":1},"revision_id":{"type":"integer","minimum":1},"title":{"type":"string","maxLength":256},"origin_type":{"enum":["rss","user"]},"source":{"type":["object","null"],"properties":{"id":{"type":"integer"},"title":{"type":"string"}}},"author":{"type":["object","null"],"properties":{"id":{"type":"string"},"nickname":{"type":"string"},"name":{"type":"string"}}},"path":{"type":"string"},"original_url":{"type":["string","null"]},"summary":{"type":"string","maxLength":1000},"summary_source":{"enum":["excerpt","extractive","model"]},"metadata_truncated":{"type":"boolean"}}}`
	// get/recommend 的对象继承可信 ArticleRef，新增字段在其最终对象 schema 内声明。
	get := ref[:len(ref)-2] + `,"content":{"type":"string","maxLength":8000},"truncated":{"type":"boolean"}}}`
	recommend := ref[:len(ref)-2] + `,"recommendation_reason":{"type":"string"}}}`
	get = strings.Replace(get, `"required":["article_id"`, `"required":["content","truncated","article_id"`, 1)
	recommend = strings.Replace(recommend, `"required":["article_id"`, `"required":["recommendation_reason","article_id"`, 1)
	result := `{"type":"object","additionalProperties":false,"required":["items","truncated"],"properties":{"items":{"type":"array","maxItems":10,"items":` + ref + `},"truncated":{"type":"boolean"}}}`
	if name == "get_article" {
		result = get
	}
	if name == "recommend_articles" {
		result = `{"type":"object","additionalProperties":false,"required":["items","truncated","mode","degraded","degrade_reason"],"properties":{"items":{"type":"array","maxItems":10,"items":` + recommend + `},"truncated":{"type":"boolean"},"mode":{"type":"string"},"degraded":{"type":"boolean"},"degrade_reason":{"type":["string","null"]}}}`
	}
	return `{"type":"object","additionalProperties":false,"required":["result","evidence"],"properties":{"result":` + result + `,"evidence":{"type":"array","maxItems":10,"items":{"type":"object","additionalProperties":false,"required":["evidence_id","content_kind","text"],"properties":{"evidence_id":{"type":"string","format":"uuid"},"content_kind":{"enum":["summary","body"]},"text":{"type":"string"}}}}}}`
}
