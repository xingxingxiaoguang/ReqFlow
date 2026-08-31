import { api } from '../client'
import type {
  ApprovedRecordSet, ReviewRecordsInput, V2Dataset, V2Schema, V2Task,
  V2TaskSnapshot, V2TaskStatus, ValidationResultSet,
} from './types'

export const v2TasksApi = {
  list: (params: { workspaceId?: string; status?: V2TaskStatus; limit?: number } = {}) => {
    const query = new URLSearchParams()
    if (params.workspaceId) query.set('workspace_id', params.workspaceId)
    if (params.status) query.set('status', params.status)
    if (params.limit) query.set('limit', String(params.limit))
    return api.get<{ tasks: V2Task[] }>(`/api/v2/tasks?${query}`)
  },
  get: (id: string) => api.get<V2TaskSnapshot>(`/api/v2/tasks/${id}`),
  create: (input: { definition_id: string; title?: string; bindings: Array<{ port_name: string; resource_type: string; resource_id: string }> }) =>
    api.post<{ task: V2Task }>('/api/v2/tasks', input),
  createBatch: (input: {
    definition_id: string
    title?: string
    bindings: Array<{ port_name: string; resource_type: string; resource_id: string }>
    split_port_name: string
    start_now: boolean
  }) => api.post<{ batch: { id: string; size: number; tasks: V2Task[] } }>('/api/v2/task-batches', input),
  start: (id: string) => api.post<V2TaskSnapshot>(`/api/v2/tasks/${id}/start`, {}),
  pause: (id: string) => api.post<V2TaskSnapshot>(`/api/v2/tasks/${id}/pause`, {}),
  resume: (id: string) => api.post<V2TaskSnapshot>(`/api/v2/tasks/${id}/resume`, {}),
  retry: (id: string, stepId: string) =>
    api.post<V2TaskSnapshot>(`/api/v2/tasks/${id}/steps/${stepId}/retry`, {}),
  approve: (id: string, stepId: string, input: ReviewRecordsInput) =>
    api.post<V2TaskSnapshot>(`/api/v2/tasks/${id}/steps/${stepId}/approve`, input),
  approveResource: (id: string, stepId: string, outputInputs: Record<string, string> = {}) =>
    api.post<V2TaskSnapshot>(`/api/v2/tasks/${id}/steps/${stepId}/approve-resource`, { output_inputs: outputInputs }),
  archive: (id: string) => api.post<{ archived: boolean }>(`/api/v2/tasks/${id}/archive`, {}),
  getValidationSet: (id: string) =>
    api.get<{ validation_result_set: ValidationResultSet }>(`/api/v2/validation-result-sets/${id}`),
  getApprovedSet: (id: string) =>
    api.get<{ approved_record_set: ApprovedRecordSet }>(`/api/v2/approved-record-sets/${id}`),
  getSchema: (id: string) => api.get<{ schema: V2Schema }>(`/api/v2/schemas/${id}`),
  getDataset: (id: string) => api.get<{ dataset: V2Dataset }>(`/api/v2/datasets/${id}`),
}
