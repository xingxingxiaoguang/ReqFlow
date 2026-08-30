import { api } from '../client'
import { postSSE } from '../sse'

export type AgentSessionStatus = 'idle' | 'running' | 'error'

export interface AgentToolCall {
  id: string
  name: string
  arguments: Record<string, unknown>
}

export interface AgentBlock {
  type: 'text' | 'thinking' | 'toolCall'
  text?: string
  thinking?: string
  tool_call?: AgentToolCall
}

export interface AgentMessage {
  role: 'user' | 'assistant' | 'toolResult'
  content?: AgentBlock[]
  tool_call_id?: string
  tool_name?: string
  result?: string
  details?: string
  is_error?: boolean
  stop_reason?: string
  error_message?: string
  timestamp?: number
}

export interface AgentSessionSummary {
  id: string
  workspace_id: string
  title: string
  status: AgentSessionStatus
  last_error?: string
  message_count: number
  last_message?: string
  created_at: string
  updated_at: string
}

export interface AgentSession extends AgentSessionSummary {
  messages: AgentMessage[]
}

export interface AgentStreamEvent {
  type: 'started' | 'assistant_delta' | 'thinking_delta' | 'message' | 'tool_start' | 'tool_end' | 'done'
  delta?: string
  tool_call_id?: string
  tool_name?: string
  arguments?: Record<string, unknown>
  details?: string
  output?: string
  is_error?: boolean
  message?: AgentMessage
  session?: AgentSession
}

export interface AgentToolConfig {
  name: string
  label: string
  group: string
  description: string
  enabled: boolean
}

export interface AgentSkill {
  id: string
  slug: string
  title: string
  description: string
  prompt: string
  enabled: boolean
  builtin: boolean
  created_at: string
  updated_at: string
}

export interface AgentConfig {
  tools: AgentToolConfig[]
  skills: AgentSkill[]
}

export interface CreateAgentSkillInput {
  slug: string
  title: string
  description?: string
  prompt: string
  enabled?: boolean
}

export const v2AgentApi = {
  listSessions: () => api.get<{ sessions: AgentSessionSummary[] }>('/api/v2/agent/sessions?limit=100'),
  createSession: (title?: string) => api.post<{ session: AgentSession }>('/api/v2/agent/sessions', { title }),
  getSession: (id: string) => api.get<{ session: AgentSession }>(`/api/v2/agent/sessions/${id}`),
  stopSession: (id: string) => api.post<{ stopped: boolean }>(`/api/v2/agent/sessions/${id}/stop`, {}),
  getConfig: () => api.get<{ config: AgentConfig }>('/api/v2/agent/config'),
  createSkill: (input: CreateAgentSkillInput) => api.post<{ skill: AgentSkill }>('/api/v2/agent/skills', input),
  setSkillEnabled: (id: string, enabled: boolean) =>
    api.put<{ enabled: boolean }>(`/api/v2/agent/skills/${id}/status`, { enabled }),
  setToolEnabled: (name: string, enabled: boolean) =>
    api.put<{ enabled: boolean }>(`/api/v2/agent/tools/${encodeURIComponent(name)}/status`, { enabled }),
  sendMessage: (
    id: string,
    message: string,
    onEvent: (event: string, data: AgentStreamEvent | { error?: string }) => void,
    signal?: AbortSignal,
  ) => postSSE(`/api/v2/agent/sessions/${id}/messages`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ message }),
    signal,
  }, onEvent),
}
