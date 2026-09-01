import { api } from './client'

export type ResourceBinding = {
  id?: string
  node_run_id?: string
  node_id?: string
  port: string
  direction: 'input' | 'output'
  resource_type: string
  resource_id: string
  boundary?: unknown
  provenance?: unknown
  created_at?: string
}

export type NodeRun = {
  id: string
  run_id: string
  node_id: string
  ordinal: number
  node: {
    id: string
    name: string
    capability: {
      ref: { kind: string; version: number }
      label: string
      inputs: Array<{ name: string; resource_type: string; required: boolean }>
      outputs: Array<{ name: string; resource_type: string; required: boolean }>
      manual_completion?: boolean
    }
    config?: unknown
  }
  status: string
  attempt: number
  checkpoint?: unknown
  progress?: unknown
  error_code?: string
  error_message?: string
  started_at?: string
  finished_at?: string
}

export type WorkflowRunSnapshot = {
  run: {
    id: string
    workflow_id: string
    revision_id: string
    workspace_id: string
    status: string
    current_node_id?: string
    error_code?: string
    error_message?: string
    created_at: string
    started_at?: string
    finished_at?: string
    revision: { name: string; key: string; revision_no: number; content_hash: string }
  }
  nodes: NodeRun[]
  bindings: ResourceBinding[]
}

export const workflowRunsApi = {
  list: (workspaceID?: string) => api.get<{ runs: WorkflowRunSnapshot[] }>(workspaceID ? `/api/workflow-runs?workspace_id=${encodeURIComponent(workspaceID)}` : '/api/workflow-runs'),
  get: (id: string) => api.get<WorkflowRunSnapshot>(`/api/workflow-runs/${id}`),
  create: (input: { revision_id: string; inputs: Array<{ port: string; resource_type: string; resource_id: string; boundary?: unknown }> }) => api.post<WorkflowRunSnapshot>('/api/workflow-runs', input),
  start: (id: string) => api.post<WorkflowRunSnapshot>(`/api/workflow-runs/${id}/start`, {}),
  pause: (id: string) => api.post<WorkflowRunSnapshot>(`/api/workflow-runs/${id}/pause`, {}),
  resume: (id: string) => api.post<WorkflowRunSnapshot>(`/api/workflow-runs/${id}/resume`, {}),
  retry: (id: string, nodeID: string) => api.post<WorkflowRunSnapshot>(`/api/workflow-runs/${id}/nodes/${nodeID}/retry`, {}),
  completeManual: (id: string, nodeID: string, payload: unknown) => api.post<WorkflowRunSnapshot>(`/api/workflow-runs/${id}/nodes/${nodeID}/manual-completion`, { payload }),
}
