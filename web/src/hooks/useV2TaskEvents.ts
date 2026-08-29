import { useEffect, useRef, useState } from 'react'
import { App } from 'antd'
import { useQueryClient } from '@tanstack/react-query'
import { getSSE } from '../api/sse'
import type { V2TaskSnapshot } from '../api/v2/types'

export type V2StreamStatus = 'connecting' | 'connected' | 'reconnecting'

/** V2 Task 使用 GET SSE。事件只负责让本地读模型收敛到服务端完整快照，数据库仍是事实源。 */
export function useV2TaskEvents(taskId: string | undefined): V2StreamStatus {
  const queryClient = useQueryClient()
  const { message } = App.useApp()
  const [status, setStatus] = useState<V2StreamStatus>('connecting')
  const reconnectTimer = useRef<ReturnType<typeof setTimeout>>()

  useEffect(() => {
    if (!taskId) return
    const controller = new AbortController()

    const connect = () => {
      if (controller.signal.aborted) return
      setStatus((current) => current === 'connected' ? 'reconnecting' : current)
      getSSE(`/api/v2/tasks/${taskId}/events`, (event, payload) => {
        if (event === 'snapshot' && payload?.data) {
          setStatus('connected')
          queryClient.setQueryData<V2TaskSnapshot>(['v2-task', taskId], payload.data)
          queryClient.invalidateQueries({ queryKey: ['v2-tasks'] })
        } else if (event === 'error') {
          message.error(payload?.error ?? 'V2 任务事件流异常')
        }
      }, controller.signal).catch((error: unknown) => {
        if (!controller.signal.aborted && (error as Error).name !== 'AbortError') {
          setStatus('reconnecting')
        }
      }).finally(() => {
        if (!controller.signal.aborted) {
          reconnectTimer.current = setTimeout(connect, 3000)
        }
      })
    }

    connect()
    return () => {
      controller.abort()
      if (reconnectTimer.current) clearTimeout(reconnectTimer.current)
    }
  }, [message, queryClient, taskId])

  return status
}
