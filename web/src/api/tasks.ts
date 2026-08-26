import { api } from './client'
import type { Dataset, DatasetItem, DatasetType, Task, TaskDetail, TaskStatus, TaskType, Workflow } from './types'

/** 任务 API 封装（生命周期：创建/列表/详情/编辑/暂停/继续/完成/步骤触发/草稿保存 + 数据集浏览） */
export const tasksApi = {
  workflows: () => api.get<{ workflows: Workflow[] }>('/api/workflows'),
  listDatasets: (params: { type?: DatasetType; limit?: number } = {}) => {
    const q = new URLSearchParams()
    if (params.type) q.set('type', params.type)
    if (params.limit) q.set('limit', String(params.limit))
    return api.get<{ datasets: Dataset[] }>(`/api/datasets?${q}`)
  },
  getDataset: (id: string) => api.get<{ dataset: Dataset; items: DatasetItem[] }>(`/api/datasets/${id}`),
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
  triggerGenerateDataset: (id: string, datasetName: string) =>
    api.post<{ task_id: string }>(`/api/tasks/${id}/dataset`, { dataset_name: datasetName }),
  pause: (id: string) => api.post<{ task: Task }>(`/api/tasks/${id}/pause`),
  resume: (id: string) => api.post<{ task: Task }>(`/api/tasks/${id}/resume`),
  complete: (id: string) => api.post<{ task: Task }>(`/api/tasks/${id}/complete`),
}
