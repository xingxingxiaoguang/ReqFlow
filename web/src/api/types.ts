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
  SourceTaskID: string
  Status: 'ready' | 'building'
  ItemCount: number
  CreatedAt: string
}

/** 数据集条目（fields 为草稿形状 JSON 文本） */
export interface DatasetItem {
  ID: string
  DatasetID: string
  Fields: string
  CreatedAt: string
}

/** 解析数据集条目字段（草稿形状） */
export function parseDatasetItemFields(fields: string): DraftItem {
  try {
    return JSON.parse(fields) as DraftItem
  } catch {
    return {} as DraftItem
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
  llm: { baseUrl: string; model: string; configured: boolean }
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
