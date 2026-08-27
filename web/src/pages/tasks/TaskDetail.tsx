import { useEffect, useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import {
  Card, Col, Row, Tag, Typography, Space, Button, Timeline, Input, App, Empty, Spin, Table, Tooltip,
} from 'antd'
import {
  ArrowLeftOutlined, PauseCircleOutlined, PlayCircleOutlined, CheckCircleOutlined,
  EditOutlined, ToolOutlined, CheckCircleTwoTone, CloseCircleTwoTone,
} from '@ant-design/icons'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { tasksApi } from '../../api/tasks'
import { api } from '../../api/client'
import { useTaskEvents, traceFromStepData } from '../../hooks/useTaskEvents'
import { parseDatasetItemFields, parseTaskWorkflow } from '../../api/types'
import type { Task, TaskInput, TaskStep, TaskType, ToolTrace, StepKind, SettingsView } from '../../api/types'
import ConfirmParsePanel from './panels/ConfirmParsePanel'
import AnalysisPane from './panels/AnalysisPane'
import MatchImportPanel from './panels/MatchImportPanel'

const { Text } = Typography

export const TASK_STATUS_TAG: Record<string, { color: string; label: string }> = {
  pending: { color: 'default', label: '待开始' },
  running: { color: 'processing', label: '进行中' },
  awaiting: { color: 'warning', label: '等待确认' },
  paused: { color: 'gold', label: '已暂停' },
  succeeded: { color: 'success', label: '成功' },
  failed: { color: 'error', label: '失败' },
}

export const TYPE_LABEL: Record<TaskType, string> = {
  requirement_import: '需求导入',
}

const STEP_COLOR: Record<TaskStep['Status'], string> = {
  pending: 'gray',
  running: 'blue',
  succeeded: 'green',
  failed: 'red',
  awaiting: 'orange',
  paused: 'gold',
}

/** 步骤种类 → 人读标签（工作流元数据展示；analyze 按实际模式显示，见 kindLabelOf） */
const KIND_LABEL: Record<Exclude<StepKind, 'analyze'>, string> = {
  parse: '解析',
  human: '人工门',
  dataset: '生成数据集',
}

/** analyze 步骤标签如实反映执行模式（agent_mode 开关决定，不再无条件标 AI agent） */
const kindLabelOf = (kind: StepKind, agentMode?: boolean): string =>
  kind === 'analyze' ? (agentMode ? 'AI agent' : 'AI 分析') : KIND_LABEL[kind]

/** 任务详情页：头部（编辑/生命周期操作） + 步骤时间线 + 按阶段切换的工作区面板 */
export default function TaskDetail() {
  const { id } = useParams()
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { message, modal } = App.useApp()
  const { data, isLoading } = useQuery({
    queryKey: ['task', id],
    queryFn: () => tasksApi.get(id!),
    enabled: !!id,
  })
  // 执行模式（llm.agent_mode）：步骤标签如实显示，避免 UI 承诺 agent 而后端单轮直调
  const { data: settings } = useQuery({ queryKey: ['settings'], queryFn: () => api.get<SettingsView>('/api/settings') })
  const agentMode = settings?.llm.agentMode
  const stream = useTaskEvents(id)
  const [editingTitle, setEditingTitle] = useState(false)
  const [titleDraft, setTitleDraft] = useState('')

  // hook 必须无条件调用：在早退 return 之前解析（data 未就绪时给空值）
  const input = useMemo(() => {
    try { return JSON.parse(data?.task.Input ?? '{}') as TaskInput } catch { return {} }
  }, [data?.task.Input])
  const workflow = useMemo(() => parseTaskWorkflow(data?.task as Task), [data?.task])

  // 工具轨迹重放：分析步骤（seq 3）的 data 在重连/刷新后回放
  const replayedTrace = useMemo(() => {
    const step = data?.steps.find((s) => s.Seq === 3)
    return traceFromStepData(step?.Data ?? '')
  }, [data?.steps])
  const toolTrace = stream.toolTrace.length > 0 ? stream.toolTrace : replayedTrace

  useEffect(() => {
    if (data && !editingTitle) setTitleDraft(data.task.Title)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data?.task.Title])

  if (isLoading) return <Spin style={{ display: 'block', margin: '80px auto' }} />
  if (!data) {
    return (
      <Card>
        <Empty description="任务不存在">
          <Button type="primary" onClick={() => navigate('/tasks')}>返回任务列表</Button>
        </Empty>
      </Card>
    )
  }

  const { task, steps, items } = data
  const tag = TASK_STATUS_TAG[task.Status] ?? { color: 'default', label: task.Status }
  const paused = task.Status === 'paused'

  const saveTitle = async () => {
    const t = titleDraft.trim()
    setEditingTitle(false)
    if (!t || t === task.Title) return
    try {
      await tasksApi.patch(task.ID, { title: t })
      qc.invalidateQueries({ queryKey: ['task', task.ID] })
      qc.invalidateQueries({ queryKey: ['tasks'] })
      message.success('标题已更新')
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const lifecycle = async (fn: () => Promise<unknown>, okMsg: string) => {
    try {
      await fn()
      qc.invalidateQueries({ queryKey: ['task', task.ID] })
      qc.invalidateQueries({ queryKey: ['tasks'] })
      if (okMsg) message.success(okMsg)
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const onComplete = () => {
    modal.confirm({
      title: '手动完成该任务？',
      content: '任务将标记为「成功」终态（未执行的步骤不再继续）。',
      okText: '完成',
      onOk: () => lifecycle(() => tasksApi.complete(task.ID), '任务已完成'),
    })
  }

  const workspace = (() => {
    switch (task.CurrentStep) {
      case 2: return <ConfirmParsePanel task={task} input={input} />
      case 3: return <AnalysisPane task={task} stream={{ ...stream, toolTrace }} />
      case 4: return (
        <MatchImportPanel
          task={task} items={items} importing={task.Status === 'running'} stream={stream}
        />
      )
      default: return (
        <MonitorView task={task} steps={steps} items={items} toolTrace={toolTrace} input={input} />
      )
    }
  })()

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      {/* 头部 */}
      <Card size="small">
        <Row align="middle" gutter={16}>
          <Col>
            <Button type="text" icon={<ArrowLeftOutlined />} onClick={() => navigate('/tasks')} title="返回列表" />
          </Col>
          <Col flex="auto">
            <Space size={12} wrap>
              {editingTitle ? (
                <Space.Compact>
                  <Input
                    size="small" value={titleDraft} autoFocus
                    onChange={(e) => setTitleDraft(e.target.value)}
                    onPressEnter={saveTitle}
                    onBlur={saveTitle}
                  />
                </Space.Compact>
              ) : (
                <Space>
                  <Text strong style={{ fontSize: 16 }}>{task.Title}</Text>
                  <Button type="text" size="small" icon={<EditOutlined />} onClick={() => { setTitleDraft(task.Title); setEditingTitle(true) }} />
                </Space>
              )}
              <Tag color={tag.color}>{tag.label}</Tag>
              <Tag>{TYPE_LABEL[task.Type] ?? task.Type}</Tag>
              {paused && task.ErrorMessage && (
                <Text type="warning" style={{ fontSize: 12 }}>{task.ErrorMessage}</Text>
              )}
            </Space>
          </Col>
          <Col>
            <Space>
              <Text type="secondary" style={{ fontSize: 12 }}>
                创建 {new Date(task.CreatedAt).toLocaleString('zh-CN')}
                {task.FinishedAt && task.FinishedAt.startsWith('0001') === false
                  ? ` · 完成 ${new Date(task.FinishedAt).toLocaleString('zh-CN')}`
                  : ''}
              </Text>
              {task.Status === 'running' && (
                <Button icon={<PauseCircleOutlined />} onClick={() => lifecycle(() => tasksApi.pause(task.ID), '已暂停')}>暂停</Button>
              )}
              {paused && (
                <Button type="primary" icon={<PlayCircleOutlined />} onClick={() => lifecycle(() => tasksApi.resume(task.ID), '已继续')}>继续</Button>
              )}
              {task.Status === 'awaiting' && (
                <Button icon={<CheckCircleOutlined />} onClick={onComplete}>完成</Button>
              )}
            </Space>
          </Col>
        </Row>
      </Card>

      {/* 步骤时间线 + 工作区 */}
      <Row gutter={[16, 16]}>
        <Col span={7}>
          <Card size="small" title={<Text strong>任务步骤</Text>}>
            <Timeline
              style={{ marginTop: 8 }}
              items={steps.map((s) => {
                const def = workflow?.steps.find((d) => d.seq === s.Seq)
                return {
                  color: STEP_COLOR[s.Status] ?? 'gray',
                  children: (
                    <div>
                      <Space size={8} wrap>
                        <Text strong style={{ fontSize: 13 }}>{s.Name}</Text>
                        <StepStatusTag status={s.Status} />
                        {def && (
                          <Tag style={{ margin: 0, fontSize: 11 }} color={def.kind === 'human' ? 'orange' : 'geekblue'}>
                            {kindLabelOf(def.kind, agentMode)}
                          </Tag>
                        )}
                      </Space>
                      {def?.deps.map((d, i) => (
                        <div key={i} style={{ fontSize: 11, color: '#9aa1b5', marginTop: 2 }}>
                          依赖：{d.data} · {d.tool}
                        </div>
                      ))}
                      {s.Detail && (
                        <div style={{ fontSize: 12, color: '#6b7280', marginTop: 2 }}>{s.Detail}</div>
                      )}
                      {s.StartedAt && !s.StartedAt.startsWith('0001') && (
                        <div style={{ fontSize: 11, color: '#9aa1b5', marginTop: 2 }}>
                          {new Date(s.StartedAt).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit' })}
                          {s.EndedAt && !s.EndedAt.startsWith('0001') && ` → ${new Date(s.EndedAt).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit' })}`}
                        </div>
                      )}
                    </div>
                  ),
                }
              })}
            />
          </Card>
        </Col>
        <Col span={17}>{workspace}</Col>
      </Row>
    </Space>
  )
}

function StepStatusTag({ status }: { status: TaskStep['Status'] }) {
  const map: Record<TaskStep['Status'], { color: string; label: string }> = {
    pending: { color: 'default', label: '未开始' },
    running: { color: 'processing', label: '执行中' },
    succeeded: { color: 'success', label: '完成' },
    failed: { color: 'error', label: '失败' },
    awaiting: { color: 'warning', label: '待确认' },
    paused: { color: 'gold', label: '已暂停' },
  }
  const t = map[status]
  return <Tag color={t.color} style={{ margin: 0, fontSize: 11 }}>{t.label}</Tag>
}

/** 监控态（未进入任何工作区阶段 / 终态回放）：概要 + 明细 + 工具轨迹 */
function MonitorView({
  task, steps, items, toolTrace, input,
}: { task: Task; steps: TaskStep[]; items: import('../../api/types').TaskItem[]; toolTrace: ToolTrace[]; input: TaskInput }) {
  const failed = items.filter((i) => i.Status === 'failed').length
  const imported = items.filter((i) => i.Status === 'success').length
  const analyzeStep = steps.find((s) => s.Seq === 3)

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Card size="small">
        <Space size={24} wrap>
          <Text><Text type="secondary">工作项</Text> <Text strong>{items.length}</Text></Text>
          {task.Type === 'requirement_import' && (
            <>
              <Text><Text type="secondary">已导入</Text> <Text strong type="success">{imported}</Text></Text>
              {failed > 0 && <Text><Text type="secondary">失败</Text> <Text strong type="danger">{failed}</Text></Text>}
              {task.TargetProjectName && <Text><Text type="secondary">目标项目</Text> <Text strong>{task.TargetProjectName}</Text></Text>}
            </>
          )}
          {task.Output && (
            <Tooltip title={task.Output}>
              <Text type="secondary" style={{ fontSize: 12 }}>统计：{task.Output.slice(0, 80)}…</Text>
            </Tooltip>
          )}
          {input.file_name && <Text type="secondary" style={{ fontSize: 12 }}>源文件：{input.file_name}</Text>}
        </Space>
      </Card>

      {toolTrace.length > 0 && (
        <Card size="small" title={<Space><ToolOutlined style={{ color: '#7c3aed' }} /><Text strong>工具查证轨迹</Text>
          <Text type="secondary" style={{ fontSize: 12 }}>AI 出稿前自主核实的信息（只读查询）</Text></Space>}>
          {toolTrace.map((t, i) => (
            <div key={`${t.callId}-${i}`} style={{ display: 'flex', alignItems: 'baseline', gap: 8, padding: '3px 0', fontSize: 12.5 }}>
              {t.status === 'error' ? <CloseCircleTwoTone twoToneColor="#dc2626" /> : <CheckCircleTwoTone twoToneColor="#16a34a" />}
              <Text strong>{t.name}</Text>
              {t.args && <Text code style={{ fontSize: 11 }}>{t.args}</Text>}
              {t.details && <Text type="secondary">{t.details}</Text>}
            </div>
          ))}
        </Card>
      )}

      {analyzeStep?.Data && (
        <Card size="small" title={<Text strong>分析产出摘要</Text>}>
          <Text type="secondary" style={{ fontSize: 12 }}>{analyzeStep.Detail}</Text>
        </Card>
      )}

      {items.length > 0 && (
        <Card size="small" title={<Text strong>工作项明细</Text>}>
          <Table
            rowKey="ID" size="small" dataSource={items} pagination={false}
            columns={[
              { title: '标题', ellipsis: true, render: (_, r) => parseDatasetItemFields(r.Fields)['title'] ?? '' },
              { title: '项目', width: 120, ellipsis: true, render: (_, r) => parseDatasetItemFields(r.Fields)['project_name'] ?? '' },
              { title: '类型', width: 80, render: (_, r) => parseDatasetItemFields(r.Fields)['type_id'] ?? '' },
              { title: '优先级', width: 90, render: (_, r) => parseDatasetItemFields(r.Fields)['priority'] ?? '' },
              {
                title: '结果', dataIndex: 'Status', width: 90,
                render: (v) => v === 'success' ? <Tag color="green">成功</Tag> : v === 'failed' ? <Tag color="red">失败</Tag> : <Tag>待导入</Tag>,
              },
            ]}
          />
        </Card>
      )}
    </Space>
  )
}
