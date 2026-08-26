package app

import (
	"fmt"
	"strings"
	"time"

	"reqflow/internal/app/agent"
	"reqflow/internal/app/tools"
	"reqflow/internal/domain/model"
)

// 分析 Prompt 全部动态装配，无固定模板（提示词与 schema/工具同源，不漂移）：
//   - 指令头：profile.Role 的 {field_spec} 占位由产出 schema 渲染（字段增删改
//     自动跟随），{current_time} 渲染时填充
//   - 单发降级的输出契约：通用契约文本 + 示例（profile.Example 覆盖保质量，
//     缺省按 schema 生成骨架——新任务类型零配置可用）
//   - agent 工具指南：从实际注入的工具集组装（agent.DocumentedTool）
//   - 额外要求段：用户补充，空则整体不出现
//   - 文档节：单发模式拼进 user 消息；agent 模式替换为文档清单（原文经工具阅读）
//
// 占位符以字面量 {var} 形式注入（避免与 JSON 大括号冲突的模板引擎）。

// renderAnalyzeHead 渲染共享指令头（单发/agent 两模式共用）：
// Role 的 {field_spec} 替换为 schema 渲染段，{current_time} 填充。
func renderAnalyzeHead(now time.Time, profile AnalyzeProfile) string {
	head := strings.ReplaceAll(profile.Role, "{field_spec}", renderFieldSpecSection(profile.Schema()))
	return strings.ReplaceAll(head, "{current_time}", now.Format(time.RFC3339))
}

// renderFieldSpecSection 从产出 schema 渲染「草稿字段规范」段。
// 类型/枚举值域/必填由 schema 自动标注，提取说明来自 FieldSpec.Prompt。
func renderFieldSpecSection(schema model.DatasetSchema) string {
	var sb strings.Builder
	sb.WriteString("## 草稿字段规范\n")
	sb.WriteString("每个工作项草稿包含以下字段（写入前按此校验）：\n")
	for _, f := range schema.Fields {
		fmt.Fprintf(&sb, "- **%s** (%s): %s\n", f.Key, promptTypeName(f), f.Prompt)
	}
	return sb.String()
}

// promptTypeName 字段类型 → 提示词类型标注（枚举自动附值域，必填自动标注）。
func promptTypeName(f model.FieldSpec) string {
	var typ string
	switch f.Type {
	case model.FieldString, model.FieldText:
		typ = "string"
	case model.FieldNumber:
		typ = "number"
	case model.FieldDate:
		typ = "string, ISO 8601 格式"
	case model.FieldEnum:
		quoted := make([]string, len(f.Enum))
		for i, v := range f.Enum {
			quoted[i] = fmt.Sprintf("%q", v)
		}
		typ = fmt.Sprintf("string，必须为 %s 之一", strings.Join(quoted, "/"))
	default:
		typ = string(f.Type)
	}
	if f.Required {
		typ += "，必填"
	}
	return typ
}

// renderClassicOutputFormat 单发直调的输出契约（agent 模式不使用——产出走写入工具）。
func renderClassicOutputFormat(now time.Time, profile AnalyzeProfile) string {
	example := profile.Example
	if strings.TrimSpace(example) == "" {
		example = renderExampleSkeleton(profile.Schema())
	}
	out := "## 输出格式\n只输出 JSON 数组，不要包含任何其他文字、解释或 markdown 代码块标记。\n\n示例输出：\n" + example
	return strings.ReplaceAll(out, "{current_time}", now.Format(time.RFC3339))
}

// renderExampleSkeleton 按 schema 生成示例骨架：必填字段给类型占位值，选填省略
// （零配置兜底；对质量有要求的任务类型在 profile.Example 提供富示例）。
func renderExampleSkeleton(schema model.DatasetSchema) string {
	var parts []string
	for _, f := range schema.Fields {
		if !f.Required {
			continue
		}
		switch f.Type {
		case model.FieldNumber:
			parts = append(parts, fmt.Sprintf("  \"%s\": 1", f.Key))
		case model.FieldEnum:
			v := ""
			if len(f.Enum) > 0 {
				v = f.Enum[0]
			}
			parts = append(parts, fmt.Sprintf("  \"%s\": %q", f.Key, v))
		case model.FieldDate:
			parts = append(parts, fmt.Sprintf("  \"%s\": \"{current_time}\"", f.Key))
		default:
			parts = append(parts, fmt.Sprintf("  \"%s\": \"（%s）\"", f.Key, f.Label))
		}
	}
	return "[\n  {\n" + strings.Join(parts, ",\n") + "\n  }\n]"
}

const analyzeDocSection = `---

## 需求文档内容

{text}

---`

// buildSpecialSection 组装「## 额外要求」章节；用户未填写时整体不出现。
func buildSpecialSection(special string) string {
	if special == "" {
		return ""
	}
	return "## 额外要求\n以下为用户针对本次分析补充的要求，优先级高于前述默认约定，需严格遵循：\n" + special
}

// renderAnalyzePrompt 单发模式：指令头 + 输出格式 + 额外要求 + 文档节拼为完整 prompt
// （一条 user 消息）。
func renderAnalyzePrompt(text string, now time.Time, special string, profile AnalyzeProfile) string {
	return strings.Join([]string{
		renderAnalyzeHead(now, profile),
		renderClassicOutputFormat(now, profile),
		buildSpecialSection(special),
		strings.ReplaceAll(analyzeDocSection, "{text}", text),
	}, "\n\n")
}

// renderAgentSystem agent 模式 SystemPrompt：指令头 + 额外要求 + 工具使用指南。
// 指南从实际注入的工具集组装（agent.DocumentedTool 的 snippet/guidelines）——
// 工具增删提示词自动跟随，杜绝引用已下线工具的漂移。
func renderAgentSystem(now time.Time, special string, toolset []agent.Tool, profile AnalyzeProfile) string {
	var sb strings.Builder
	sb.WriteString(renderAnalyzeHead(now, profile))
	if sp := buildSpecialSection(special); sp != "" {
		sb.WriteString("\n\n")
		sb.WriteString(sp)
	}
	sb.WriteString("\n\n## 工具使用指南\n")
	sb.WriteString("文档原文不直接提供（见首条消息的文档清单），你通过工具自主阅读并产出草稿：\n")
	for _, t := range toolset {
		if dt, ok := t.(agent.DocumentedTool); ok {
			sb.WriteString("- ")
			sb.WriteString(dt.PromptSnippet())
			sb.WriteString("\n")
		}
	}
	var guidelines []string
	for _, t := range toolset {
		if dt, ok := t.(agent.DocumentedTool); ok {
			guidelines = append(guidelines, dt.PromptGuidelines()...)
		}
	}
	if len(guidelines) > 0 {
		sb.WriteString("\n规则：\n")
		for i, g := range guidelines {
			fmt.Fprintf(&sb, "%d. %s\n", i+1, g)
		}
	}
	return sb.String()
}

// renderDocManifest agent 模式首轮 user 消息：文档清单（原文不进上下文，经工具阅读）。
// 带首步行动指引——模型最容易犯的错是不调工具直接凭空作答，首条消息明确第一步动作。
// 其余工作方式由 SystemPrompt 的工具指南约束（同源不漂移）。
func renderDocManifest(doc tools.DocSource) string {
	return fmt.Sprintf(`## 待分析文档

- 文件名：%s
- 规模：%d 行 / %d 字

文档原文不在本消息中，你必须先调用 read_document（offset=1）开始阅读，再按系统提示
的工具使用指南完成分析——不要在没有阅读原文的情况下直接给出结果。`, doc.FileName, doc.LineCount(), doc.RuneCount())
}
