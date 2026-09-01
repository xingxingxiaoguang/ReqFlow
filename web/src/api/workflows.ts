import { api } from './client'

export type WorkflowPort = {
  name: string
  label: string
  resource_type: string
  required: boolean
  description?: string
}

export type CapabilityPort = WorkflowPort & { role: 'primary' | 'side' | 'delivery'; multiple?: boolean }

export type Capability = {
  ref: { kind: string; version: number }
  label: string
  description: string
  inputs: CapabilityPort[]
  outputs: CapabilityPort[]
  rule_requirements?: string[]
  config_schema?: unknown
  default_config?: Record<string, unknown>
  has_side_effects?: boolean
  requires_llm?: boolean
  manual_completion?: boolean
}

export type WorkflowNode = {
  id: string
  name: string
  capability: { kind: string; version: number }
  config?: Record<string, unknown>
}

export type WorkflowDraft = {
  id: string
  workspace_id: string
  key: string
  name: string
  description?: string
  revision: number
  inputs: WorkflowPort[]
  outputs: WorkflowPort[]
  nodes: WorkflowNode[]
  connections: Array<{ from: { kind: string; node_id?: string; port: string }; to: { kind: string; node_id?: string; port: string } }>
  rules: {
    data_contract?: { record_granularity: string; key_fields: string[]; fields: Array<{ key: string; label: string; type: string; required?: boolean }> }
    extraction?: unknown
    search?: unknown
    output_contract?: unknown
    decisions?: unknown[]
  }
  acceptance_cases?: Array<{ id: string; name: string; input: unknown; expectation: unknown; last_passed: boolean; last_passed_revision?: number; last_preview_id?: string }>
}

export type ValidationIssue = { code: string; path: string; message: string; severity: 'warning' | 'error' }
export type WorkflowView = { draft: WorkflowDraft; issues: ValidationIssue[]; active_revision_id?: string }
export type Preview = { id: string; workflow_id: string; draft_revision: number; status: 'passed' | 'failed'; output_manifest: unknown; issues?: ValidationIssue[] }
export type Revision = WorkflowDraft & { revision_no: number; content_hash: string; published_by: string; published_at: string }

export const workflowsApi = {
  capabilities: () => api.get<{ capabilities: Capability[] }>('/api/capabilities'),
  list: () => api.get<{ workflows: Array<{ id: string; key: string; name: string; revision: number; active_revision_id?: string }> }>('/api/workflows'),
  get: (id: string) => api.get<WorkflowView>(`/api/workflows/${id}`),
  create: (input: { key: string; name: string; description?: string; inputs: WorkflowPort[]; outputs: WorkflowPort[] }) => api.post<WorkflowView>('/api/workflows', input),
  command: (id: string, expectedRevision: number, type: string, payload: unknown) => api.post<{ draft: WorkflowDraft; issues: ValidationIssue[] }>(`/api/workflows/${id}/commands`, { command_id: crypto.randomUUID(), expected_revision: expectedRevision, type, payload }),
  validate: (id: string, mode: 'draft' | 'publish') => api.post<{ draft: WorkflowDraft; issues: ValidationIssue[]; valid: boolean }>(`/api/workflows/${id}/validate`, { mode }),
  preview: (id: string, draftRevision: number, input: unknown) => api.post<Preview>(`/api/workflows/${id}/previews`, { draft_revision: draftRevision, input }),
  runAcceptance: (id: string, caseID: string, previewID: string) => api.post<{ draft: WorkflowDraft; preview: Preview }>(`/api/workflows/${id}/acceptance-cases/${caseID}/run`, { preview_id: previewID }),
  publish: (id: string, expectedRevision: number) => api.post<Revision>(`/api/workflows/${id}/publish`, { expected_revision: expectedRevision }),
  revisions: (id: string) => api.get<{ revisions: Revision[] }>(`/api/workflows/${id}/revisions`),
}
