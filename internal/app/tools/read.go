package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reqflow/internal/app/agent"
	"reqflow/internal/port"
)

// 读取限制（与 Spec 描述同源——描述里的数字来自这些常量，不会漂移）。
const (
	readDefaultLines = 200 // limit 默认值（行）
	readMaxLines     = 500 // limit 上限（行）
	readMaxRunes     = 6000
)

// readDocumentTool 分批读取待分析文档（pi read 模式：行号分页 + 行动性续读提示）。
type readDocumentTool struct{ doc DocSource }

func (t *readDocumentTool) Spec() port.ToolSpec {
	return port.ToolSpec{
		Name:        "read_document",
		Description: fmt.Sprintf("分批读取待分析文档的内容，按行分页，输出每行前缀行号（与 search_document 的行号一致）。单次最多 %d 行且不超过 %d 字（先到者截断）。需要全文档时从 offset=1 开始持续用 offset 续读，直到不再出现续读提示（即已读完）。", readDefaultLines, readMaxRunes),
		Parameters: json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{`+
			`"offset":{"type":"integer","description":"起始行号（1 起，默认 1）"},`+
			`"limit":{"type":"integer","description":"本次读取行数（默认 %d，上限 %d）"}}}`,
			readDefaultLines, readMaxLines)),
	}
}

func (t *readDocumentTool) Execute(_ context.Context, call port.ToolCall, _ func(string)) agent.ToolOutput {
	var args struct {
		Offset int `json:"offset"`
		Limit  int `json:"limit"`
	}
	if err := decodeArgs(call, &args); err != nil {
		return errOutput("参数解析失败: %v", err)
	}
	lines := splitLines(t.doc.Text)
	total := len(lines)
	if args.Offset <= 0 {
		args.Offset = 1
	}
	if args.Limit <= 0 {
		args.Limit = readDefaultLines
	}
	if args.Limit > readMaxLines {
		args.Limit = readMaxLines
	}
	if args.Offset > total {
		return errOutput("offset %d 超出文档长度（共 %d 行），请从 offset=1 开始读取", args.Offset, total)
	}

	start := args.Offset - 1
	end := start // 实际读完的行（0-indexed，开区间）
	used := 0    // 本批已输出的字符数（rune 计）
	var sb strings.Builder
	for end < total && end-start < args.Limit && used < readMaxRunes {
		runes := []rune(lines[end])
		if used+len(runes) > readMaxRunes {
			if end == start {
				// 单行即超字符上限：按 rune 边界硬拆。行内剩余部分无法按行翻页读取
				// （无 shell 兜底），提示改用检索定位——pi firstLineExceedsLimit 的等价物。
				budget := readMaxRunes - used
				fmt.Fprintf(&sb, "%d:%s…\n", end+1, string(runes[:budget]))
				fmt.Fprintf(&sb, "[第 %d 行超长已截断（共 %d 字，未展示 %d 字）。行内剩余部分无法按行翻页读取，如需请用 search_document 检索关键词]\n",
					end+1, len(runes), len(runes)-budget)
				end++
			}
			// 非首行：预算耗尽，本行留给续读
			break
		}
		fmt.Fprintf(&sb, "%d:%s\n", end+1, lines[end])
		used += len(runes)
		end++
	}
	if end < total {
		fmt.Fprintf(&sb, "\n[已显示第 %d-%d 行，共 %d 行。用 offset=%d 继续读取]", args.Offset, end, total, end+1)
	}
	return agent.ToolOutput{
		Output:  sb.String(),
		Details: fmt.Sprintf("read_document：第 %d-%d 行 / 共 %d 行", args.Offset, end, total),
	}
}

func (t *readDocumentTool) PromptSnippet() string {
	return "read_document：分批读取文档内容（行号分页，输出带行号）"
}

func (t *readDocumentTool) PromptGuidelines() []string {
	return []string{
		"通读全文是产出草稿的前提：从 offset=1 开始持续续读直到文档结束（无续读提示即已读完），不要跳读",
		"单行超长被截断时，行内剩余部分用 search_document 检索关键词定位",
	}
}
