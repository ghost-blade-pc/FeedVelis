package eino

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/application/strictjson"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/infrastructure/config"
)

// readingModel 保留 usage 字段的缺失性，避免 SDK 把未报告字段默认为零。
// 它只适配 Eino 模型端口；调用次数、授权和业务证据由外层受控编排负责。
type readingModel struct {
	cfg    config.AgentGenerationConfig
	client *http.Client
	tools  []*schema.ToolInfo
}

func newReadingModel(cfg config.AgentGenerationConfig) *readingModel {
	return &readingModel{cfg: cfg, client: &http.Client{Timeout: cfg.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (m *readingModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	copy := *m
	copy.tools = append([]*schema.ToolInfo(nil), tools...)
	return &copy, nil
}

func (m *readingModel) requestBody(messages []*schema.Message, stream bool) ([]byte, error) {
	items := make([]map[string]any, 0, len(messages))
	for _, v := range messages {
		if v == nil {
			return nil, &agentrun.Error{Code: agentrun.CodeModelIncompatible}
		}
		item := map[string]any{"role": v.Role, "content": v.Content}
		if len(v.ToolCalls) > 0 {
			item["tool_calls"] = v.ToolCalls
		}
		if v.ToolCallID != "" {
			item["tool_call_id"] = v.ToolCallID
		}
		items = append(items, item)
	}
	request := map[string]any{"model": m.cfg.Model, "messages": items, "stream": stream, m.cfg.MaxTokensParameter: m.cfg.MaxOutputTokens}
	if stream {
		request["stream_options"] = map[string]bool{"include_usage": true}
	}
	if len(m.tools) > 0 {
		tools := make([]map[string]any, 0, len(m.tools))
		for _, t := range m.tools {
			parameters, err := t.ParamsOneOf.ToJSONSchema()
			if err != nil {
				return nil, err
			}
			tools = append(tools, map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Desc, "parameters": parameters}})
		}
		request["tools"] = tools
		request["parallel_tool_calls"] = false
	}
	return json.Marshal(request)
}
func (m *readingModel) post(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(m.cfg.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, &agentrun.Error{Code: agentrun.CodeModelUnavailable}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+string(m.cfg.APIKey.Bytes()))
	resp, err := m.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, &agentrun.Error{Code: agentrun.CodeTimeout}
		}
		return nil, &agentrun.Error{Code: agentrun.CodeModelUnavailable}
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		code := agentrun.CodeModelUnavailable
		if resp.StatusCode == 400 || resp.StatusCode == 404 || resp.StatusCode == 422 {
			code = agentrun.CodeModelIncompatible
		}
		return nil, &agentrun.Error{Code: code}
	}
	return resp, nil
}

type readingUsage struct {
	Input  *int64 `json:"prompt_tokens"`
	Output *int64 `json:"completion_tokens"`
	Total  *int64 `json:"total_tokens"`
}
type readingResponse struct {
	Choices []struct {
		Index        int             `json:"index"`
		Message      *schema.Message `json:"message"`
		Delta        *schema.Message `json:"delta"`
		FinishReason string          `json:"finish_reason"`
	} `json:"choices"`
	Usage *readingUsage   `json:"usage"`
	Error json.RawMessage `json:"error"`
}

func attachReadingUsage(message *schema.Message, u *readingUsage) {
	if u != nil {
		message.Extra = map[string]any{"velis_usage": &agentrun.ModelUsage{Input: u.Input, Output: u.Output, Total: u.Total}}
	}
}
func readingMessageUsage(message *schema.Message) *agentrun.ModelUsage {
	if message == nil {
		return nil
	}
	if u, ok := message.Extra["velis_usage"].(*agentrun.ModelUsage); ok {
		return u
	}
	return nil
}

func (m *readingModel) Generate(ctx context.Context, messages []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	body, err := m.requestBody(messages, false)
	if err != nil {
		return nil, err
	}
	resp, err := m.post(ctx, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > 512*1024 || !strictjson.ValidDocument(raw) {
		return nil, &agentrun.Error{Code: agentrun.CodeOutputInvalid}
	}
	var result readingResponse
	if json.Unmarshal(raw, &result) != nil || len(result.Error) > 0 || len(result.Choices) != 1 || result.Choices[0].Index != 0 || result.Choices[0].Message == nil {
		return nil, &agentrun.Error{Code: agentrun.CodeModelIncompatible}
	}
	message := result.Choices[0].Message
	message.ResponseMeta = &schema.ResponseMeta{FinishReason: result.Choices[0].FinishReason}
	attachReadingUsage(message, result.Usage)
	return message, nil
}

func (m *readingModel) Stream(ctx context.Context, messages []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	body, err := m.requestBody(messages, true)
	if err != nil {
		return nil, err
	}
	resp, err := m.post(ctx, body)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		resp.Body.Close()
		return nil, &agentrun.Error{Code: agentrun.CodeModelIncompatible}
	}
	reader, writer := schema.Pipe[*schema.Message](1)
	go func() {
		defer resp.Body.Close()
		defer writer.Close()
		buffer := bufio.NewReaderSize(resp.Body, 128*1024)
		total := 0
		for {
			line, err := buffer.ReadSlice('\n')
			total += len(line)
			if errors.Is(err, bufio.ErrBufferFull) || total > 2*1024*1024 {
				writer.Send(nil, &agentrun.Error{Code: agentrun.CodeOutputInvalid})
				return
			}
			if err != nil {
				if ctx.Err() != nil {
					writer.Send(nil, ctx.Err())
				} else {
					writer.Send(nil, &agentrun.Error{Code: agentrun.CodeOutputInvalid})
				}
				return
			}
			if !bytes.HasPrefix(line, []byte("data:")) {
				continue
			}
			raw := bytes.TrimSpace(line[5:])
			if bytes.Equal(raw, []byte("[DONE]")) {
				return
			}
			if !strictjson.ValidDocument(raw) {
				writer.Send(nil, &agentrun.Error{Code: agentrun.CodeOutputInvalid})
				return
			}
			var result readingResponse
			if json.Unmarshal(raw, &result) != nil || len(result.Error) > 0 || len(result.Choices) > 1 {
				writer.Send(nil, &agentrun.Error{Code: agentrun.CodeModelIncompatible})
				return
			}
			message := &schema.Message{Role: schema.Assistant}
			if len(result.Choices) == 1 {
				choice := result.Choices[0]
				if choice.Index != 0 || choice.Delta == nil {
					writer.Send(nil, &agentrun.Error{Code: agentrun.CodeModelIncompatible})
					return
				}
				message = choice.Delta
				message.ResponseMeta = &schema.ResponseMeta{FinishReason: choice.FinishReason}
			}
			attachReadingUsage(message, result.Usage)
			if writer.Send(message, nil) {
				return
			}
		}
	}()
	return reader, nil
}
