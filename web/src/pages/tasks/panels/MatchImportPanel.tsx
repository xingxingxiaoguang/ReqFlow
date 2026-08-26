import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  Card, Col, Row, Typography, Space, Button, Select, Table, Tag, InputNumber,
  Input, Progress, Alert, App, Statistic, List,
} from 'antd'
import {
  AimOutlined, DatabaseOutlined, ReloadOutlined, CheckCircleTwoTone,
  CloseCircleTwoTone, SaveOutlined, PlayCircleOutlined,
} from '@ant-design/icons'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../../../api/client'
import { tasksApi } from '../../../api/tasks'
import type { SettingsView, Task, TaskItem } from '../../../api/types'
import type { TaskStream } from '../../../hooks/useTaskEvents'
import MatchBadge from '../../../components/MatchBadge'

const { Text } = Typography

const PRIORITY_OPTIONS = [
  { value: 'High', label: 'High · 高' },
  { value: 'Medium', label: 'Medium · 中' },
  { value: 'Low', label: 'Low · 低' },
]
const TYPE_OPTIONS = ['story', 'task', 'bug', 'feature', 'epic'].map((v) => ({ value: v, label: v }))

/** 生成数据集门：查重 → 行内编辑草稿 → 保存 → 命名数据集并生成（任务产出的结果集） */
export default function MatchImportPanel({
  task, items, importing, stream,
}: { task: Task; items: TaskItem[]; importing: boolean; stream: TaskStream }) {
  const qc = useQueryClient()
  const { message } = App.useApp()
  const [editable, setEditable] = useState<TaskItem[]>(items)
  const [datasetName, setDatasetName] = useState<string>()
  const [dupResults, setDupResults] = useState<{ index: number; match: any }[]>([])
  const [saving, setSaving] = useState(false)
  const { importProgress, importLog } = stream

  // 服务端明细变化（分析完成/重新分析）同步到编辑态
  useEffect(() => {
    setEditable(items)
  }, [items])

  const { data: settings } = useQuery({ queryKey: ['settings'], queryFn: () => api.get<SettingsView>('/api/settings') })

  /* 查重（语料 = 已有需求数据集，跨数据集） */
  const runDuplicates = useCallback(async () => {
    try {
      const data = await api.post<{ results: { index: number; match: any }[] }>('/api/match/duplicates', {
        items: editable.map((i) => ({
          project_name: i.project_name, title: i.title, description: i.description,
          priority: i.priority, estimated_hours: i.estimated_hours, start_at: i.start_at,
          end_at: i.end_at, type_id: i.type_id, assignee_name: i.assignee_name,
          state: i.state, solution_suggestion: i.solution_suggestion,
        })),
      })
      setDupResults(data.results)
    } catch (e) {
      message.warning(`查重失败：${(e as Error).message}`)
    }
  }, [editable, message])

  useEffect(() => {
    if (editable.length && !importing) void runDuplicates()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [editable.length, importing])

  const patchItem = (index: number, patch: Partial<TaskItem>) => {
    setEditable((list) => list.map((it, i) => (i === index ? { ...it, ...patch } : it)))
  }

  const dupByIndex = useMemo(() => {
    const m = new Map<number, any>()
    for (const r of dupResults) m.set(r.index, r.match)
    return m
  }, [dupResults])

  const onSave = async () => {
    setSaving(true)
    try {
      await tasksApi.saveItems(
        task.ID,
        editable.map((i) => ({
          id: i.ID,
          draft: {
            project_name: i.project_name, title: i.title, description: i.description,
            priority: i.priority, estimated_hours: i.estimated_hours, start_at: i.start_at,
            end_at: i.end_at, type_id: i.type_id, assignee_name: i.assignee_name,
            state: i.state, solution_suggestion: i.solution_suggestion,
          },
        })),
      )
      qc.invalidateQueries({ queryKey: ['task', task.ID] })
      message.success('草稿已保存')
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const onGenerate = async () => {
    const name = datasetName?.trim()
    if (!name) {
      message.warning('请先为数据集命名')
      return
    }
    try {
      await tasksApi.triggerGenerateDataset(task.ID, name)
      qc.invalidateQueries({ queryKey: ['task', task.ID] })
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const onResumeGenerate = async () => {
    try {
      await tasksApi.resume(task.ID)
      qc.invalidateQueries({ queryKey: ['task', task.ID] })
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const dupCount = dupResults.filter((r) => r.match).length
  const totalHours = editable.reduce((s, i) => s + (i.estimated_hours || 0), 0)
  const done = task.Status === 'succeeded'

  return (
    <Row gutter={[16, 16]}>
      {/* 概要 + 数据集命名 */}
      <Col span={24}>
        <Card size="small">
          <Row gutter={24} align="middle">
            <Col><Statistic title="需求条目" value={editable.length} /></Col>
            <Col><Statistic title="涉及分组" value={new Set(editable.map((i) => i.project_name).filter(Boolean)).size} /></Col>
            <Col><Statistic title="预估总工时" value={totalHours} suffix="h" /></Col>
            <Col>{dupResults.length > 0 && <Statistic title="疑似重复" value={dupCount} valueStyle={{ color: dupCount ? '#dc2626' : undefined }} />}</Col>
            <Col flex="auto" />
            <Col span={9}>
              <Space.Compact style={{ width: '100%' }}>
                <Input
                  placeholder="数据集命名（如：订单中心需求集 v1）"
                  value={datasetName}
                  onChange={(e) => setDatasetName(e.target.value)}
                  disabled={importing || done}
                />
                <Button
                  icon={<ReloadOutlined />} onClick={() => void runDuplicates()}
                  title="重新查重"
                />
              </Space.Compact>
              <div style={{ marginTop: 6 }}>
                <Text type="secondary" style={{ fontSize: 12 }}>
                  生成的数据集将作为后续任务（如 Bug 分析）的输入底料——任务与任务通过数据集衔接
                </Text>
              </div>
            </Col>
          </Row>
        </Card>
      </Col>

      {/* 明细表 */}
      <Col span={24}>
        <Card
          size="small"
          title={<Space><AimOutlined style={{ color: '#4F46E5' }} /><Text strong>需求明细</Text><Text type="secondary">（可直接编辑）</Text></Space>}
          extra={
            <Space>
              {dupResults.length > 0 && <Text type="secondary">查重完成</Text>}
              {importing ? (
                <Button type="primary" icon={<PlayCircleOutlined />} onClick={onResumeGenerate}>继续生成</Button>
              ) : (
                <>
                  <Button icon={<SaveOutlined />} loading={saving} onClick={onSave}>保存草稿</Button>
                  <Button
                    type="primary"
                    icon={<DatabaseOutlined />}
                    disabled={!datasetName?.trim() || done}
                    onClick={onGenerate}
                  >
                    {task.ErrorMessage && !done ? '重新生成数据集' : '生成数据集'}
                  </Button>
                </>
              )}
            </Space>
          }
        >
          {settings && !settings.embedding.configured && (
            <Alert type="warning" showIcon style={{ marginBottom: 12 }}
              message="Embedding 未配置：数据集照常生成，但语义查重与 agent 查证降级为仅精确匹配。" />
          )}
          <Table
            rowKey={(_, i) => String(i)}
            dataSource={editable}
            size="middle"
            pagination={editable.length > 10 ? { pageSize: 10, showSizeChanger: false } : false}
            columns={[
              {
                title: '查重', width: 92, dataIndex: 'match',
                render: (_, __, index) => <MatchBadge match={dupByIndex.get(index)} />,
              },
              { title: '标题', dataIndex: 'title', width: 260,
                render: (v, _, index) => <Input value={v} variant="borderless" disabled={importing || done} onChange={(e) => patchItem(index, { title: e.target.value })} /> },
              { title: '分组', dataIndex: 'project_name', width: 130, ellipsis: true },
              { title: '类型', dataIndex: 'type_id', width: 110,
                render: (v, _, index) => <Select variant="borderless" value={v} style={{ width: '100%' }} options={TYPE_OPTIONS} disabled={importing || done} onChange={(t) => patchItem(index, { type_id: t })} /> },
              { title: '优先级', dataIndex: 'priority', width: 130,
                render: (v, _, index) => <Select variant="borderless" value={v} style={{ width: '100%' }} options={PRIORITY_OPTIONS} disabled={importing || done} onChange={(p) => patchItem(index, { priority: p })} /> },
              { title: '工时(h)', dataIndex: 'estimated_hours', width: 100,
                render: (v, _, index) => <InputNumber variant="borderless" min={0} value={v} disabled={importing || done} onChange={(n) => patchItem(index, { estimated_hours: n ?? 0 })} /> },
              { title: '负责人', dataIndex: 'assignee_name', width: 110,
                render: (v, _, index) => <Input variant="borderless" value={v ?? ''} placeholder="可留空" disabled={importing || done} onChange={(e) => patchItem(index, { assignee_name: e.target.value })} /> },
              { title: '状态', dataIndex: 'state', width: 90,
                render: (v) => v ? <Tag style={{ margin: 0 }}>{v}</Tag> : <Tag style={{ margin: 0 }} color="default">待办</Tag> },
              {
                title: '结果', dataIndex: 'Status', width: 90,
                render: (v) => v === 'success' ? <Tag color="green">已入数据集</Tag> : <Tag>待生成</Tag>,
              },
            ]}
            expandable={{
              rowExpandable: (r) => !!r.description || !!r.solution_suggestion || !!r.ErrorMessage,
              expandedRowRender: (r) => (
                <div style={{ padding: '4px 8px' }}>
                  <ParagraphBlock label="描述" text={r.description} />
                  <ParagraphBlock label="解决方案建议" text={r.solution_suggestion} />
                  <ParagraphBlock label="失败原因" text={r.ErrorMessage} danger />
                </div>
              ),
            }}
          />
        </Card>
      </Col>

      {/* 生成进度 */}
      {(importing || importLog.length > 0 || done) && (
        <Col span={24}>
          <Card size="small" title={<Text strong>数据集生成进度</Text>} extra={done && (
            <Space>
              <CheckCircleTwoTone twoToneColor="#16a34a" />
              <Text>数据集「{task.Title}」已生成</Text>
              <Button size="small" onClick={() => qc.invalidateQueries({ queryKey: ['datasets'] })}>刷新数据集</Button>
            </Space>
          )}>
            {importing && (
              <Progress
                percent={Math.round((importProgress.current / Math.max(1, importProgress.total)) * 100)}
                size="small" style={{ maxWidth: 480, marginBottom: 12 }}
                format={() => `${importProgress.current}/${importProgress.total}`}
              />
            )}
            <List
              size="small"
              style={{ maxHeight: 260, overflow: 'auto' }}
              dataSource={importLog}
              renderItem={(r) => (
                <List.Item style={{ padding: '4px 0' }}>
                  <Space>
                    {r.status === 'success'
                      ? <CheckCircleTwoTone twoToneColor="#16a34a" />
                      : <CloseCircleTwoTone twoToneColor="#dc2626" />}
                    <Text style={{ fontSize: 13 }}>{r.title}</Text>
                  </Space>
                </List.Item>
              )}
            />
          </Card>
        </Col>
      )}
    </Row>
  )
}

function ParagraphBlock({ label, text, danger }: { label: string; text?: string; danger?: boolean }) {
  if (!text) return null
  return (
    <div style={{ marginBottom: 8 }}>
      <Text type={danger ? 'danger' : 'secondary'} strong style={{ fontSize: 12 }}>{label}</Text>
      <div style={{ whiteSpace: 'pre-wrap', fontSize: 13, color: danger ? '#dc2626' : '#374151', marginTop: 2 }}>{text}</div>
    </div>
  )
}
