// Package markdown 提供用户稿件 Markdown 的基础解析引擎。
package markdown

import "github.com/yuin/goldmark"

// NewEngine 创建默认禁用原始 HTML 输出的 Goldmark 引擎。
// 完整的资产协议、白名单清洗和摘要管线由文章内容适配器在此基础上组合。
func NewEngine() goldmark.Markdown {
	return goldmark.New()
}
