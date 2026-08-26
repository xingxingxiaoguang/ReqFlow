package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"reqflow/internal/app/agent"
	"reqflow/internal/port"
)

// 检索限制（与 Spec 描述同源）。
const (
	searchDefaultLimit = 20  // 命中数默认上限
	searchMaxLimit     = 50  // 命中数上限
	searchMaxContext   = 10  // 上下文行数上限
	searchLineMaxRunes = 500 // 命中/上下文行的展示截断（rune）
)

// searchDocumentTool 正则检索文档（pi grep 模式：纯文本 path:line 输出 + 可行动截断提示）。
type searchDocumentTool struct{ doc DocSource }

func (t *searchDocumentTool) Spec() port.ToolSpec {
	return port.ToolSpec{
		Name:        "search_document",
		Description: fmt.Sprintf("在待分析文档中检索：正则或字面量匹配，返回带行号的命中行（行号与 read_document 一致，可直接用于其 offset）。命中行超长截断到 %d 字，命中数上限默认 %d 条。用 context 参数携带命中行前后若干行。", searchLineMaxRunes, searchDefaultLimit),
		Parameters: json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{`+
			`"pattern":{"type":"string","description":"检索模式（正则）"},`+
			`"literal":{"type":"boolean","description":"按字面量匹配（转义正则元字符），检索 3.1.2、C++ 这类字符串时使用"},`+
			`"ignore_case":{"type":"boolean","description":"忽略大小写"},`+
			`"context":{"type":"integer","description":"每个命中行前后附带的上/下文行数（默认 0，上限 %d）"},`+
			`"limit":{"type":"integer","description":"返回命中数上限（默认 %d，上限 %d）"},`+
			`"required":["pattern"]}}`,
			searchMaxContext, searchDefaultLimit, searchMaxLimit)),
	}
}

func (t *searchDocumentTool) Execute(_ context.Context, call port.ToolCall, _ func(string)) agent.ToolOutput {
	var args struct {
		Pattern    string `json:"pattern"`
		Literal    bool   `json:"literal"`
		IgnoreCase bool   `json:"ignore_case"`
		Context    int    `json:"context"`
		Limit      int    `json:"limit"`
	}
	if err := decodeArgs(call, &args); err != nil {
		return errOutput("参数解析失败: %v", err)
	}
	pattern := strings.TrimSpace(args.Pattern)
	if pattern == "" {
		return errOutput("pattern 不能为空")
	}
	if args.Context < 0 {
		args.Context = 0
	}
	if args.Context > searchMaxContext {
		args.Context = searchMaxContext
	}
	if args.Limit <= 0 {
		args.Limit = searchDefaultLimit
	}
	if args.Limit > searchMaxLimit {
		args.Limit = searchMaxLimit
	}

	expr := pattern
	if args.Literal {
		expr = regexp.QuoteMeta(expr)
	}
	if args.IgnoreCase {
		expr = "(?i)" + expr
	}
	re, err := regexp.Compile(expr)
	if err != nil {
		return errOutput("正则表达式无效: %v（如需按字面量检索请设 literal=true）", err)
	}

	lines := splitLines(t.doc.Text)
	var matched []int // 0-indexed 行号
	limitReached := false
	for i, line := range lines {
		if re.MatchString(line) {
			if len(matched) >= args.Limit {
				limitReached = true
				break
			}
			matched = append(matched, i)
		}
	}
	if len(matched) == 0 {
		return agent.ToolOutput{Output: "无命中", Details: fmt.Sprintf("search_document(%s)：无命中", truncateRunes(pattern, 30))}
	}

	// grep 格式输出：命中行「N:内容」、上下文行「N-内容」（组间空行）
	var blocks []string
	lineTruncated := false
	for _, idx := range matched {
		var block []string
		lo := idx - args.Context
		if lo < 0 {
			lo = 0
		}
		hi := idx + args.Context
		if hi > len(lines)-1 {
			hi = len(lines) - 1
		}
		for i := lo; i <= hi; i++ {
			text, truncated := truncateLineRunes(lines[i])
			if truncated {
				lineTruncated = true
			}
			if i == idx {
				block = append(block, fmt.Sprintf("%d:%s", i+1, text))
			} else {
				block = append(block, fmt.Sprintf("%d-%s", i+1, text))
			}
		}
		blocks = append(blocks, strings.Join(block, "\n"))
	}
	out := strings.Join(blocks, "\n\n")

	// 可行动截断提示（pi grep 的 notices 模式）
	var notices []string
	if limitReached {
		notices = append(notices, fmt.Sprintf("已达 %d 条命中上限。用 limit=%d 获取更多，或收窄 pattern", args.Limit, args.Limit*2))
	}
	if lineTruncated {
		notices = append(notices, fmt.Sprintf("部分行已截断到 %d 字。用 read_document 按行号查看完整行", searchLineMaxRunes))
	}
	if len(notices) > 0 {
		out += "\n\n[" + strings.Join(notices, "；") + "]"
	}

	// Details：命中行号摘要（人读）
	var nums []string
	for _, idx := range matched {
		if len(nums) >= 5 {
			nums = append(nums, "…")
			break
		}
		nums = append(nums, fmt.Sprintf("%d", idx+1))
	}
	return agent.ToolOutput{
		Output:  out,
		Details: fmt.Sprintf("search_document(%s)：命中 %d 处（第 %s 行）", truncateRunes(pattern, 30), len(matched), strings.Join(nums, "、")),
	}
}

func (t *searchDocumentTool) PromptSnippet() string {
	return "search_document：正则/字面量检索文档，返回带行号的命中行（与 read_document 行号一致）"
}

func (t *searchDocumentTool) PromptGuidelines() []string {
	return []string{
		"优先用于定位关键信息（表格、日期、负责人、编号、版本号等），再用 read_document 按命中行号精读上下文",
		"检索含正则元字符的字面量（如 3.1.2、C++）时设 literal=true",
	}
}

// truncateLineRunes 单行展示截断（pi truncateLine 模式：截断处显式标注）。
func truncateLineRunes(line string) (string, bool) {
	r := []rune(line)
	if len(r) <= searchLineMaxRunes {
		return line, false
	}
	return string(r[:searchLineMaxRunes]) + "… [截断]", true
}
