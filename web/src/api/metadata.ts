import { api } from './client'
import type {
  MetadataCatalog, PromptPreviewInput, PromptPreview, TaskTypeView,
  SchemaUpdateResult, ProfileUpdateResult, MetadataVersionView, MetadataExport, MetadataImportResult, DatasetSchema,
} from './types'

/** schema 受控保存入参（保存前 check；⚠️ 项需 confirm_risky） */
export interface SchemaSaveInput {
  schema: DatasetSchema
  confirm_risky?: boolean
  summary?: string
}

/** 元数据 API 封装：M1 只读 + M3 受控编辑（写路径是显式管理动作，每次写后端必记审计） */
export const metadataApi = {
  catalog: () => api.get<MetadataCatalog>('/api/metadata'),
  taskType: (type: string) => api.get<TaskTypeView>(`/api/metadata/task-types/${type}`),
  promptPreview: (input: PromptPreviewInput) => api.post<PromptPreview>('/api/metadata/render/preview', input),

  checkSchema: (datasetType: string, input: SchemaSaveInput) =>
    api.post<SchemaUpdateResult>(`/api/metadata/schemas/${datasetType}/check`, input),
  /** 守卫拦截（409）不抛错：成功/被拦截都返回 result（含判定明细与影响面） */
  updateSchema: async (datasetType: string, input: SchemaSaveInput): Promise<SchemaUpdateResult> => {
    const r = await api.putDetail<SchemaUpdateResult>(`/api/metadata/schemas/${datasetType}`, input)
    if (r.data) return r.data
    throw new Error(r.error || '保存失败')
  },
  resetSchema: (datasetType: string) => api.del<SchemaUpdateResult>(`/api/metadata/schemas/${datasetType}`),

  updateProfile: (taskType: string, input: { role: string; example: string; summary?: string }) =>
    api.put<ProfileUpdateResult>(`/api/metadata/profiles/${taskType}`, input),
  resetProfile: (taskType: string) => api.del<ProfileUpdateResult>(`/api/metadata/profiles/${taskType}`),

  history: (kind: 'dataset_schema' | 'analyze_profile', key: string) =>
    api.get<MetadataVersionView[]>(`/api/metadata/history/${kind}/${key}`),
  export: () => api.get<MetadataExport>('/api/metadata/export'),
  import: (input: { task_types: { type: string; schema?: DatasetSchema; profile?: { role: string; example: string } }[]; confirm_risky?: boolean; summary?: string }) =>
    api.post<MetadataImportResult>('/api/metadata/import', input),
}
