package analysis

import (
	"context"
	"encoding/json"
	"fmt"

	"reqflow/internal/app/agent"
	"reqflow/internal/domain/logic"
	"reqflow/internal/port"
)

type resultState struct {
	Submitted bool            `json:"submitted"`
	Output    json.RawMessage `json:"output,omitempty"`
}

// submitResultTool 把节点冻结的 OutputContract Schema 变成唯一完成出口。Schema 或字段
// 错误是可恢复工具错误，会回到 Agent 上下文供下一轮自行修正。
type submitResultTool struct {
	schema json.RawMessage
	state  *resultState
}

func (t *submitResultTool) Spec() port.ToolSpec {
	return port.ToolSpec{Name: "submit_analysis_result",
		Description: "校验并提交最终结构化分析结果；仅校验通过时结束运行",
		Parameters:  t.schema}
}

func (*submitResultTool) PromptSnippet() string {
	return "submit_analysis_result：按 OutputContract Schema 校验并提交最终结果（唯一完成出口）"
}

func (*submitResultTool) PromptGuidelines() []string {
	return []string{
		"完成知识检索和事实核对后调用 submit_analysis_result，不得用普通文本宣告完成。",
		"若工具返回 Schema 错误，按错误信息修正字段、类型和值后重新提交。",
	}
}

func (t *submitResultTool) Execute(_ context.Context, call port.ToolCall, _ func(string)) agent.ToolOutput {
	normalized, err := logic.NormalizeDatasetItem(t.schema, call.Arguments)
	if err != nil {
		payload, _ := json.Marshal(map[string]any{"ok": false, "error": map[string]any{
			"code": "ANALYSIS_SCHEMA_INVALID", "recoverable": true,
			"message": err.Error(), "hint": "按 OutputContract 的输出 Schema 修正参数后重新调用 submit_analysis_result",
		}})
		return agent.ToolOutput{Output: string(payload), Details: "分析结果未通过 Schema 校验", IsError: true}
	}
	t.state.Submitted = true
	t.state.Output = normalized
	return agent.ToolOutput{Output: `{"ok":true,"submitted":true}`,
		Details: fmt.Sprintf("结构化分析结果已提交（%d bytes）", len(normalized)), Terminate: true}
}

var _ agent.DocumentedTool = (*submitResultTool)(nil)
