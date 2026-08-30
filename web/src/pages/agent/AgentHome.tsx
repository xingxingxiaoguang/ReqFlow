import {
  ArrowRightOutlined, BranchesOutlined, CheckCircleFilled, ClockCircleOutlined,
  DatabaseOutlined, ExclamationCircleFilled, HistoryOutlined, LoadingOutlined,
  PlusOutlined, RobotOutlined, SearchOutlined, SendOutlined, StopOutlined,
  ThunderboltFilled, ToolOutlined, UnorderedListOutlined,
} from '@ant-design/icons'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Avatar, Button, Empty, Input, Spin, Tooltip, Typography } from 'antd'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import {
  v2AgentApi, type AgentMessage, type AgentSession, type AgentSessionSummary,
  type AgentStreamEvent,
} from '../../api/v2/agent'
import './agent-home.css'

const { Text, Title } = Typography
const { TextArea } = Input

const quickPrompts = [
  { icon: <BranchesOutlined />, label: '设计一个流程', prompt: '帮我根据目标设计并创建一个新的业务流程。先查询现有流程避免重复，再告诉我还需要哪些信息。' },
  { icon: <UnorderedListOutlined />, label: '查看运行任务', prompt: '查询当前正在运行和等待处理的任务，按优先级告诉我需要关注什么。' },
  { icon: <SearchOutlined />, label: '查询平台数据', prompt: '先列出平台里可查询的数据集和索引，帮助我选择要分析的数据。' },
  { icon: <DatabaseOutlined />, label: '建立数据索引', prompt: '查询还没有可用检索索引的数据集，并帮助我为合适的数据集建立索引。' },
]

const toolMeta: Record<string, { label: string; group: string; path: string }> = {
  list_workflows: { label: '查询流程', group: '流程工具', path: '/definitions' },
  create_workflow: { label: '创建流程', group: '流程工具', path: '/definitions' },
  list_tasks: { label: '查询任务', group: '任务工具', path: '/tasks' },
  create_task: { label: '创建任务', group: '任务工具', path: '/tasks' },
  run_task: { label: '运行任务', group: '任务工具', path: '/tasks' },
  query_data: { label: '查询数据', group: '数据工具', path: '/datasets' },
  index_dataset: { label: '建立索引', group: '数据工具', path: '/datasets' },
}

interface LiveTool {
  id: string
  name: string
  status: 'running' | 'done' | 'error'
  details?: string
  output?: string
}

const messageText = (message: AgentMessage) => (message.content ?? [])
  .filter((block) => block.type === 'text')
  .map((block) => block.text ?? '')
  .join('')

const messageThinking = (message: AgentMessage) => (message.content ?? [])
  .filter((block) => block.type === 'thinking')
  .map((block) => block.thinking ?? '')
  .join('')

const formatRelative = (value: string) => {
  const elapsed = Date.now() - new Date(value).getTime()
  if (elapsed < 60_000) return '刚刚'
  if (elapsed < 3_600_000) return `${Math.floor(elapsed / 60_000)} 分钟前`
  if (elapsed < 86_400_000) return `${Math.floor(elapsed / 3_600_000)} 小时前`
  return new Date(value).toLocaleDateString('zh-CN', { month: 'numeric', day: 'numeric' })
}

export default function AgentHome() {
  const { sessionId } = useParams()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const [session, setSession] = useState<AgentSession | null>(null)
  const [draft, setDraft] = useState('')
  const [running, setRunning] = useState(false)
  const [liveText, setLiveText] = useState('')
  const [thinking, setThinking] = useState('')
  const [liveTools, setLiveTools] = useState<LiveTool[]>([])
  const [streamError, setStreamError] = useState('')
  const abortRef = useRef<AbortController | null>(null)
  const bottomRef = useRef<HTMLDivElement | null>(null)

  const sessionsQuery = useQuery({
    queryKey: ['agent-sessions'],
    queryFn: v2AgentApi.listSessions,
  })
  const sessionQuery = useQuery({
    queryKey: ['agent-session', sessionId],
    queryFn: () => v2AgentApi.getSession(sessionId!),
    enabled: Boolean(sessionId),
  })

  useEffect(() => {
    if (sessionQuery.data?.session && !running) setSession(sessionQuery.data.session)
  }, [sessionQuery.data, running])

  useEffect(() => {
    if (!sessionId) {
      setSession(null)
      setStreamError('')
      setLiveText('')
      setThinking('')
      setLiveTools([])
    }
  }, [sessionId])

  useEffect(() => {
    if (sessionId && session?.id !== sessionId && !running) setSession(null)
  }, [sessionId])

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: running ? 'smooth' : 'auto', block: 'end' })
  }, [session?.messages.length, liveText, thinking, liveTools, running])

  useEffect(() => () => abortRef.current?.abort(), [])

  const displayMessages = useMemo(() => (session?.messages ?? []).filter((message) => {
    if (message.role === 'user' || message.role === 'toolResult') return true
    return Boolean(messageText(message) || messageThinking(message))
  }), [session?.messages])

  const startNew = () => {
    abortRef.current?.abort()
    setSession(null)
    setDraft('')
    setRunning(false)
    setStreamError('')
    navigate('/agent')
  }

  const send = async (preset?: string) => {
    const content = (preset ?? draft).trim()
    if (!content || running) return
    setDraft('')
    setStreamError('')
    setLiveText('')
    setThinking('')
    setLiveTools([])
    setRunning(true)

    let target = session
    try {
      if (!target) {
        const created = await v2AgentApi.createSession()
        target = created.session
        setSession(target)
        navigate(`/agent/${target.id}`, { replace: true })
      }
      const optimisticUser: AgentMessage = {
        role: 'user', content: [{ type: 'text', text: content }], timestamp: Date.now(),
      }
      setSession((current) => current
        ? { ...current, messages: [...(current.messages ?? []), optimisticUser], message_count: current.message_count + 1 }
        : current)

      const controller = new AbortController()
      abortRef.current = controller
      await v2AgentApi.sendMessage(target.id, content, (eventName, payload) => {
        if (eventName === 'error') {
          setStreamError('error' in payload ? payload.error ?? '回答失败' : '回答失败')
          return
        }
        const event = payload as AgentStreamEvent
        if (event.type === 'started' && event.session) setSession(event.session)
        if (event.type === 'assistant_delta') setLiveText((value) => value + (event.delta ?? ''))
        if (event.type === 'thinking_delta') setThinking((value) => value + (event.delta ?? ''))
        if (event.type === 'tool_start' && event.tool_call_id && event.tool_name) {
          setLiveTools((tools) => [...tools, { id: event.tool_call_id!, name: event.tool_name!, status: 'running' }])
        }
        if (event.type === 'tool_end' && event.tool_call_id) {
          setLiveTools((tools) => tools.map((tool) => tool.id === event.tool_call_id
            ? { ...tool, status: event.is_error ? 'error' : 'done', details: event.details, output: event.output }
            : tool))
        }
        if (event.type === 'done' && event.session) {
          setSession(event.session)
          if (event.session.last_error) setStreamError(event.session.last_error)
        }
      }, controller.signal)
    } catch (error) {
      if ((error as Error).name !== 'AbortError') setStreamError((error as Error).message || '回答失败')
    } finally {
      abortRef.current = null
      setRunning(false)
      setLiveText('')
      setThinking('')
      setLiveTools([])
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['agent-sessions'] }),
        target ? queryClient.invalidateQueries({ queryKey: ['agent-session', target.id] }) : Promise.resolve(),
      ])
    }
  }

  const stop = () => abortRef.current?.abort()

  return (
    <div className="agent-home">
      <aside className="agent-history">
        <div className="agent-history-head">
          <div>
            <Text className="agent-eyebrow">REQFLOW AGENT</Text>
            <Title level={4}>数字大脑</Title>
          </div>
          <Tooltip title="新会话"><Button type="text" icon={<PlusOutlined />} onClick={startNew} /></Tooltip>
        </div>
        <Button className="agent-new-button" icon={<PlusOutlined />} onClick={startNew}>开始新任务</Button>
        <div className="agent-history-label"><HistoryOutlined /> 最近会话</div>
        <div className="agent-session-list">
          {sessionsQuery.isLoading && <Spin size="small" />}
          {!sessionsQuery.isLoading && !(sessionsQuery.data?.sessions.length) && (
            <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="还没有会话" />
          )}
          {sessionsQuery.data?.sessions.map((item) => (
            <SessionItem key={item.id} item={item} active={item.id === sessionId}
              onClick={() => !running && navigate(`/agent/${item.id}`)} />
          ))}
        </div>
        <div className="agent-capability-note">
          <ThunderboltFilled />
          <span>连接 ReqFlow 全部业务能力<br /><small>7 个默认工具已就绪</small></span>
        </div>
      </aside>

      <main className="agent-conversation">
        <header className="agent-topbar">
          <div className="agent-identity">
            <Avatar className="agent-avatar" icon={<RobotOutlined />} />
            <div><Text strong>ReqFlow Agent</Text><span><i /> 平台数字大脑</span></div>
          </div>
          <div className="agent-tool-summary">
            <ToolOutlined /> 流程 · 任务 · 数据 <b>7</b>
          </div>
        </header>

        <section className="agent-thread">
          {sessionQuery.isLoading && sessionId && !session && <div className="agent-loading"><Spin /></div>}
          {!sessionId && !session ? (
            <Welcome onPrompt={send} />
          ) : (
            <div className="agent-messages">
              {displayMessages.map((message, index) => (
                <MessageView key={`${message.timestamp ?? index}-${index}`} message={message} />
              ))}
              {running && <LiveResponse text={liveText} thinking={thinking} tools={liveTools} />}
              {streamError && <div className="agent-error"><ExclamationCircleFilled /> {streamError}</div>}
              <div ref={bottomRef} />
            </div>
          )}
        </section>

        <footer className="agent-composer-wrap">
          <div className={`agent-composer ${running ? 'is-running' : ''}`}>
            <TextArea
              value={draft}
              onChange={(event) => setDraft(event.target.value)}
              onPressEnter={(event) => {
                if (!event.shiftKey && !event.nativeEvent.isComposing) {
                  event.preventDefault()
                  void send()
                }
              }}
              disabled={running}
              autoSize={{ minRows: 1, maxRows: 6 }}
              placeholder="告诉数字大脑你想完成什么，也可以直接查询平台数据…"
              variant="borderless"
            />
            {running ? (
              <Button className="agent-send stop" shape="circle" icon={<StopOutlined />} onClick={stop} />
            ) : (
              <Button className="agent-send" type="primary" shape="circle" icon={<SendOutlined />}
                disabled={!draft.trim()} onClick={() => void send()} />
            )}
          </div>
          <Text className="agent-composer-hint">Enter 发送 · Shift + Enter 换行 · 执行动作会保留在会话记录中</Text>
        </footer>
      </main>
    </div>
  )
}

function SessionItem({ item, active, onClick }: { item: AgentSessionSummary; active: boolean; onClick: () => void }) {
  return <button className={`agent-session-item ${active ? 'active' : ''}`} onClick={onClick}>
    <span className="agent-session-title">{item.title}</span>
    <span className="agent-session-meta">
      {item.status === 'running' && <LoadingOutlined spin />}
      {item.status === 'error' && <ExclamationCircleFilled className="error" />}
      {item.status === 'idle' && <ClockCircleOutlined />}
      {formatRelative(item.updated_at)}
    </span>
  </button>
}

function Welcome({ onPrompt }: { onPrompt: (prompt: string) => void }) {
  return <div className="agent-welcome">
    <div className="agent-orb"><RobotOutlined /><span /><span /></div>
    <Text className="agent-welcome-kicker">REQFLOW PLATFORM INTELLIGENCE</Text>
    <Title>今天想让数字大脑<br />帮你完成什么？</Title>
    <Text className="agent-welcome-subtitle">它不仅回答问题，还会调用平台能力创建流程、运行任务、查询和分析真实数据。</Text>
    <div className="agent-quick-grid">
      {quickPrompts.map((item) => <button key={item.label} onClick={() => onPrompt(item.prompt)}>
        <span className="quick-icon">{item.icon}</span>
        <span><b>{item.label}</b><small>{item.prompt.slice(0, 25)}…</small></span>
        <ArrowRightOutlined />
      </button>)}
    </div>
    <div className="agent-tools-strip">
      <span><BranchesOutlined /> 流程 <b>查 · 增</b></span>
      <span><UnorderedListOutlined /> 任务 <b>查 · 增 · 运行</b></span>
      <span><DatabaseOutlined /> 数据 <b>查询 · 索引</b></span>
    </div>
  </div>
}

function MessageView({ message }: { message: AgentMessage }) {
  if (message.role === 'toolResult') return <ToolResult message={message} />
  const text = messageText(message)
  const thought = messageThinking(message)
  if (!text && !thought) return null
  if (message.role === 'user') return <div className="agent-message user"><div>{text}</div></div>
  return <div className="agent-message assistant">
    <Avatar className="message-avatar" icon={<RobotOutlined />} />
    <div className="message-body">
      {thought && <details className="agent-thinking"><summary>已完成思考</summary><pre>{thought}</pre></details>}
      {text && <MarkdownAnswer text={text} />}
    </div>
  </div>
}

function ToolResult({ message }: { message: AgentMessage }) {
  const meta = toolMeta[message.tool_name ?? ''] ?? { label: message.tool_name || '平台工具', group: '工具', path: '/' }
  const navigate = useNavigate()
  return <div className={`tool-trace ${message.is_error ? 'error' : 'done'}`}>
    <span className="tool-status">{message.is_error ? <ExclamationCircleFilled /> : <CheckCircleFilled />}</span>
    <div><Text strong>{meta.label}</Text><small>{message.details || (message.is_error ? message.result : meta.group)}</small></div>
    {!message.is_error && <Button type="link" size="small" onClick={() => navigate(meta.path)}>查看</Button>}
  </div>
}

function LiveResponse({ text, thinking, tools }: { text: string; thinking: string; tools: LiveTool[] }) {
  return <div className="agent-message assistant live">
    <Avatar className="message-avatar pulse" icon={<RobotOutlined />} />
    <div className="message-body">
      {thinking && <details className="agent-thinking" open={!text}><summary><LoadingOutlined /> 正在思考</summary><pre>{thinking}</pre></details>}
      {tools.map((tool) => {
        const meta = toolMeta[tool.name] ?? { label: tool.name, group: '平台工具', path: '/' }
        return <div className={`tool-trace ${tool.status}`} key={tool.id}>
          <span className="tool-status">{tool.status === 'running' ? <LoadingOutlined spin />
            : tool.status === 'error' ? <ExclamationCircleFilled /> : <CheckCircleFilled />}</span>
          <div><Text strong>{meta.label}</Text><small>{tool.details || (tool.status === 'running' ? '正在调用平台能力…' : meta.group)}</small></div>
        </div>
      })}
      {text ? <div className="agent-answer live-answer"><ReactMarkdown remarkPlugins={[remarkGfm]}>{text}</ReactMarkdown><i className="typing-caret" /></div>
        : !thinking && tools.length === 0 && <div className="agent-working"><LoadingOutlined /> 正在理解你的任务…</div>}
    </div>
  </div>
}

function MarkdownAnswer({ text }: { text: string }) {
  return <div className="agent-answer"><ReactMarkdown remarkPlugins={[remarkGfm]}>{text}</ReactMarkdown></div>
}
