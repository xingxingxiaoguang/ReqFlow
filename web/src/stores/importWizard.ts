import { create } from 'zustand'
import { postSSE } from '../api/sse'
import type { DraftItem, DuplicateResult, ProjectMatch } from '../api/types'

/**
 * 需求导入向导的跨页临时态。
 * 流程：upload（解析）→ review（确认门）→ analyzing（LLM 流式）→ result（匹配/查重/导入）。
 * recordId 进入 URL；刷新后通过 /api/records/:id 恢复，store 只承载流式过程态。
 */
interface ImportWizardState {
  /* 解析确认门 */
  fileName: string
  parsedText: string
  specialRequirements: string
  parsing: boolean
  parseMessage: string

  /* LLM 流式分析 */
  analyzing: boolean
  analyzeMessage: string
  elapsedSec: number
  phase: 'thinking' | 'answer' | 'idle'
  thinkingTail: string // 思考流尾部（滑动窗口）
  answerTail: string // 正文流尾部（滑动窗口）
  answerCount: number // 已生成条目实时计数

  /* 结果态 */
  recordId?: string
  items: DraftItem[]
  matches: ProjectMatch[]
  selectedProjectId?: string
  dupResults: DuplicateResult[]
  importing: boolean
  importProgress: { current: number; total: number; lastTitle?: string; lastStatus?: string }
  importDone?: { success: number; failed: number }

  /* actions */
  setField: (patch: Partial<ImportWizardState>) => void
  uploadAndParse: (file: File) => Promise<void>
  startAnalyze: () => Promise<void>
  reset: () => void
  restoreFromRecord: (recordId: string, items: DraftItem[]) => void
}

const TAIL_LEN = 3000

export const useImportWizard = create<ImportWizardState>((set, get) => ({
  fileName: '',
  parsedText: '',
  specialRequirements: '',
  parsing: false,
  parseMessage: '',

  analyzing: false,
  analyzeMessage: '',
  elapsedSec: 0,
  phase: 'idle',
  thinkingTail: '',
  answerTail: '',
  answerCount: 0,

  items: [],
  matches: [],
  dupResults: [],
  importing: false,
  importProgress: { current: 0, total: 0 },

  setField: (patch) => set(patch),

  uploadAndParse: async (file) => {
    set({ parsing: true, parseMessage: '文件已上传，正在解析文档内容…', fileName: file.name })
    const form = new FormData()
    form.append('file', file)
    try {
      await postSSE('/api/parse', { method: 'POST', body: form }, (event, data) => {
        if (event === 'progress') set({ parseMessage: data.message })
        else if (event === 'parsed') {
          set({ parsedText: data.text, fileName: data.file_name, parsing: false })
        } else if (event === 'error') {
          set({ parsing: false, parseMessage: '' })
          throw new Error(data.message)
        }
      })
    } finally {
      set({ parsing: false })
    }
  },

  startAnalyze: async () => {
    const { parsedText, fileName, specialRequirements } = get()
    set({
      analyzing: true, phase: 'idle',
      thinkingTail: '', answerTail: '', answerCount: 0, elapsedSec: 0,
      analyzeMessage: 'AI 正在拆解需求功能点…',
    })
    try {
      await postSSE(
        '/api/analyze',
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            text: parsedText,
            file_name: fileName,
            special_requirements: specialRequirements,
          }),
        },
        (event, data) => {
          const s = get()
          if (event === 'progress') {
            set({ analyzeMessage: data.message, elapsedSec: data.elapsedSec ?? s.elapsedSec })
          } else if (event === 'token') {
            const tail = data.phase === 'thinking' ? 'thinkingTail' : 'answerTail'
              const prev = (s as any)[tail] as string
              const next = (prev + data.delta).slice(-TAIL_LEN)
              set({
                phase: data.phase,
                [tail]: next,
                answerCount: data.phase === 'answer' ? countTitles(next) : s.answerCount,
              } as any)
          } else if (event === 'complete') {
            set({
              analyzing: false,
              recordId: data.record_id,
              items: data.items as DraftItem[],
              matches: [], dupResults: [], selectedProjectId: undefined,
              importDone: undefined,
            })
          } else if (event === 'error') {
            set({ analyzing: false, analyzeMessage: data.message })
            throw new Error(data.message)
          }
        },
      )
    } catch (e) {
      set({ analyzing: false })
      throw e
    }
  },

  reset: () =>
    set({
      fileName: '', parsedText: '', specialRequirements: '', parsing: false, parseMessage: '',
      analyzing: false, analyzeMessage: '', elapsedSec: 0, phase: 'idle',
      thinkingTail: '', answerTail: '', answerCount: 0,
      recordId: undefined, items: [], matches: [], selectedProjectId: undefined,
      dupResults: [], importing: false, importProgress: { current: 0, total: 0 },
      importDone: undefined,
    }),

  restoreFromRecord: (recordId, items) =>
    set({
      recordId,
      items,
      fileName: '（历史记录恢复）',
      parsedText: '',
      matches: [], dupResults: [], selectedProjectId: undefined,
      importing: false, importProgress: { current: 0, total: items.length },
      importDone: undefined,
    }),
}))

/** 从正文流尾部按 "title": 出现次数实时计数（对齐 PingCraft 的轻量做法） */
function countTitles(answerTail: string): number {
  const m = answerTail.match(/"title"\s*:/g)
  return m ? m.length : 0
}
