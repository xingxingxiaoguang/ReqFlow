// Package tools 提供需求分析 agent 的过程工具（pi 工具模式：读取/检索/写入/问人）。
//
// 设计约定：
//   - 工具围绕「读文档 → 检索定位 → 分批产出草稿 → 必要时问人」的分析过程；
//     文档原文不进首轮消息，agent 经工具自主阅读
//   - 不直接写任何持久存储：草稿进内存 DraftSink，落库仍由人工确认后的任务步骤
//     执行——AI 是草稿机不是审批者
//   - Output 返回模型友好的纯文本/紧凑 JSON（上下文预算优先）；Details 返回人读摘要
//     （前端工具轨迹展示用）——pi 的 output/details 拆分
//   - 每个工具自带 PromptSnippet/PromptGuidelines（agent.DocumentedTool），系统提示词
//     从实际注入的工具集组装——工具增删，提示词自动跟随，不漂移
//   - 工具按运行构造：文档文本、草稿 sink、人工交互桥都是运行期状态
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reqflow/internal/app/agent"
	"reqflow/internal/port"
)

// DocSource 本次分析的目标文档。
type DocSource struct {
	FileName string
	Text     string
}

// LineCount 文档总行数（与 read/search 的行号口径一致）。
func (d DocSource) LineCount() int { return len(splitLines(d.Text)) }

// RuneCount 文档总字符数（rune 计，与 read 的截断口径一致）。
func (d DocSource) RuneCount() int { return len([]rune(d.Text)) }

// HumanAsker 人工交互桥（app.DialogHub 实现：SSE 推问 + 阻塞等待 HTTP 应答）。
// Ask 在人工回答前阻塞；ctx 取消（任务暂停）时返回 error。
type HumanAsker interface {
	Ask(ctx context.Context, taskID, callID, question string, options []string) (string, error)
}

// RunDeps 按运行构造工具集的依赖。
type RunDeps struct {
	Doc    DocSource
	Sink   *DraftSink
	TaskID string
	Ask    HumanAsker // nil 时 ask_human 返回不可用回执（模型按默认规则继续）
}

// BuildForRun 构造分析过程工具集（顺序即注入 Context.Tools 的顺序）。
func BuildForRun(d RunDeps) []agent.Tool {
	return []agent.Tool{
		&readDocumentTool{doc: d.Doc},
		&searchDocumentTool{doc: d.Doc},
		&writeWorkItemsTool{sink: d.Sink},
		&askHumanTool{taskID: d.TaskID, ask: d.Ask},
	}
}

/* ---- 公共辅助 ---- */

func decodeArgs(call port.ToolCall, dst any) error {
	if len(call.Arguments) == 0 {
		return nil
	}
	return json.Unmarshal(call.Arguments, dst)
}

func errOutput(format string, a ...any) agent.ToolOutput {
	return agent.ToolOutput{Output: fmt.Sprintf(format, a...), IsError: true}
}

// compactJSON 序列化为无缩进 JSON（结构化回执用，控制上下文膨胀）。
func compactJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// splitLines 统一行切分（\r\n/\r 归一为 \n，去尾部空行）。read/search 共享，
// 保证两边行号口径严格一致——search 命中行号可直接用于 read 的 offset。
func splitLines(text string) []string {
	normalized := strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(normalized, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}
