package app

import (
	"fmt"

	"reqflow/internal/app/tools"
	"reqflow/internal/domain/model"
)

// AnalyzeProfile 一类任务的 agent 装配描述（聚合注册表的 agent 侧构件）。
//
// 系统提示词由此动态装配（无固定模板）：
//
//	profile.Role（{field_spec} 占位 → 产出 schema 渲染）+ 额外要求（用户输入）
//	+ 工具指南（agent.DocumentedTool 同源组装）
//
// 单发降级路径同理由 profile 装配：输出契约 + 示例（profile.Example 覆盖，
// 缺省按 schema 生成骨架）。新增任务类型 = 在聚合注册表（registry.go）加一条
// 聚合定义（工作流 + schema + 本 profile），执行骨架与提示词装配零改动。
type AnalyzeProfile struct {
	// Role 指令头：角色 + 任务 + 分析要点。模式中立（不预设输出形态）；
	// {field_spec} 占位符由产出 schema 渲染替换，{current_time} 渲染时填充。
	Role string
	// Schema 产出数据集 schema：字段规范渲染 + 写入工具校验的单一事实源。
	Schema func() model.DatasetSchema
	// Write 写入工具绑定（工具名/校验 schema/草稿归一化；会话重放共用）。
	Write tools.WriteSpec
	// Example 单发降级的输出示例（含 {current_time} 占位；空则按 schema 生成骨架）。
	Example string
}

// AnalyzeProfileOf 按任务类型取 agent 装配描述；未注册返回 false（委托聚合注册表）。
func AnalyzeProfileOf(taskType string) (AnalyzeProfile, bool) {
	d, ok := TaskTypeOf(taskType)
	if !ok {
		return AnalyzeProfile{}, false
	}
	return d.Profile, true
}

// profileFor 运行期解析：空类型（旧会话/测试）回退 requirement；未注册类型报错。
func profileFor(taskType string) (AnalyzeProfile, error) {
	if taskType == "" {
		return requirementProfile(), nil
	}
	p, ok := AnalyzeProfileOf(taskType)
	if !ok {
		return p, fmt.Errorf("任务类型 %s 未注册 agent 装配描述（AnalyzeProfileOf）", taskType)
	}
	return p, nil
}

/* ---- requirement_import ---- */

func requirementProfile() AnalyzeProfile {
	return AnalyzeProfile{
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

// requirementExample 单发降级的输出示例（富示例保质量；新任务类型可省略，
// 缺省由 schema 生成骨架）。
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
