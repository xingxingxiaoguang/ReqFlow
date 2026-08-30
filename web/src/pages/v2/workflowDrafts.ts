import type { DefinitionInput } from '../../api/v2/catalog'

const STORAGE_KEY = 'reqflow.v2.workflow-drafts.v1'

export interface LocalWorkflowDraft {
  id: string
  created_at: string
  saved_at: string
  definition: DefinitionInput
}

function readStorage(): LocalWorkflowDraft[] {
  try {
    const raw = window.localStorage.getItem(STORAGE_KEY)
    if (!raw) return []
    const value = JSON.parse(raw) as unknown
    if (!Array.isArray(value)) return []
    return value.filter((item): item is LocalWorkflowDraft => Boolean(
      item && typeof item === 'object' &&
      typeof (item as LocalWorkflowDraft).id === 'string' &&
      typeof (item as LocalWorkflowDraft).saved_at === 'string' &&
      (item as LocalWorkflowDraft).definition?.key,
    ))
  } catch {
    return []
  }
}

export function listLocalWorkflowDrafts() {
  return readStorage().sort((a, b) => b.saved_at.localeCompare(a.saved_at))
}

export function getLocalWorkflowDraft(id: string) {
  return readStorage().find((item) => item.id === id)
}

export function saveLocalWorkflowDraft(id: string, definition: DefinitionInput, createdAt?: string) {
  const drafts = readStorage()
  const existing = drafts.find((item) => item.id === id)
  const now = new Date().toISOString()
  const saved: LocalWorkflowDraft = {
    id,
    created_at: existing?.created_at ?? createdAt ?? now,
    saved_at: now,
    definition: structuredClone({ ...definition, status: 'draft' as const }),
  }
  const next = [saved, ...drafts.filter((item) => item.id !== id)]
  window.localStorage.setItem(STORAGE_KEY, JSON.stringify(next))
  return saved
}

export function removeLocalWorkflowDraft(id: string) {
  window.localStorage.setItem(STORAGE_KEY, JSON.stringify(readStorage().filter((item) => item.id !== id)))
}
