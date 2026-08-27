import { api } from './client'
import type { MetadataCatalog, PromptPreviewInput, PromptPreview, TaskTypeView } from './types'

/** 元数据 API 封装（M1 只读：目录总览 / 任务类型聚合视图 / 提示词预览） */
export const metadataApi = {
  catalog: () => api.get<MetadataCatalog>('/api/metadata'),
  taskType: (type: string) => api.get<TaskTypeView>(`/api/metadata/task-types/${type}`),
  promptPreview: (input: PromptPreviewInput) => api.post<PromptPreview>('/api/metadata/render/preview', input),
}
