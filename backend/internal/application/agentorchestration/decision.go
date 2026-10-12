package agentorchestration

import (
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentevidence"
	"github.com/ghost-blade-pc/Velis_Feed/backend/internal/domain/agentrun"
)

type ClarificationReason string

const (
	MissingTask           ClarificationReason = "missing_task"
	AmbiguousReference    ClarificationReason = "ambiguous_reference"
	UnsupportedCapability ClarificationReason = "unsupported_capability"
	UnsupportedFilter     ClarificationReason = "unsupported_filter"
	InsufficientEvidence  ClarificationReason = "insufficient_evidence"
)

// DecisionFacts 由可信工具调用及上下文策略生成，不能从模型的自述直接构造。
type DecisionFacts struct {
	SuccessfulRetrievals, UsableResults int
	RequiredFailure                     agentrun.Code
	Clarification                       ClarificationReason
}

func Decide(f DecisionFacts) (agentevidence.AnswerKind, error) {
	if f.RequiredFailure != "" {
		if !f.RequiredFailure.Valid() {
			return "", &agentrun.Error{Code: agentrun.CodeToolFailed}
		}
		return "", &agentrun.Error{Code: f.RequiredFailure}
	}
	if f.Clarification != "" {
		if _, ok := clarifications[f.Clarification]; !ok {
			return "", agentevidence.ErrOutput
		}
		return agentevidence.Clarification, nil
	}
	if f.UsableResults > 0 {
		return agentevidence.AnswerArticle, nil
	}
	if f.SuccessfulRetrievals > 0 && f.UsableResults == 0 {
		return agentevidence.NoResults, nil
	}
	return "", agentevidence.ErrOutput
}

var clarifications = map[ClarificationReason]string{
	MissingTask:           "请说明想查找的主题、需要推荐的方向，或要解释的文章。",
	AmbiguousReference:    "暂时无法唯一确定你指的是哪篇文章，请提供文章或说明它在上一轮来源中的编号。",
	UnsupportedCapability: "目前支持站内文章查找、推荐和阅读解释。保存笔记、外部检索、写作产物及长期偏好记忆尚未启用，请说明需要阅读的站内内容。",
	UnsupportedFilter:     "当前支持关键词、主题和来源条件，尚不支持时间等筛选。请调整筛选条件。",
	InsufficientEvidence:  "现有材料不足以支持所需的正文细节，请明确要阅读的文章或调整问题。",
}

func Template(f DecisionFacts) (agentevidence.Answer, error) {
	kind, err := Decide(f)
	if err != nil {
		return agentevidence.Answer{}, err
	}
	text := ""
	switch kind {
	case agentevidence.NoResults:
		text = "本轮检索或推荐已完成，没有找到可用的站内文章。可以调整关键词、主题或来源后再试。"
	case agentevidence.Clarification:
		text = clarifications[f.Clarification]
	default:
		return agentevidence.Answer{}, agentevidence.ErrOutput
	}
	return agentevidence.Answer{Version: 1, Kind: kind, Blocks: []agentevidence.Block{{ID: "notice", Text: text, EvidenceIDs: []string{}}}, References: []agentevidence.Reference{}, Cards: []agentevidence.Card{}}, nil
}
