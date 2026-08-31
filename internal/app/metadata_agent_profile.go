package app

import (
	"fmt"

	"reqflow/internal/app/tools"
	"reqflow/internal/domain/model"
)

// MetadataAgentProfile 是旧元数据目录使用的提示词预览装配描述；它不参与 V2
// knowledge.analyze 执行，真正的不可变分析合同是 model.AnalysisProfile。
//
// 系统提示词由此动态装配（无固定模板）：
//
//	profile.Role（{field_spec} 占位 → 产出 schema 渲染）+ 额外要求（用户输入）
//	+ 工具指南（agent.DocumentedTool 同源组装）
//
// 该结构仅支撑旧元数据目录的声明和预览，不承载任何 LLM 执行或恢复状态。
type MetadataAgentProfile struct {
	// Role 指令头：角色 + 任务 + 分析要点。模式中立（不预设输出形态）；
	// {field_spec} 占位符由产出 schema 渲染替换，{current_time} 渲染时填充。
	Role string
	// Schema 产出数据集 schema：字段规范渲染 + 写入工具校验的单一事实源。
	Schema func() model.DatasetSchema
	// Write 写入工具绑定（工具名/校验 schema/草稿归一化；会话重放共用）。
	Write tools.WriteSpec
	// Example 元数据目录中展示的示例片段。
	Example string
}

// MetadataAgentProfileOf 按任务类型取 agent 装配描述；未注册返回 false（委托聚合注册表）。
func MetadataAgentProfileOf(taskType string) (MetadataAgentProfile, bool) {
	d, ok := TaskTypeOf(taskType)
	if !ok {
		return MetadataAgentProfile{}, false
	}
	return d.Profile, true
}

// metadataAgentProfileFor 解析已注册的元数据 Agent Profile。
func metadataAgentProfileFor(taskType string) (MetadataAgentProfile, error) {
	p, ok := MetadataAgentProfileOf(taskType)
	if !ok {
		return p, fmt.Errorf("任务类型 %s 未注册 agent 装配描述（MetadataAgentProfileOf）", taskType)
	}
	return p, nil
}

/* ---- requirement_import ---- */

func requirementProfile() MetadataAgentProfile {
	return MetadataAgentProfile{
		Role:    requirementRole,
		Schema:  model.RequirementSchema,
		Write:   tools.DefaultWriteSpec(),
		Example: requirementExample,
	}
}

// requirementRole 需求导入的指令头。字段规范段由 RequirementSchema 渲染注入
// （{field_spec} 占位）——字段增删改，提示词与写入校验自动跟随。
const requirementRole = `你是一位专业的项目管理助手和技术顾问，擅长分析需求文档并提取结构化的工作项信息。

## 任务
分析以下需求文档，识别其中的所有工作项（需求、任务、功能点等），并按项目分组整理。

{field_spec}

## 分析要点
1. 项目识别：仔细区分多项目需求，避免混淆
2. 需求拆分：大需求拆为独立可交付的合理粒度
3. 优先级：综合重要性、紧急程度、依赖关系判断
4. 完整性：提取所有明确的需求点，不遗漏
5. 负责人识别：注意"由XXX负责"等表述
6. 状态识别：仅提取文档明确写出的状态
7. 解决方案：专业、具体、可执行`

// requirementExample 是元数据目录展示的示例片段。
const requirementExample = `[
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
