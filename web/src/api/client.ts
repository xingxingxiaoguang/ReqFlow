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

/**
 * 带载荷的失败响应：守卫拦截（HTTP 409）时后端把判定明细放在 data 字段带回
 * （兼容检查报告/影响面），本变体不抛错而是返回 { ok:false, status, error, data }。
 */
export async function requestWithDetail<T>(path: string, init?: RequestInit): Promise<{
  ok: boolean; status: number; data?: T; error?: string
}> {
  const resp = await fetch(path, {
    ...init,
    headers: { 'Content-Type': 'application/json', ...(init?.headers as Record<string, string> | undefined) },
  })
  const body = (await resp.json().catch(() => null)) as ApiResponse<T> | null
  if (!resp.ok || !body?.success) {
    return { ok: false, status: resp.status, error: body?.error || `请求失败（HTTP ${resp.status}）`, data: body?.data }
  }
  return { ok: true, status: resp.status, data: body.data as T }
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
  put: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: 'PUT', body: body === undefined ? undefined : JSON.stringify(body) }),
  putDetail: <T>(path: string, body?: unknown) =>
    requestWithDetail<T>(path, { method: 'PUT', body: body === undefined ? undefined : JSON.stringify(body) }),
  del: <T>(path: string) => request<T>(path, { method: 'DELETE' }),
}
