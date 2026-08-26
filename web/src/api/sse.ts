/**
 * POST SSE 消费器：fetch + ReadableStream 手写解析（后端分析/同步/导入进度均走 POST SSE）。
 * 按空行拆帧，每帧内解析 event:/data: 行，回调 onEvent(event, dataJSON)。
 */
export type SSEEventHandler = (event: string, data: any) => void

export async function postSSE(
  path: string,
  init: RequestInit,
  onEvent: SSEEventHandler,
): Promise<void> {
  const resp = await fetch(path, {
    ...init,
    headers: { Accept: 'text/event-stream', ...(init.headers as Record<string, string>) },
  })
  if (!resp.ok || !resp.body) {
    const text = await resp.text().catch(() => '')
    let msg = `HTTP ${resp.status}`
    try {
      msg = JSON.parse(text)?.error || msg
    } catch { /* 非 JSON 错误体 */ }
    throw new Error(msg)
  }

  const reader = resp.body.getReader()
  const decoder = new TextDecoder('utf-8')
  let buffer = ''

  const dispatchFrame = (frame: string) => {
    let event = 'message'
    let data = ''
    for (const line of frame.split('\n')) {
      if (line.startsWith('event:')) event = line.slice(6).trim()
      else if (line.startsWith('data:')) data += line.slice(5).trim()
    }
    if (!data) return
    let parsed: any = data
    try {
      parsed = JSON.parse(data)
    } catch { /* 保留原文 */ }
    // 单帧解析/回调异常只丢该帧，绝不中断整个事件流（否则连接秒死、UI 永久失联）
    try {
      onEvent(event, parsed)
    } catch (e) {
      console.error('[SSE] 事件处理失败（已跳过该帧）:', event, e)
    }
  }

  for (;;) {
    const { done, value } = await reader.read()
    if (done) break
    buffer += decoder.decode(value, { stream: true })
    let sep: number
    while ((sep = buffer.indexOf('\n\n')) >= 0) {
      const frame = buffer.slice(0, sep)
      buffer = buffer.slice(sep + 2)
      dispatchFrame(frame)
    }
  }
  if (buffer.trim()) dispatchFrame(buffer)
}
