import { api } from './client'

export type PlatformConfigKind = 'llm' | 'embedding' | 'rerank' | 'mineru'

export interface LLMPlatformConfig {
  provider: 'openai' | 'anthropic'
  base_url: string
  model: string
  temperature: number
  max_tokens: number
  timeout_ms: number
}

export interface EmbeddingPlatformConfig {
  base_url: string
  model: string
  dimensions: number
  batch_size: number
  timeout_ms: number
}

export interface RerankPlatformConfig {
  base_url: string
  model: string
  timeout_ms: number
}

export interface MinerUPlatformConfig {
  enabled: boolean
  api_url: string
  model_version: 'vlm' | 'pipeline'
  timeout_ms: number
  poll_interval_ms: number
}

export type PlatformConfigValue = LLMPlatformConfig | EmbeddingPlatformConfig | RerankPlatformConfig | MinerUPlatformConfig

export interface PlatformConfigItem {
  id: string
  kind: PlatformConfigKind
  name: string
  source: 'file' | 'database'
  active: boolean
  read_only: boolean
  configured: boolean
  secret_configured: boolean
  config: PlatformConfigValue
  created_at?: string
  updated_at?: string
}

export interface PlatformConfigGroup {
  kind: PlatformConfigKind
  active_id: string
  items: PlatformConfigItem[]
}

export interface PlatformConfigCatalog {
  workspace_name: string
  summary: Record<PlatformConfigKind, boolean>
  groups: PlatformConfigGroup[]
}

export interface PlatformConfigInput {
  name: string
  config: PlatformConfigValue
  secret?: string
  activate?: boolean
}

export const platformConfigsApi = {
  catalog: () => api.get<PlatformConfigCatalog>('/api/platform-configs'),
  create: (kind: PlatformConfigKind, input: PlatformConfigInput) =>
    api.post<PlatformConfigItem>(`/api/platform-configs/${kind}`, input),
  update: (kind: PlatformConfigKind, id: string, input: PlatformConfigInput) =>
    api.put<PlatformConfigItem>(`/api/platform-configs/${kind}/${id}`, input),
  activate: (kind: PlatformConfigKind, id: string) =>
    api.post<{ active: boolean }>(`/api/platform-configs/${kind}/${id}/activate`),
  remove: (kind: PlatformConfigKind, id: string) =>
    api.del<{ deleted: boolean }>(`/api/platform-configs/${kind}/${id}`),
}
