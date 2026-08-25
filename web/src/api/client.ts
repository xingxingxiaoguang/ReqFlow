import type { ApiResponse } from './types'

/** 统一 JSON GET/POST 封装 */
export async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const resp = await fetch(path, {
    headers: { 'Content-Type': 'application/json' },
    ...init,
  })
  const body = (await resp.json().catch(() => null)) as ApiResponse<T> | null
  if (!resp.ok || !body?.success) {
    throw new Error(body?.error || `请求失败（HTTP ${resp.status}）`)
  }
  return body.data as T
}

export const api = {
  get: <T>(path: string) => request<T>(path),
  post: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: 'POST', body: body === undefined ? undefined : JSON.stringify(body) }),
}
