package app

// 需求分析 Prompt（自 PingCraft 验证过的模板移植，字段与协作平台工作项对齐）。
// 占位符以字面量 {var} 形式注入（避免与 JSON 大括号冲突的模板引擎）。
const analyzePromptTemplate = `你是一位专业的项目管理助手和技术顾问，擅长分析需求文档并提取结构化的工作项信息。

## 任务
分析以下需求文档，识别其中的所有工作项（需求、任务、功能点等），并按项目分组整理。

## 输出要求
返回一个 JSON 数组，每个元素代表一个工作项，包含以下字段：

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
7. 解决方案：专业、具体、可执行

## 输出格式
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
]

{special_requirements_section}

---

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
