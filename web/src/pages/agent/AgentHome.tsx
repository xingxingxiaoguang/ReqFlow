import {
  ArrowRightOutlined, BranchesOutlined, CheckCircleFilled, ClockCircleOutlined,
  DatabaseOutlined, ExclamationCircleFilled, HistoryOutlined, LoadingOutlined,
  PlusOutlined, RobotOutlined, SearchOutlined, SendOutlined, StopOutlined,
  SettingOutlined, ThunderboltFilled, ToolOutlined, UnorderedListOutlined,
} from '@ant-design/icons'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { App, Avatar, Button, Drawer, Empty, Form, Input, Modal, Spin, Switch, Tag, Tooltip, Typography } from 'antd'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import {
  v2AgentApi, type AgentMessage, type AgentSession, type AgentSessionSummary, type AgentSkill,
  type AgentStreamEvent, type CreateAgentSkillInput,
} from '../../api/v2/agent'
import './agent-home.css'

const { Text, Title } = Typography
const { TextArea } = Input

const quickPrompts = [
  { icon: <BranchesOutlined />, label: '设计一个流程', prompt: '我想把一个重复的业务环节搬上平台。请先了解我的业务 SOP，再按平台使用规则给我一步步的流程搭建指引。' },
  { icon: <UnorderedListOutlined />, label: '查看运行任务', prompt: '查询当前正在运行和等待处理的任务，按优先级告诉我需要关注什么。' },
  { icon: <SearchOutlined />, label: '查询平台数据', prompt: '先列出平台里可查询的数据集和索引，帮助我选择要分析的数据。' },
  { icon: <DatabaseOutlined />, label: '建立数据索引', prompt: '查询还没有可用检索索引的数据集，并按平台使用规则指导我一步步为它建立索引。' },
]

const toolMeta: Record<string, { label: string; group: string; path?: string }> = {
  platform_guide: { label: '平台使用规则', group: '平台指南' },
  list_workflows: { label: '查询流程', group: '流程工具', path: '/definitions' },
  list_tasks: { label: '查询任务', group: '任务工具', path: '/tasks' },
  query_data: { label: '查询数据', group: '数据工具', path: '/datasets' },
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
  const { message } = App.useApp()
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
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [skillModalOpen, setSkillModalOpen] = useState(false)
  const [savingSetting, setSavingSetting] = useState('')
  const [creatingSkill, setCreatingSkill] = useState(false)
  const [slashIndex, setSlashIndex] = useState(0)
  const [slashDismissed, setSlashDismissed] = useState(false)
  const [skillForm] = Form.useForm<CreateAgentSkillInput>()
  const abortRef = useRef<AbortController | null>(null)
  const activeSessionIDRef = useRef<string | undefined>(sessionId)
  const streamSessionIDRef = useRef<string | null>(null)
  const streamGenerationRef = useRef(0)
  const bottomRef = useRef<HTMLDivElement | null>(null)

  const sessionsQuery = useQuery({
    queryKey: ['agent-sessions'],
    queryFn: v2AgentApi.listSessions,
    refetchInterval: (query) => query.state.data?.sessions.some((item) => item.status === 'running') ? 1000 : false,
  })
  const sessionQuery = useQuery({
    queryKey: ['agent-session', sessionId],
    queryFn: () => v2AgentApi.getSession(sessionId!),
    enabled: Boolean(sessionId),
    refetchInterval: (query) => query.state.data?.session.status === 'running' ? 1000 : false,
  })
  const configQuery = useQuery({
    queryKey: ['agent-config'],
    queryFn: v2AgentApi.getConfig,
  })

  useEffect(() => {
    activeSessionIDRef.current = sessionId
    if (streamSessionIDRef.current && streamSessionIDRef.current !== sessionId) {
      streamGenerationRef.current += 1
      abortRef.current?.abort()
      abortRef.current = null
      streamSessionIDRef.current = null
      setRunning(false)
      setStreamError('')
      setLiveText('')
      setThinking('')
      setLiveTools([])
    }
  }, [sessionId])

  useEffect(() => {
    const loaded = sessionQuery.data?.session
    if (!sessionId) {
      if (!running) setSession(null)
      return
    }
    if (!running && loaded?.id === sessionId) {
      setSession(loaded)
    } else if (!running && session?.id !== sessionId) {
      setSession(null)
    }
  }, [running, session?.id, sessionId, sessionQuery.data?.session])

  const queriedSession = sessionQuery.data?.session
  const visibleSession = session?.id === sessionId
    ? session
    : queriedSession?.id === sessionId ? queriedSession : null
  const isRunning = running || visibleSession?.status === 'running'

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: isRunning ? 'smooth' : 'auto', block: 'end' })
  }, [visibleSession?.messages.length, liveText, thinking, liveTools, isRunning])

  useEffect(() => () => abortRef.current?.abort(), [])

  const displayMessages = useMemo(() => (visibleSession?.messages ?? []).filter((message) => {
    if (message.role === 'user' || message.role === 'toolResult') return true
    return Boolean(messageText(message) || messageThinking(message))
  }), [visibleSession?.messages])

  const enabledSkills = useMemo(() => (configQuery.data?.config.skills ?? [])
    .filter((skill) => skill.enabled), [configQuery.data?.config.skills])
  const slashCandidates = useMemo(() => {
    if (slashDismissed || isRunning) return []
    const match = draft.match(/^\/([^\s]*)$/)
    if (!match) return []
    const query = match[1].toLowerCase()
    return enabledSkills.filter((skill) => !query
      || skill.slug.includes(query)
      || skill.title.toLowerCase().includes(query)
      || skill.description.toLowerCase().includes(query)).slice(0, 8)
  }, [draft, enabledSkills, isRunning, slashDismissed])

  useEffect(() => {
    setSlashIndex(0)
  }, [draft])

  const disconnectStream = () => {
    streamGenerationRef.current += 1
    abortRef.current?.abort()
    abortRef.current = null
    streamSessionIDRef.current = null
    setRunning(false)
    setLiveText('')
    setThinking('')
    setLiveTools([])
  }

  const startNew = () => {
    disconnectStream()
    activeSessionIDRef.current = undefined
    setSession(null)
    setDraft('')
    setStreamError('')
    navigate('/agent')
  }

  const openSession = (id: string) => {
    if (id === sessionId) return
    disconnectStream()
    activeSessionIDRef.current = id
    setSession(null)
    setStreamError('')
    navigate(`/agent/${id}`)
  }

  const send = async (preset?: string) => {
    const content = (preset ?? draft).trim()
    if (!content || isRunning) return
    const generation = streamGenerationRef.current + 1
    streamGenerationRef.current = generation
    setDraft('')
    setStreamError('')
    setLiveText('')
    setThinking('')
    setLiveTools([])
    setRunning(true)

    let target = visibleSession
    try {
      if (!target) {
        const created = await v2AgentApi.createSession()
        if (streamGenerationRef.current !== generation) return
        target = created.session
        activeSessionIDRef.current = target.id
        streamSessionIDRef.current = target.id
        setSession(target)
        navigate(`/agent/${target.id}`, { replace: true })
      }
      if (streamGenerationRef.current !== generation) return
      const targetID = target.id
      activeSessionIDRef.current = targetID
      streamSessionIDRef.current = targetID
      const optimisticUser: AgentMessage = {
        role: 'user', content: [{ type: 'text', text: content }], timestamp: Date.now(),
      }
      setSession((current) => current
        ? { ...current, messages: [...(current.messages ?? []), optimisticUser], message_count: current.message_count + 1 }
        : current)

      const controller = new AbortController()
      abortRef.current = controller
      await v2AgentApi.sendMessage(targetID, content, (eventName, payload) => {
        if (streamGenerationRef.current !== generation || activeSessionIDRef.current !== targetID) return
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
      if ((error as Error).name !== 'AbortError' && streamGenerationRef.current === generation) {
        setStreamError((error as Error).message || '回答失败')
      }
    } finally {
      if (streamGenerationRef.current === generation) {
        abortRef.current = null
        streamSessionIDRef.current = null
        setRunning(false)
        setLiveText('')
        setThinking('')
        setLiveTools([])
      }
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['agent-sessions'] }),
        target ? queryClient.invalidateQueries({ queryKey: ['agent-session', target.id] }) : Promise.resolve(),
      ])
    }
  }

  const stop = async () => {
    if (!visibleSession) return
    try {
      await v2AgentApi.stopSession(visibleSession.id)
      disconnectStream()
      await Promise.all([sessionQuery.refetch(), sessionsQuery.refetch()])
    } catch (error) {
      setStreamError((error as Error).message || '停止失败')
    }
  }

  const chooseSkill = (skill: AgentSkill) => {
    setDraft(`/${skill.slug} `)
    setSlashDismissed(true)
  }

  const updateTool = async (name: string, enabled: boolean) => {
    const key = `tool:${name}`
    setSavingSetting(key)
    try {
      await v2AgentApi.setToolEnabled(name, enabled)
      await configQuery.refetch()
      message.success(enabled ? '工具已激活' : '工具已停用')
    } catch (error) {
      message.error((error as Error).message || '工具设置保存失败')
    } finally {
      setSavingSetting('')
    }
  }

  const updateSkill = async (id: string, enabled: boolean) => {
    const key = `skill:${id}`
    setSavingSetting(key)
    try {
      await v2AgentApi.setSkillEnabled(id, enabled)
      await configQuery.refetch()
      message.success(enabled ? 'Skill 已激活' : 'Skill 已停用')
    } catch (error) {
      message.error((error as Error).message || 'Skill 设置保存失败')
    } finally {
      setSavingSetting('')
    }
  }

  const createSkill = async (values: CreateAgentSkillInput) => {
    setCreatingSkill(true)
    try {
      const result = await v2AgentApi.createSkill(values)
      await configQuery.refetch()
      setSkillModalOpen(false)
      skillForm.resetFields()
      message.success(`Skill /${result.skill.slug} 已创建`)
    } catch (error) {
      message.error((error as Error).message || 'Skill 创建失败')
    } finally {
      setCreatingSkill(false)
    }
  }

  const enabledToolCount = configQuery.data?.config.tools.filter((tool) => tool.enabled).length ?? 0

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
              onClick={() => openSession(item.id)} />
          ))}
        </div>
        <div className="agent-capability-note">
          <ThunderboltFilled />
          <span>连接 ReqFlow 全部业务能力<br /><small>{enabledToolCount} 个工具 · {enabledSkills.length} 个 Skill 已就绪</small></span>
        </div>
      </aside>

      <main className="agent-conversation">
        <header className="agent-topbar">
          <div className="agent-identity">
            <Avatar className="agent-avatar" icon={<RobotOutlined />} />
            <div><Text strong>ReqFlow Agent</Text><span><i /> 平台数字大脑</span></div>
          </div>
          <div className="agent-topbar-actions">
            <div className="agent-tool-summary">
              <ToolOutlined /> 流程 · 任务 · 数据 · Skill <b>{enabledToolCount}</b>
            </div>
            <Tooltip title="Agent 设置">
              <Button className="agent-settings-button" type="text" icon={<SettingOutlined />}
                onClick={() => setSettingsOpen(true)} />
            </Tooltip>
          </div>
        </header>

        <section className="agent-thread">
          {sessionId && !visibleSession ? (
            <div className="agent-loading">{sessionQuery.isError ? <Empty description="会话加载失败" /> : <Spin />}</div>
          ) : !sessionId && !visibleSession ? (
            <Welcome onPrompt={send} />
          ) : (
            <div className="agent-messages">
              {displayMessages.map((message, index) => (
                <MessageView key={`${message.timestamp ?? index}-${index}`} message={message} />
              ))}
              {isRunning && <LiveResponse text={liveText} thinking={thinking} tools={liveTools} detached={!running} />}
              {streamError && <div className="agent-error"><ExclamationCircleFilled /> {streamError}</div>}
              <div ref={bottomRef} />
            </div>
          )}
        </section>

        <footer className="agent-composer-wrap">
          <div className="agent-composer-shell">
            {slashCandidates.length > 0 && (
              <div className="agent-skill-menu" role="listbox" aria-label="可用 Skill">
                <div className="agent-skill-menu-head">选择 Skill <span>输入斜杠快速调用</span></div>
                {slashCandidates.map((skill, index) => (
                  <button key={skill.id} className={index === slashIndex ? 'active' : ''}
                    onMouseDown={(event) => event.preventDefault()} onClick={() => chooseSkill(skill)}>
                    <span className="skill-command">/{skill.slug}</span>
                    <span><b>{skill.title}</b><small>{skill.description || '纯文本提示词 Skill'}</small></span>
                    {skill.builtin && <Tag color="purple">内置</Tag>}
                  </button>
                ))}
              </div>
            )}
            <div className={`agent-composer ${isRunning ? 'is-running' : ''}`}>
              <TextArea
                value={draft}
                onChange={(event) => {
                  setDraft(event.target.value)
                  setSlashDismissed(false)
                }}
                onKeyDown={(event) => {
                  if (slashCandidates.length > 0) {
                    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
                      event.preventDefault()
                      setSlashIndex((current) => (current + (event.key === 'ArrowDown' ? 1 : -1)
                        + slashCandidates.length) % slashCandidates.length)
                      return
                    }
                    if (event.key === 'Tab' || (event.key === 'Enter' && !event.shiftKey)) {
                      event.preventDefault()
                      chooseSkill(slashCandidates[slashIndex] ?? slashCandidates[0])
                      return
                    }
                    if (event.key === 'Escape') {
                      event.preventDefault()
                      setSlashDismissed(true)
                      return
                    }
                  }
                  if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) {
                    event.preventDefault()
                    void send()
                  }
                }}
                disabled={isRunning}
                autoSize={{ minRows: 1, maxRows: 6 }}
                placeholder="告诉数字大脑你想完成什么，输入 / 可调用 Skill…"
                variant="borderless"
              />
              {isRunning ? (
                <Button className="agent-send stop" shape="circle" icon={<StopOutlined />} onClick={() => void stop()} />
              ) : (
                <Button className="agent-send" type="primary" shape="circle" icon={<SendOutlined />}
                  disabled={!draft.trim()} onClick={() => void send()} />
              )}
            </div>
          </div>
          <Text className="agent-composer-hint">/ 调用 Skill · Enter 发送 · Shift + Enter 换行</Text>
        </footer>
      </main>

      <Drawer title="Agent 设置" width={520} open={settingsOpen} onClose={() => setSettingsOpen(false)}>
        <div className="agent-settings-intro">按模块控制数字大脑可以调用的能力。停用后会立即从下一轮模型上下文中移除。</div>
        <div className="agent-settings-title"><span>工具</span><Tag>{enabledToolCount}/{configQuery.data?.config.tools.length ?? 0} 已激活</Tag></div>
        {configQuery.isLoading ? <div className="agent-settings-loading"><Spin /></div> : (
          <div className="agent-settings-list">
            {(configQuery.data?.config.tools ?? []).map((tool) => (
              <div className="agent-setting-item" key={tool.name}>
                <div><span><b>{tool.label}</b><Tag bordered={false}>{tool.group}</Tag></span><small>{tool.description}</small></div>
                <Switch checked={tool.enabled} loading={savingSetting === `tool:${tool.name}`}
                  onChange={(enabled) => void updateTool(tool.name, enabled)} />
              </div>
            ))}
          </div>
        )}
        <div className="agent-settings-title skill"><span>Skill</span>
          <Button type="primary" ghost size="small" icon={<PlusOutlined />} onClick={() => setSkillModalOpen(true)}>创建 Skill</Button>
        </div>
        <div className="agent-settings-list">
          {(configQuery.data?.config.skills ?? []).map((skill) => (
            <div className="agent-setting-item skill" key={skill.id}>
              <div><span><b>/{skill.slug}</b>{skill.builtin && <Tag color="purple">内置</Tag>}</span>
                <small><strong>{skill.title}</strong>{skill.description ? ` · ${skill.description}` : ''}</small></div>
              <Switch checked={skill.enabled} loading={savingSetting === `skill:${skill.id}`}
                onChange={(enabled) => void updateSkill(skill.id, enabled)} />
            </div>
          ))}
          {!configQuery.isLoading && !(configQuery.data?.config.skills.length) && <Empty description="还没有 Skill" />}
        </div>
      </Drawer>

      <Modal title="创建纯文本 Skill" open={skillModalOpen} confirmLoading={creatingSkill}
        okText="创建并激活" cancelText="取消" onOk={() => skillForm.submit()}
        onCancel={() => { setSkillModalOpen(false); skillForm.resetFields() }}>
        <div className="agent-skill-form-tip">Skill 只保存提示词，不执行脚本，也不包含附件或依赖。</div>
        <Form form={skillForm} layout="vertical" requiredMark={false} onFinish={(values) => void createSkill(values)}
          initialValues={{ enabled: true }}>
          <Form.Item name="slug" label="斜杠命令" rules={[
            { required: true, message: '请输入斜杠命令' },
            { pattern: /^[a-z][a-z0-9-]{0,47}$/, message: '使用小写字母、数字和连字符，并以字母开头' },
          ]}><Input prefix="/" placeholder="summarize-requirement" /></Form.Item>
          <Form.Item name="title" label="名称" rules={[{ required: true, message: '请输入 Skill 名称' }, { max: 80 }]}>
            <Input placeholder="需求摘要" />
          </Form.Item>
          <Form.Item name="description" label="简介" rules={[{ max: 500 }]}>
            <Input placeholder="说明何时应该使用这个 Skill" />
          </Form.Item>
          <Form.Item name="prompt" label="提示词" rules={[{ required: true, message: '请输入 Skill 提示词' }, { max: 30000 }]}>
            <TextArea rows={9} placeholder="描述角色、处理步骤、输出格式和边界…" />
          </Form.Item>
        </Form>
      </Modal>
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
    {!message.is_error && meta.path && <Button type="link" size="small" onClick={() => navigate(meta.path!)}>查看</Button>}
  </div>
}

function LiveResponse({ text, thinking, tools, detached }: { text: string; thinking: string; tools: LiveTool[]; detached?: boolean }) {
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
        : !thinking && tools.length === 0 && <div className="agent-working"><LoadingOutlined /> {detached ? '模型正在后台继续处理，完成后会自动显示…' : '正在理解你的任务…'}</div>}
    </div>
  </div>
}

function MarkdownAnswer({ text }: { text: string }) {
  return <div className="agent-answer"><ReactMarkdown remarkPlugins={[remarkGfm]}>{text}</ReactMarkdown></div>
}
