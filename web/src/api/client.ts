/** 统一 JSON 请求封装（FormData 时由浏览器自动设置 multipart 边界） */
export interface ApiResponse<T> {
  success: boolean
  data?: T
  error?: string | { message?: string; code?: string }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
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
    const error = typeof body?.error === 'string' ? body.error : body?.error?.message
    throw new Error(error || `请求失败（HTTP ${resp.status}）`)
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
  put: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: 'PUT', body: body === undefined ? undefined : JSON.stringify(body) }),
  del: <T>(path: string) => request<T>(path, { method: 'DELETE' }),
}
