package app

import (
	"fmt"
	"strings"
	"time"

	"reqflow/internal/app/agent"
	"reqflow/internal/app/tools"
)

// 需求分析 Prompt。结构拆分为四段：
//   - 共享指令头（角色 + 任务 + 草稿字段规范 + 分析要点）：单发/agent 两模式共用，
//     措辞模式中立（不预设输出形态）——单发的 JSON 契约在输出格式段、agent 的
//     产出通道在工具指南，各自声明，避免「要求输出 JSON 数组」与「经工具提交」
//     自相矛盾导致模型跳过工具直接单轮作答
//   - 输出格式段（JSON 数组契约）：仅单发直调使用
//   - 额外要求段：用户补充，空则整体不出现
//   - 文档节：单发模式拼进 user 消息；agent 模式替换为文档清单（原文经工具阅读）
//
// agent 模式的工具使用指南不在此维护——renderAgentSystem 从实际注入的工具集
// 组装（agent.DocumentedTool），工具增删提示词自动跟随，不会漂移。
// 占位符以字面量 {var} 形式注入（避免与 JSON 大括号冲突的模板引擎）。

const analyzePromptHead = `你是一位专业的项目管理助手和技术顾问，擅长分析需求文档并提取结构化的工作项信息。

## 任务
分析以下需求文档，识别其中的所有工作项（需求、任务、功能点等），并按项目分组整理。

## 草稿字段规范
每个工作项草稿包含以下字段：

### 必填字段
- **project_name** (string): 该工作项所属的项目名称
  - 从文档中识别项目名称；未明确时根据需求内容推断合理名称；多项目需求需正确区分
- **title** (string): 工作项的简洁标题，10-50 字，动宾结构，如"实现用户登录功能"
- **description** (string): 详细描述，包含需求背景、实现细节、验收标准，保留原文关键信息
- **priority** (string): 优先级，必须为 "High"（核心/紧急/阻塞）、"Medium"（常规）、"Low"（非核心）之一，默认 "Medium"
- **estimated_hours** (number): 预估工时（小时）
  - 简单任务 1-4；中等任务（单模块/API）8-16；复杂任务（核心功能/架构）24-40；大型任务 40+
- **start_at** (string): 计划开始时间，ISO 8601 格式，默认使用当前时间: "{current_time}"
- **assignee_name** (string | null): 负责人姓名；文档明确指派时提取，否则返回 null
- **state** (string | null): 状态名称。仅当文档**明确标注**状态时提取（如"待办/进行中/已完成"）；未提及必须返回 null，不要猜测，不要输出状态 ID
- **solution_suggestion** (string): 解决方案建议，具体可执行、含技术细节；按类型给出：story/feature 给开发建议与技术选型，task 给实现步骤，bug 给排查思路与修复建议，epic 给拆分建议与风险点

### 选填字段
- **type_id** (string): 工作项类型，必须为 "story"（用户故事）/"task"（任务）/"bug"（缺陷）/"feature"（特性）/"epic"（史诗）之一，默认 "story"

## 分析要点
1. 项目识别：仔细区分多项目需求，避免混淆
2. 需求拆分：大需求拆为独立可交付的合理粒度
3. 优先级：综合重要性、紧急程度、依赖关系判断
4. 完整性：提取所有明确的需求点，不遗漏
5. 负责人识别：注意"由XXX负责"等表述
6. 状态识别：仅提取文档明确写出的状态
7. 解决方案：专业、具体、可执行`

// analyzeOutputFormat 单发直调的输出契约（agent 模式不使用——产出走 write_work_items）。
const analyzeOutputFormat = `## 输出格式
只输出 JSON 数组，不要包含任何其他文字、解释或 markdown 代码块标记。

示例输出：
[
  {
    "project_name": "用户中心",
    "title": "实现用户注册功能",
    "description": "支持邮箱和手机号注册，包含验证码验证、密码强度校验、用户协议确认等功能。",
    "priority": "High",
    "estimated_hours": 16,
    "start_at": "{current_time}",
    "type_id": "story",
    "assignee_name": null,
    "state": null,
    "solution_suggestion": "1. 设计注册接口\n2. bcrypt 加密密码\n3. 集成邮件/短信验证码\n4. 前端表单与验证\n5. 防重复提交"
  }
]`

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

// renderPromptHead 渲染共享指令头（填充时间占位符）。
func renderPromptHead(now time.Time) string {
	return strings.ReplaceAll(analyzePromptHead, "{current_time}", now.Format(time.RFC3339))
}

// renderAnalyzePrompt 单发模式：指令头 + 输出格式 + 额外要求 + 文档节拼为完整 prompt
// （一条 user 消息；与拆分前的输出逐字等价）。
func renderAnalyzePrompt(text string, now time.Time, special string) string {
	return strings.Join([]string{
		renderPromptHead(now),
		strings.ReplaceAll(analyzeOutputFormat, "{current_time}", now.Format(time.RFC3339)),
		buildSpecialSection(special),
		strings.ReplaceAll(analyzeDocSection, "{text}", text),
	}, "\n\n")
}

// renderAgentSystem agent 模式 SystemPrompt：共享指令头 + 额外要求 + 工具使用指南。
// 指南从实际注入的工具集组装（agent.DocumentedTool 的 snippet/guidelines）——
// 工具增删提示词自动跟随，杜绝引用已下线工具的漂移。
func renderAgentSystem(now time.Time, special string, toolset []agent.Tool) string {
	var sb strings.Builder
	sb.WriteString(renderPromptHead(now))
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
