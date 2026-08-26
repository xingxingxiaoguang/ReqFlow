import { useEffect, useRef, useState } from 'react'
import { App } from 'antd'
import { useQueryClient } from '@tanstack/react-query'
import { postSSE } from '../api/sse'
import type {
  Task, TaskDetail, TaskStep, TaskItem, ToolEvent, ToolTrace, TokenEvent,
} from '../api/types'

const TAIL_LEN = 3000

/**
 * 任务事件流订阅（POST /tasks/:id/events）：
 * - snapshot 整包写入 react-query 缓存（['task', id]）；task/step/items 事件浅合并补丁；
 * - token/progress/tool_trace 属瞬时流，只进页面本地 state，不落缓存（重连后工具轨迹从
 *   step.data 回放，token 尾区从空开始——服务端不落库 token，避免会话膨胀）；
 * - 页面卸载即 abort（服务端退订），任务照跑，重进页面重新订阅收快照。
 */
export interface TaskStream {
  thinkingTail: string
  answerTail: string
  answerCount: number
  phase: 'thinking' | 'answer' | 'idle'
  elapsedSec: number
  analyzeMessage: string
  toolTrace: ToolTrace[]
  importProgress: { current: number; total: number; lastTitle?: string; lastStatus?: string }
  importLog: { title: string; status: string }[]
}

export function useTaskEvents(taskId: string | undefined): TaskStream {
  const qc = useQueryClient()
  const { message } = App.useApp()
  const [stream, setStream] = useState<TaskStream>({
    thinkingTail: '', answerTail: '', answerCount: 0, phase: 'idle',
    elapsedSec: 0, analyzeMessage: '', toolTrace: [],
    importProgress: { current: 0, total: 0 }, importLog: [],
  })
  const streamRef = useRef(stream)
  streamRef.current = stream

  useEffect(() => {
    if (!taskId) return
    const controller = new AbortController()
    let elapsedTimer: ReturnType<typeof setInterval> | undefined

    const patch = (fn: (s: TaskStream) => TaskStream) => setStream((s) => fn(s))
    const stopElapsed = () => {
      if (elapsedTimer) { clearInterval(elapsedTimer); elapsedTimer = undefined }
    }

    // 断线自动重连（后端重启/网络抖动）：非主动断开 3s 退避重连，重连重收快照恢复完整状态
    const connect = () => {
      if (controller.signal.aborted) return
      postSSE(`/api/tasks/${taskId}/events`, { method: 'POST', signal: controller.signal }, (event, data) => {
      const payload = data?.data
      switch (event) {
        case 'snapshot': {
          qc.setQueryData<TaskDetail>(['task', taskId], { task: payload.task, steps: payload.steps, items: payload.items })
          // 已暂停/终态的任务不再有实时流
          if (payload.task?.Status !== 'running') stopElapsed()
          return
        }
        case 'task': {
          qc.setQueryData<TaskDetail>(['task', taskId], (old) => (old ? { ...old, task: payload as Task } : old))
          if (payload?.Status !== 'running') stopElapsed()
          if (payload?.Status === 'succeeded' || payload?.Status === 'failed') {
            qc.invalidateQueries({ queryKey: ['task', taskId] })
            qc.invalidateQueries({ queryKey: ['tasks'] })
          }
          return
        }
        case 'step': {
          const step = payload as TaskStep
          qc.setQueryData<TaskDetail>(['task', taskId], (old) => {
            if (!old) return old
            const steps = old.steps.map((s) => (s.ID === step.ID ? step : s))
            return { ...old, steps }
          })
          return
        }
        case 'items': {
          qc.setQueryData<TaskDetail>(['task', taskId], (old) => (old ? { ...old, items: payload as TaskItem[] } : old))
          return
        }
        case 'token': {
          const t = payload as TokenEvent
          patch((s) => {
            const tail = t.phase === 'thinking' ? 'thinkingTail' : 'answerTail'
            const prev = s[tail]
            const next = (prev + t.delta).slice(-TAIL_LEN)
            return {
              ...s,
              phase: t.phase,
              thinkingTail: t.phase === 'thinking' ? next : s.thinkingTail,
              answerTail: t.phase === 'answer' ? next : s.answerTail,
              answerCount: t.phase === 'answer' ? countTitles(next) : s.answerCount,
            }
          })
          return
        }
        case 'tool_trace': {
          patch((s) => ({ ...s, toolTrace: applyToolEvent(s.toolTrace, payload as ToolEvent) }))
          return
        }
        case 'progress': {
          patch((s) => {
            if (typeof payload?.current === 'number') {
              return {
                ...s,
                importProgress: {
                  current: payload.current, total: payload.total ?? s.importProgress.total,
                  lastTitle: payload.title ?? s.importProgress.lastTitle,
                  lastStatus: payload.status ?? s.importProgress.lastStatus,
                },
                importLog: payload.title
                  ? [...s.importLog, { title: payload.title, status: payload.status }]
                  : s.importLog,
              }
            }
            if (payload?.message) return { ...s, analyzeMessage: payload.message }
            return s
          })
          return
        }
        case 'error': {
          message.error(payload?.message ?? '任务执行出错')
          qc.invalidateQueries({ queryKey: ['task', taskId] })
          return
        }
        }
      }).finally(() => {
        // 流结束（服务端断开/网络中断/abort）：非主动断开则定时重连
        if (!controller.signal.aborted) setTimeout(connect, 3000)
      })
    }
    connect()

    elapsedTimer = setInterval(() => {
      patch((s) => ({ ...s, elapsedSec: s.elapsedSec + 1 }))
    }, 1000)

    return () => {
      stopElapsed()
      controller.abort()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [taskId])

  return stream
}

/** 从正文流尾部按 "title": 出现次数实时计数（对齐 PingCraft 的轻量做法） */
function countTitles(answerTail: string): number {
  const m = answerTail.match(/"title"\s*:/g)
  return m ? m.length : 0
}

/** tool 事件归并进轨迹：start 入列、end 回写最近一条同 call_id 条目的终态 */
export function applyToolEvent(trace: ToolTrace[], ev: ToolEvent): ToolTrace[] {
  if (ev.phase === 'start') {
    const next = [...trace, { callId: ev.call_id, name: ev.name, args: ev.args, status: 'running' as const }]
    return next.length > 50 ? next.slice(next.length - 50) : next
  }
  const next = [...trace]
  for (let i = next.length - 1; i >= 0; i--) {
    if (next[i].callId === ev.call_id) {
      next[i] = { ...next[i], status: ev.is_error ? 'error' : 'done', details: ev.details }
      break
    }
  }
  return next
}

/** 从步骤 data（JSON 数组的 ToolEvent）重建轨迹（重放/刷新后回看） */
export function traceFromStepData(data: string): ToolTrace[] {
  if (!data) return []
  try {
    const events = JSON.parse(data) as ToolEvent[]
    return events.reduce<ToolTrace[]>((acc, ev) => applyToolEvent(acc, ev), [])
  } catch {
    return []
  }
}
