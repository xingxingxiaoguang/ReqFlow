export type V2TaskStatus =
  | 'pending' | 'running' | 'pausing' | 'awaiting' | 'paused' | 'succeeded' | 'failed'

export type V2StepStatus =
  | 'pending' | 'queued' | 'running' | 'awaiting' | 'paused' | 'succeeded' | 'failed' | 'skipped'

export interface V2Task {
  id: string
  workspace_id: string
  definition_id: string
  type: string
  title: string
  status: V2TaskStatus
  current_step: number
  error_message?: string
  created_at: string
  updated_at: string
  started_at?: string
  finished_at?: string
}

export interface V2Resource {
  port_name: string
  resource_type: string
  resource_id: string
  boundary?: Record<string, unknown>
}

export interface V2StepRun {
  id: string
  step_id: string
  name: string
  ordinal: number
  kind: string
  config?: Record<string, unknown>
  status: V2StepStatus
  attempt: number
  input_hash?: string
  config_hash?: string
  progress?: Record<string, unknown>
  error_code?: string
  error_message?: string
  lease_until?: string
  started_at?: string
  finished_at?: string
}

export interface V2TaskSnapshot {
  task: V2Task
  inputs: V2Resource[] | null
  outputs: V2Resource[] | null
  steps: V2StepRun[] | null
  step_outputs: Record<string, V2Resource[]>
}

export interface JSONSchemaProperty {
  type?: string | string[]
  title?: string
  description?: string
  enum?: unknown[]
  format?: string
  items?: JSONSchemaProperty
  [key: string]: unknown
}

export interface V2JSONSchema {
  type?: string
  title?: string
  properties?: Record<string, JSONSchemaProperty>
  required?: string[]
  additionalProperties?: boolean
}

export interface V2Schema {
  id: string
  workspace_id: string
  name: string
  description?: string
  json_schema: V2JSONSchema
  ui_schema: Record<string, unknown>
  schema_hash: string
  created_at: string
}

export interface V2Dataset {
  id: string
  workspace_id: string
  name: string
  description?: string
  purpose: string
  schema_id: string
  key_fields: string[]
  status: string
  current_seq: number
  item_count: number
  created_at: string
  updated_at: string
}

export interface RecordIssue {
  code: string
  field?: string
  severity: 'warning' | 'error'
  message: string
}

export interface RecordChange {
  field: string
  operation: string
  before: unknown
  after: unknown
}

export interface SourceReference {
  dataset_item_id?: string
  asset_id?: string
  block_id?: string
  page_no?: number
  quote?: string
}

export interface ItemProvenance {
  source_refs?: SourceReference[]
  pipeline_key?: string
  source_dataset_id?: string
  source_dataset_item_id?: string
  source_fingerprint?: string
  extraction_profile_id?: string
  model?: string
  prompt_hash?: string
  quality_status?: string
  validation_result_id?: string
  approved_record_set_id?: string
  review_decision_id?: string
  review_action?: string
}

export type ValidationRecordStatus =
  | 'valid' | 'warning' | 'invalid' | 'duplicate_in_batch' | 'conflict_existing_key'

export interface ValidationResult {
  id: string
  transformed_record_id: string
  ordinal: number
  draft_fields: Record<string, unknown>
  fields: Record<string, unknown>
  field_confidence: Record<string, number>
  changes: RecordChange[]
  item_key?: string
  fingerprint?: string
  status: ValidationRecordStatus
  issues: RecordIssue[]
  provenance: ItemProvenance
  created_at: string
}

export interface ValidationResultSet {
  id: string
  transformed_record_set_id: string
  target_dataset_id: string
  target_schema_id: string
  source_step_run_id: string
  status: string
  engine_version: string
  validated_through_seq: number
  record_count: number
  valid_count: number
  warning_count: number
  invalid_count: number
  duplicate_count: number
  conflict_count: number
  created_at: string
  finished_at?: string
  results: ValidationResult[]
}

export type ReviewAction = 'approve' | 'edit' | 'exclude'

export interface ReviewDecisionInput {
  validation_result_id: string
  action: ReviewAction
  fields?: Record<string, unknown>
  note?: string
}

export interface ReviewRecordsInput {
  reviewer: string
  rationale: string
  decisions: ReviewDecisionInput[]
}

export interface RecordReviewDecision {
  id: string
  validation_result_id: string
  transformed_record_id: string
  ordinal: number
  action: ReviewAction
  fields: Record<string, unknown>
  item_key?: string
  fingerprint?: string
  issues: RecordIssue[]
  provenance: ItemProvenance
  note?: string
  created_at: string
}

export interface ApprovedRecordSet {
  id: string
  validation_result_set_id: string
  target_dataset_id: string
  target_schema_id: string
  source_step_run_id: string
  reviewer: string
  rationale: string
  review_hash: string
  reviewed_through_seq: number
  record_count: number
  approved_count: number
  edited_count: number
  excluded_count: number
  created_at: string
  decisions: RecordReviewDecision[]
}
