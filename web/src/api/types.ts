/** API 类型定义（与后端契约对齐） */

export interface ApiResponse<T> {
  success: boolean
  data?: T
  error?: string
}

/** LLM 分析产出的工作项草稿 */
export interface DraftItem {
  id?: string
  record_id?: string
  project_name: string
  title: string
  description: string
  priority: 'High' | 'Medium' | 'Low'
  estimated_hours: number
  start_at: string
  end_at?: string
  type_id: string
  assignee_name: string
  state: string
  solution_suggestion: string
  status?: string
  match?: DuplicateMatch | null
}

export interface ProjectMatch {
  id: string
  name: string
  score: number
  match_type: 'exact' | 'semantic'
  suggested_name: string
}

export interface DuplicateMatch {
  id: string
  title: string
  score: number
  match_type: 'exact' | 'semantic'
}

export interface DuplicateResult {
  index: number
  match: DuplicateMatch | null
}

/** 任务状态机：pending → running → awaiting(人工门) | paused → running → succeeded | failed */
export type TaskStatus = 'pending' | 'running' | 'awaiting' | 'paused' | 'succeeded' | 'failed'
export type TaskType = 'requirement_import'

/** 数据集类型（任务产出的结果集） */
export type DatasetType = 'requirement'

/** 数据集写入模式（任务 → 数据集的标准化接缝） */
export type WriteMode = 'create' | 'merge' | 'upsert' | 'replace'

/** 写入声明：模式 + 目标数据集 */
export interface DatasetTarget {
  mode: WriteMode
  dataset_id?: string
  dataset_name?: string
}

/** 写入预览（冲突分桶） */
export interface WritePreview {
  mode: WriteMode
  dataset_id?: string
  dataset_name?: string
  insert: number
  update: number
  unchanged: number
  invalid: number
  total: number
  errors?: string[]
}

/** 数据集 schema（字段合同：表格列/筛选器/向量组装的驱动源） */
export type FieldType = 'string' | 'text' | 'number' | 'enum' | 'date'
export type VectorRole = 'none' | 'title' | 'body'

export interface FieldSpec {
  key: string
  label: string
  type: FieldType
  required?: boolean
  enum?: string[]
  filterable?: boolean
  in_vector?: VectorRole
  in_key?: boolean
}

export interface DatasetSchema {
  type: string
  label: string
  version: number
  fields: FieldSpec[]
}

/** 任务（长程流程生命周期载体；Workflow/Input/Output 为 JSON 文本） */
export interface Task {
  ID: string
  Type: TaskType
  Title: string
  Status: TaskStatus
  CurrentStep: number
  Workflow: string
  Input: string
  Output: string
  AgentContext: string
  ItemsCount: number
  ImportedCount: number
  FailedCount: number
  TargetProjectID: string
  TargetProjectName: string
  OutputDatasetID: string
  InputDatasetID: string
  ErrorMessage: string
  CreatedAt: string
  UpdatedAt: string
  StartedAt: string
  FinishedAt: string
}

/** 数据集（任务产出的结果集，任务间衔接的载体） */
export interface Dataset {
  ID: string
  Type: DatasetType
  Name: string
  Description: string
  Tags: string[]
  SourceTaskID: string
  Status: 'ready' | 'building'
  ItemCount: number
  SchemaVersion: number
  CreatedAt: string
  UpdatedAt: string
}

/** 数据集条目（fields 为 schema 类型化字段 JSON 文本） */
export interface DatasetItem {
  ID: string
  DatasetID: string
  Fields: string
  ItemKey: string
  Fingerprint: string
  SourceTaskID: string
  CreatedAt: string
  UpdatedAt: string
}

/** 语义查询命中（score/match_type 仅语义命中附带） */
export interface QueryHit extends DatasetItem {
  Score?: number
  MatchType?: 'semantic'
}

/** 归档种类 */
export type ArchiveKind = 'task' | 'dataset'

/** 归档任务（含步骤/明细快照，恢复后可继续流程） */
export interface ArchivedTask extends Task {
  ArchivedAt: string
}

/** 归档数据集（条目随行归档，含向量） */
export interface ArchivedDataset extends Dataset {
  ArchivedAt: string
}

/** 归档列表视图（两个集合按需取用） */
export interface ArchiveView {
  tasks: ArchivedTask[] | null
  datasets: ArchivedDataset[] | null
}

/** 解析数据集条目字段（通用 map 形状；表格渲染按 schema 取值） */
export function parseDatasetItemFields(fields: string): Record<string, any> {
  try {
    return JSON.parse(fields) as Record<string, any>
  } catch {
    return {}
  }
}

/** 工作流元数据（半元数据驱动：任务类型定义 = 步骤链 + 依赖声明） */
export type StepKind = 'parse' | 'human' | 'analyze' | 'dataset'

export interface StepDependency {
  data: string
  tool: string
}

export interface WorkflowStep {
  seq: number
  name: string
  kind: StepKind
  deps: StepDependency[]
}

export interface Workflow {
  type: TaskType
  name: string
  desc: string
  steps: WorkflowStep[]
}

/** 元数据目录（任务类型聚合定义的统一对外视图；M1 只读，source 恒 builtin，M3 起 DB 覆盖出现 overridden） */
export type MetadataSource = 'builtin' | 'overridden'

export interface TaskTypeSummary {
  type: string
  name: string
  desc: string
  step_count: number
  dataset_type?: string
  schema_label?: string
  source: MetadataSource
}

export interface MetadataCatalog {
  task_types: TaskTypeSummary[]
}

export interface MetadataWriteBinding {
  tool_name: string
}

/** 装配描述视图（role 为声明原文，含 {field_spec} 占位；渲染后形态见 PromptPreview） */
export interface MetadataProfileView {
  role: string
  example: string
  write: MetadataWriteBinding
}

/** agent 工具声明（snippet/guidelines 即系统提示词的同源素材） */
export interface MetadataToolView {
  name: string
  description: string
  snippet: string
  guidelines: string[]
}

/** 任务类型聚合视图（元数据页详情：workflow + schema + profile + 工具清单） */
export interface TaskTypeView {
  type: string
  name: string
  desc: string
  source: MetadataSource
  dataset_type: string
  workflow: Workflow
  schema: DatasetSchema
  profile: MetadataProfileView
  tools: MetadataToolView[]
}

export interface PromptPreviewInput {
  task_type: string
  special_requirements?: string
}

/** 三段提示词的实时渲染（与运行时装配同一函数：改元数据 → 此处即最终形态） */
export interface PromptPreview {
  task_type: string
  agent_system_prompt: string
  agent_first_message: string
  classic_prompt: string
}

/** 解析任务自带的工作流快照（task.Workflow JSON 文本） */
export function parseTaskWorkflow(task: Task): Workflow | null {
  if (!task?.Workflow) return null
  try {
    const w = JSON.parse(task.Workflow) as Workflow
    return w.steps?.length ? w : null
  } catch {
    return null
  }
}

/** 任务输入（task.Input 解析后） */
export interface TaskInput {
  file_name?: string
  original_file_path?: string
  parsed_text?: string
  special_requirements?: string
  dataset_name?: string
  dataset_target?: DatasetTarget
}

export type TaskStepStatus = 'pending' | 'running' | 'succeeded' | 'failed' | 'awaiting' | 'paused'

export interface TaskStep {
  ID: string
  TaskID: string
  Seq: number
  Name: string
  Status: TaskStepStatus
  Detail: string
  Data: string // JSON 文本（工具轨迹/导入汇总）
  StartedAt: string
  EndedAt: string
}

/** 任务明细草稿（含导入结果回写字段；草稿字段沿用 DraftItem snake_case） */
export interface TaskItem {
  ID: string
  TaskID: string
  project_name: string
  title: string
  description: string
  priority: 'High' | 'Medium' | 'Low'
  estimated_hours: number
  start_at: string
  end_at?: string
  type_id: string
  assignee_name: string
  state: string
  solution_suggestion: string
  Status: 'pending' | 'success' | 'failed'
  ErrorMessage: string
  match?: DuplicateMatch | null
}

export interface TaskDetail {
  task: Task
  steps: TaskStep[]
  items: TaskItem[]
}

export interface SettingsView {
  workspaceName: string
  llm: { baseUrl: string; model: string; configured: boolean; agentMode: boolean }
  embedding: { baseUrl: string; model: string; configured: boolean }
  mineru: { enabled: boolean; configured: boolean }
}

export interface Overview {
  datasets: number
  datasetItems: number
  tasks: number
  recentTasks: Task[]
  recentDatasets: Dataset[]
}

/** SSE 事件负载形状 */
export interface ProgressEvent {
  stage: string
  status?: string
  message: string
  elapsedSec?: number
  current?: number
  total?: number
  title?: string
}

export interface TokenEvent {
  delta: string
  phase: 'thinking' | 'answer'
}

/** agent 模式工具调用轨迹（/api/analyze SSE tool 事件，两端契约见 handler_analyze.go） */
export interface ToolEvent {
  phase: 'start' | 'end'
  call_id: string
  name: string
  args?: string
  details?: string
  is_error?: boolean
}

/** 工具轨迹条目（store 内聚状态，由 ToolEvent 驱动） */
export interface ToolTrace {
  callId: string
  name: string
  args?: string
  status: 'running' | 'done' | 'error'
  details?: string
}

/** agent 人工交互事件（ask_human 工具 ↔ 前端弹窗） */
export interface DialogEvent {
  phase: 'ask' | 'close'
  call_id: string
  question?: string
  options?: string[]
  reason?: string // close：answered | cancelled
}

/** 当前等待回答的提问（SSE snapshot 恢复用；无则 null） */
export interface PendingDialog {
  callId: string
  question: string
  options?: string[]
}
