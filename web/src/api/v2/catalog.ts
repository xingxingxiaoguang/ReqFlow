import { api } from '../client'
import type {
  V2AnalysisProfile, V2Artifact, V2AssetSet, V2Dataset, V2DatasetBatch,
  V2ExtractionProfile, V2ExtractionProfileDetail, V2JSONSchema, V2RetrievalProfile, V2RetrievalSnapshot,
  V2Schema, V2Task, V2TaskDefinition,
} from './types'

const query = (params: Record<string, string | number | undefined>) => {
  const result = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) if (value !== undefined && value !== '') result.set(key, String(value))
  return result.toString()
}

export interface DefinitionInput {
  workspace_id?: string
  key: string
  name: string
  description?: string
  status: 'draft' | 'active'
  input_ports: Record<string, { resource_type: string; required?: boolean; description?: string }>
  output_ports?: Record<string, { resource_type: string; required?: boolean; description?: string }>
  output_bindings?: Record<string, string>
  steps: Array<{
    id: string; name: string; kind: string; depends_on?: string[]
    inputs?: Record<string, string>; outputs?: Record<string, string>; config?: Record<string, unknown>
  }>
}

export interface ExtractionProfileInput {
  workspace_id?: string
  name: string
  target_schema_id: string
  record_granularity: string
  system_instruction: string
  field_guides: Record<string, unknown>
  examples: unknown[]
  normalization_rules: Array<Record<string, unknown>>
  validation_rules: Array<Record<string, unknown>>
}

export const v2CatalogApi = {
  listDefinitions: (params: { status?: string; limit?: number } = {}) =>
    api.get<{ task_definitions: V2TaskDefinition[] }>(`/api/v2/task-definitions?${query(params)}`),
  getDefinition: (id: string) => api.get<{ definition: V2TaskDefinition }>(`/api/v2/task-definitions/${id}`),
  createDefinition: (input: DefinitionInput) =>
    api.post<{ definition: V2TaskDefinition }>('/api/v2/task-definitions', input),
  archiveDefinition: (id: string) => api.post<{ archived: boolean }>(`/api/v2/task-definitions/${id}/archive`, {}),
  restoreDefinition: (id: string) => api.post<{ restored: boolean }>(`/api/v2/task-definitions/${id}/restore`, {}),
  listSchemas: () => api.get<{ schemas: V2Schema[] }>('/api/v2/schemas?limit=200'),
  createSchema: (input: { name: string; description?: string; json_schema: V2JSONSchema; ui_schema?: Record<string, unknown> }) =>
    api.post<{ schema: V2Schema }>('/api/v2/schemas', input),
  listDatasets: (params: { status?: string; purpose?: string } = {}) =>
    api.get<{ datasets: V2Dataset[] }>(`/api/v2/datasets?${query({ ...params, limit: 200 })}`),
  createDataset: (input: { name: string; description?: string; purpose: string; schema_id: string; key_fields: string[] }) =>
    api.post<{ dataset: V2Dataset }>('/api/v2/datasets', input),
  listDatasetItems: (id: string, afterSeq = 0, throughSeq = 0, limit = 200) =>
    api.get<{ items: Array<{ id: string; commit_seq: number; fields: Record<string, unknown>; provenance: Record<string, unknown> }> }>(`/api/v2/datasets/${id}/items?after_seq=${afterSeq}&through_seq=${throughSeq}&limit=${limit}`),
  listBatches: (id: string) => api.get<{ batches: V2DatasetBatch[] }>(`/api/v2/datasets/${id}/batches?limit=200`),
  archiveDataset: (id: string) => api.post<{ archived: boolean }>(`/api/v2/datasets/${id}/archive`, {}),
  restoreDataset: (id: string) => api.post<{ restored: boolean }>(`/api/v2/datasets/${id}/restore`, {}),
  listAssetSets: () => api.get<{ asset_sets: V2AssetSet[] }>('/api/v2/asset-sets?limit=200'),
  uploadAsset: (file: File) => {
    const body = new FormData(); body.append('file', file)
    return api.post<{ asset: { id: string; filename: string }; created: boolean }>('/api/v2/assets', body)
  },
  createAssetSet: (input: { name: string; asset_ids: string[] }) => api.post<{ asset_set: V2AssetSet }>('/api/v2/asset-sets', input),
  listExtractionProfiles: (params: { workspaceId?: string; targetSchemaId?: string; limit?: number } = {}) =>
    api.get<{ extraction_profiles: V2ExtractionProfile[] }>(`/api/v2/extraction-profiles?${query({
      workspace_id: params.workspaceId,
      target_schema_id: params.targetSchemaId,
      limit: params.limit ?? 200,
    })}`),
  createExtractionProfile: (input: ExtractionProfileInput) => api.post<{ extraction_profile: V2ExtractionProfile }>('/api/v2/extraction-profiles', input),
  getExtractionProfile: (id: string) =>
    api.get<{ extraction_profile: V2ExtractionProfileDetail }>(`/api/v2/extraction-profiles/${id}`),
  deleteExtractionProfile: (id: string) => api.del<{ deleted: boolean }>(`/api/v2/extraction-profiles/${id}`),
  listRetrievalProfiles: () => api.get<{ retrieval_profiles: V2RetrievalProfile[] }>('/api/v2/retrieval-profiles?limit=200'),
  queryRetrievalProfiles: (params: { workspaceId?: string; datasetSchemaId?: string; limit?: number } = {}) =>
    api.get<{ retrieval_profiles: V2RetrievalProfile[] }>(`/api/v2/retrieval-profiles?${query({
      workspace_id: params.workspaceId,
      dataset_schema_id: params.datasetSchemaId,
      limit: params.limit ?? 200,
    })}`),
  listRetrievalSnapshots: () => api.get<{ retrieval_snapshots: V2RetrievalSnapshot[] }>('/api/v2/retrieval-snapshots?limit=200'),
  queryRetrievalSnapshots: (params: { datasetId?: string; retrievalProfileId?: string; status?: string; limit?: number } = {}) =>
    api.get<{ retrieval_snapshots: V2RetrievalSnapshot[] }>(`/api/v2/retrieval-snapshots?${query({
      dataset_id: params.datasetId,
      retrieval_profile_id: params.retrievalProfileId,
      status: params.status,
      limit: params.limit ?? 200,
    })}`),
  createRetrievalProfile: (input: Record<string, unknown>) => api.post<{ retrieval_profile: V2RetrievalProfile }>('/api/v2/retrieval-profiles', input),
  deleteRetrievalProfile: (id: string) => api.del<{ deleted: boolean }>(`/api/v2/retrieval-profiles/${id}`),
  deleteRetrievalSnapshot: (id: string) => api.del<{ deleted: boolean }>(`/api/v2/retrieval-snapshots/${id}`),
  search: (input: Record<string, unknown>) => api.post<{ search: Record<string, unknown> }>('/api/v2/retrieval/search', input),
  listAnalysisProfiles: () => api.get<{ analysis_profiles: V2AnalysisProfile[] }>('/api/v2/analysis-profiles?limit=200'),
  createAnalysisProfile: (input: { name: string; instruction: string; output_schema: V2JSONSchema }) =>
    api.post<{ analysis_profile: V2AnalysisProfile }>('/api/v2/analysis-profiles', input),
  getAnalysisResult: (id: string) => api.get<{ analysis_result: { id: string; status: string; output: Record<string, unknown>; model: string; input_tokens: number; output_tokens: number } }>(`/api/v2/analysis-results/${id}`),
  listArtifacts: () => api.get<{ artifacts: V2Artifact[] }>('/api/v2/artifacts?limit=200'),
  listArchives: () => api.get<{ tasks: V2Task[]; datasets: V2Dataset[] }>('/api/v2/archives?limit=200'),
  restoreTask: (id: string) => api.post<{ restored: boolean }>(`/api/v2/tasks/${id}/restore`, {}),
}
