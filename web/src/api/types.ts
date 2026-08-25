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

export interface ImportRecord {
  ID: string
  FileName: string
  Status: 'analyzed' | 'importing' | 'success' | 'partial_success' | 'failed'
  ItemsCount: number
  TargetProjectName?: string
  ImportedCount: number
  FailedCount: number
  CreatedAt: string
}

export interface SyncedProject {
  ID: string
  Name: string
  Description: string
}

export interface SyncedWorkItem {
  ID: string
  ProjectID: string
  Identifier: string
  Title: string
  Kind: string
}

export interface SettingsView {
  workspaceName: string
  llm: { baseUrl: string; model: string; configured: boolean }
  embedding: { baseUrl: string; model: string; configured: boolean }
  pingcode: { host: string; configured: boolean }
  mineru: { enabled: boolean; configured: boolean }
}

export interface Overview {
  projects: number
  workItems: number
  records: number
  recentRecords: ImportRecord[]
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
