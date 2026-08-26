import { api } from './client'
import type {
  ArchiveKind, ArchiveView, Dataset, DatasetItem, DatasetSchema, DatasetType,
  QueryHit, Task, TaskDetail, TaskStatus, TaskType, Workflow, WritePreview,
} from './types'

/** 任务 API 封装（生命周期：创建/列表/详情/编辑/暂停/继续/完成/步骤触发/草稿保存 + 数据集浏览与写入） */
export const tasksApi = {
  workflows: () => api.get<{ workflows: Workflow[] }>('/api/workflows'),
  schemas: () => api.get<{ schemas: DatasetSchema[] }>('/api/datasets/schemas'),
  listDatasets: (params: { type?: DatasetType; limit?: number } = {}) => {
    const q = new URLSearchParams()
    if (params.type) q.set('type', params.type)
    if (params.limit) q.set('limit', String(params.limit))
    return api.get<{ datasets: Dataset[] }>(`/api/datasets?${q}`)
  },
  getDataset: (id: string) => api.get<{ dataset: Dataset; items: DatasetItem[] }>(`/api/datasets/${id}`),
  queryDatasetItems: (id: string, params: { q?: string; filters?: Record<string, string>; limit?: number } = {}) => {
    const search = new URLSearchParams()
    if (params.q) search.set('q', params.q)
    if (params.limit) search.set('limit', String(params.limit))
    for (const [k, v] of Object.entries(params.filters ?? {})) search.set(`f[${k}]`, v)
    return api.get<{ items: QueryHit[]; total: number }>(`/api/datasets/${id}/items?${search}`)
  },
  create: (type: TaskType, title: string) => api.post<{ task: Task }>('/api/tasks', { type, title }),
  list: (params: { status?: TaskStatus; type?: TaskType; limit?: number } = {}) => {
    const q = new URLSearchParams()
    if (params.status) q.set('status', params.status)
    if (params.type) q.set('type', params.type)
    if (params.limit) q.set('limit', String(params.limit))
    return api.get<{ tasks: Task[] }>(`/api/tasks?${q}`)
  },
  get: (id: string) => api.get<TaskDetail>(`/api/tasks/${id}`),
  patch: (id: string, p: { title?: string; parsed_text?: string; special_requirements?: string }) =>
    api.patch<{ task: Task }>(`/api/tasks/${id}`, p),
  saveItems: (id: string, items: { id?: string; draft: Record<string, unknown> }[]) =>
    api.post<{ ok: boolean }>(`/api/tasks/${id}/items`, { items }),
  triggerParse: (id: string, file: File) => {
    const form = new FormData()
    form.append('file', file)
    return api.post<{ task_id: string }>(`/api/tasks/${id}/parse`, form)
  },
  triggerAnalyze: (id: string) => api.post<{ task_id: string }>(`/api/tasks/${id}/analyze`, {}),
  triggerGenerateDataset: (id: string, target: { mode: string; dataset_id?: string; dataset_name?: string }) =>
    api.post<{ task_id: string }>(`/api/tasks/${id}/dataset`, target),
  previewDatasetWrite: (id: string, target: { mode: string; dataset_id?: string; dataset_name?: string }) =>
    api.post<{ preview: WritePreview }>(`/api/tasks/${id}/dataset/preview`, target),
  pause: (id: string) => api.post<{ task: Task }>(`/api/tasks/${id}/pause`),
  resume: (id: string) => api.post<{ task: Task }>(`/api/tasks/${id}/resume`),
  complete: (id: string) => api.post<{ task: Task }>(`/api/tasks/${id}/complete`),
  answerDialog: (id: string, callId: string, answer: string) =>
    api.post<{ ok: boolean }>(`/api/tasks/${id}/dialog`, { call_id: callId, answer }),
  archiveTask: (id: string) => api.del<{ archived: boolean }>(`/api/tasks/${id}`),
  archiveDataset: (id: string) => api.del<{ archived: boolean }>(`/api/datasets/${id}`),
  listArchives: (params: { kind?: ArchiveKind; type?: string; limit?: number } = {}) => {
    const q = new URLSearchParams()
    if (params.kind) q.set('kind', params.kind)
    if (params.type) q.set('type', params.type)
    if (params.limit) q.set('limit', String(params.limit))
    return api.get<ArchiveView>(`/api/archives?${q}`)
  },
  restoreArchive: (kind: ArchiveKind, id: string) =>
    api.post<{ task?: Task; restored?: boolean }>(`/api/archives/${kind}/${id}/restore`, {}),
}
