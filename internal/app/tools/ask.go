package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reqflow/internal/app/agent"
	"reqflow/internal/port"
)

// askHumanTool 关键决策点/卡点向人工提问（阻塞等待 HTTP 应答）。
type askHumanTool struct {
	taskID string
	ask    HumanAsker
}

func (t *askHumanTool) Spec() port.ToolSpec {
	return port.ToolSpec{
		Name:        "ask_human",
		Description: "向人工提问以获取关键决策信息或解除卡点（提问会弹窗展示并阻塞等待回答）。仅在文档无法解决时使用：关键歧义、缺失的决策信息、需求间冲突。能按文档内容和默认规则推断的不要提问。",
		Parameters: json.RawMessage(`{"type":"object","properties":{` +
			`"question":{"type":"string","description":"要问的问题（完整、具体，含必要的上下文）"},` +
			`"options":{"type":"array","items":{"type":"string"},"description":"候选答案（可选）。提供时人工单选作答，适用于明确的决策点；不提供则自由文本回答"}` +
			`,"required":["question"]}}`),
	}
}

func (t *askHumanTool) Execute(ctx context.Context, call port.ToolCall, _ func(string)) agent.ToolOutput {
	var args struct {
		Question string   `json:"question"`
		Options  []string `json:"options"`
	}
	if err := decodeArgs(call, &args); err != nil {
		return errOutput("参数解析失败: %v", err)
	}
	question := strings.TrimSpace(args.Question)
	if question == "" {
		return errOutput("question 不能为空")
	}
	if t.ask == nil {
		return errOutput("人工交互通道不可用，请按文档信息与默认规则继续")
	}
	answer, err := t.ask.Ask(ctx, t.taskID, call.ID, question, args.Options)
	if err != nil {
		if ctx.Err() != nil {
			return errOutput("人工对话因任务暂停中断，恢复后可重新提问")
		}
		return errOutput("人工对话失败: %v", err)
	}
	answer = strings.TrimSpace(answer)
	if answer == "" {
		return errOutput("人工未作答（空回答）。请按文档信息与默认规则继续，或换一个更具体的问题")
	}
	return agent.ToolOutput{
		Output:  "人工回答：" + answer,
		Details: fmt.Sprintf("ask_human：%s → %s", truncateRunes(question, 30), truncateRunes(answer, 30)),
	}
}

func (t *askHumanTool) PromptSnippet() string {
	return "ask_human：向人工提问（关键决策点或卡点，弹窗等待回答）"
}

func (t *askHumanTool) PromptGuidelines() []string {
	return []string{
		"仅在文档无法解决的歧义、关键决策信息缺失或需求冲突时使用；能按文档与默认规则推断的不要问",
		"问题要完整具体（含必要的上下文与候选项）；options 提供候选答案时人工将单选作答",
	}
}
