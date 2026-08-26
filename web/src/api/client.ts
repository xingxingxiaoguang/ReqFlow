import type { ApiResponse } from './types'

/** 统一 JSON GET/POST/PATCH 封装（FormData 时由浏览器自动设置 multipart 边界） */
export async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const isForm = init?.body instanceof FormData
  const resp = await fetch(path, {
    ...init,
    headers: {
      ...(isForm ? {} : { 'Content-Type': 'application/json' }),
      ...(init?.headers as Record<string, string> | undefined),
    },
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
    request<T>(path, {
      method: 'POST',
      body: body === undefined ? undefined : body instanceof FormData ? body : JSON.stringify(body),
    }),
  patch: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: 'PATCH', body: JSON.stringify(body) }),
}
