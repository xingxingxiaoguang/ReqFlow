import { useCallback, useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import {
  Card, Col, Row, Typography, Space, Button, Select, Table, Tag, InputNumber,
  Input, Progress, Empty, App, Alert, Statistic, List,
} from 'antd'
import {
  AimOutlined, DownloadOutlined, ReloadOutlined, CheckCircleTwoTone,
  CloseCircleTwoTone, HistoryOutlined,
} from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { api } from '../../api/client'
import { postSSE } from '../../api/sse'
import type { DraftItem, ProjectMatch, SettingsView, SyncedProject } from '../../api/types'
import { useImportWizard } from '../../stores/importWizard'
import MatchBadge from '../../components/MatchBadge'

const { Text } = Typography

const PRIORITY_OPTIONS = [
  { value: 'High', label: 'High · 高' },
  { value: 'Medium', label: 'Medium · 中' },
  { value: 'Low', label: 'Low · 低' },
]
const TYPE_OPTIONS = ['story', 'task', 'bug', 'feature', 'epic'].map((v) => ({ value: v, label: v }))

/** 阶段 4：项目匹配 → 查重 → 行内编辑 → SSE 导入 */
export default function Result() {
  const navigate = useNavigate()
  const { message } = App.useApp()
  const wizard = useImportWizard()
  const { items, recordId, matches, selectedProjectId, dupResults, setField } = wizard

  const { data: settings } = useQuery({ queryKey: ['settings'], queryFn: () => api.get<SettingsView>('/api/settings') })
  const { data: syncedProjects } = useQuery({
    queryKey: ['projects'],
    queryFn: () => api.get<{ projects: SyncedProject[] }>('/api/projects'),
  })

  const uniqueNames = useMemo(() => [...new Set(items.map((i) => i.project_name).filter(Boolean))], [items])
  const matchedByName = useMemo(() => {
    const m = new Map<string, ProjectMatch[]>()
    for (const match of matches) {
      const list = m.get(match.suggested_name) ?? []
      list.push(match)
      m.set(match.suggested_name, list)
    }
    return m
  }, [matches])

  /* 进入结果页自动推荐项目 */
  const runMatch = useCallback(async () => {
    if (uniqueNames.length === 0) return
    try {
      const data = await api.post<{ matches: ProjectMatch[] }>('/api/match/projects', { names: uniqueNames })
      setField({ matches: data.matches })
      if (data.matches[0] && !selectedProjectId) setField({ selectedProjectId: data.matches[0].id })
    } catch (e) {
      message.warning(`项目推荐失败：${(e as Error).message}`)
    }
  }, [uniqueNames, selectedProjectId, setField, message])

  useEffect(() => {
    if (items.length && matches.length === 0) void runMatch()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [items.length])

  /* 选中项目后自动查重 */
  const runDuplicates = useCallback(
    async (projectId: string) => {
      try {
        const data = await api.post<{ results: { index: number; match: any }[] }>('/api/match/duplicates', {
          project_id: projectId,
          items: items.map((i) => ({
            project_name: i.project_name, title: i.title, description: i.description,
            priority: i.priority, estimated_hours: i.estimated_hours, start_at: i.start_at,
            end_at: i.end_at, type_id: i.type_id, assignee_name: i.assignee_name,
            state: i.state, solution_suggestion: i.solution_suggestion,
          })),
        })
        setField({ dupResults: data.results })
      } catch (e) {
        message.warning(`查重失败：${(e as Error).message}`)
      }
    },
    [items, setField, message],
  )

  const onProjectChange = (value: string) => {
    setField({ selectedProjectId: value, dupResults: [] })
    if (!value.startsWith('new:')) void runDuplicates(value)
  }

  const patchItem = (index: number, patch: Partial<DraftItem>) => {
    const next = items.map((it, i) => (i === index ? { ...it, ...patch } : it))
    setField({ items: next })
  }

  const dupByIndex = useMemo(() => {
    const m = new Map<number, any>()
    for (const r of dupResults) m.set(r.index, r.match)
    return m
  }, [dupResults])

  /* 导入 */
  const [importLog, setImportLog] = useState<{ title: string; status: string }[]>([])
  const startImport = async () => {
    if (!recordId || !selectedProjectId) return
    setField({ importing: true, importProgress: { current: 0, total: items.length }, importDone: undefined })
    setImportLog([])
    try {
      await postSSE(
        '/api/import',
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            record_id: recordId,
            project_id: selectedProjectId,
            items: items.map((i) => ({ id: i.id ?? '', draft: {
              project_name: i.project_name, title: i.title, description: i.description,
              priority: i.priority, estimated_hours: i.estimated_hours, start_at: i.start_at,
              end_at: i.end_at, type_id: i.type_id, assignee_name: i.assignee_name,
              state: i.state, solution_suggestion: i.solution_suggestion,
            } })),
          }),
        },
        (event, data) => {
          if (event === 'progress') {
            setField({ importProgress: { current: data.current ?? 0, total: data.total ?? items.length } })
            if (data.title) {
              setImportLog((log) => [...log, { title: data.title, status: data.status }])
            }
          } else if (event === 'complete') {
            const r = data.result
            setField({ importing: false, importDone: { success: r.success, failed: r.failed } })
          } else if (event === 'error') {
            setField({ importing: false })
            message.error(data.message)
          }
        },
      )
    } catch (e) {
      setField({ importing: false })
      message.error((e as Error).message)
    }
  }

  if (items.length === 0) {
    return (
      <Card>
        <Empty description="还没有分析结果">
          <Button type="primary" onClick={() => navigate('/import/upload')}>上传文档开始</Button>
          <Button style={{ marginLeft: 8 }} icon={<HistoryOutlined />} onClick={() => navigate('/records')}>从记录恢复</Button>
        </Empty>
      </Card>
    )
  }

  const projectOptions = [
    ...matches.map((m) => ({
      value: m.id,
      label: `${m.name}${m.match_type === 'exact' ? '（精确匹配）' : `（相似 ${(m.score * 100).toFixed(0)}%）`}`,
    })),
    ...(syncedProjects?.projects ?? [])
      .filter((p) => !matches.some((m) => m.id === p.ID))
      .map((p) => ({ value: p.ID, label: p.Name })),
  ]

  const done = wizard.importDone
  const dupCount = dupResults.filter((r) => r.match).length
  const totalHours = items.reduce((s, i) => s + (i.estimated_hours || 0), 0)

  return (
    <Row gutter={[16, 16]}>
      {/* 概要 + 项目选择 */}
      <Col span={24}>
        <Card size="small">
          <Row gutter={24} align="middle">
            <Col><Statistic title="工作项" value={items.length} /></Col>
            <Col><Statistic title="涉及项目" value={uniqueNames.length} /></Col>
            <Col><Statistic title="预估总工时" value={totalHours} suffix="h" /></Col>
            <Col>{dupResults.length > 0 && <Statistic title="疑似重复" value={dupCount} valueStyle={{ color: dupCount ? '#dc2626' : undefined }} />}</Col>
            <Col flex="auto" />
            <Col span={9}>
              <Space.Compact style={{ width: '100%' }}>
                <Select
                  style={{ width: '100%' }}
                  placeholder="选择目标项目（或新增）"
                  value={selectedProjectId}
                  onChange={onProjectChange}
                  status={!selectedProjectId ? 'warning' : undefined}
                  options={projectOptions}
                  showSearch
                  optionFilterProp="label"
                />
                <Button
                  icon={<ReloadOutlined />} onClick={() => void runMatch()}
                  title="重新推荐"
                />
              </Space.Compact>
              <div style={{ marginTop: 6 }}>
                <Input.Search
                  size="small"
                  placeholder="或输入新项目名，回车创建"
                  enterButton={<><DownloadOutlined /> 创建并导入</>}
                  onSearch={(v) => v.trim() && onProjectChange(`new:${v.trim()}`)}
                  disabled={wizard.importing}
                />
              </div>
            </Col>
          </Row>
          {uniqueNames.length > 0 && (
            <div style={{ marginTop: 10 }}>
              <Text type="secondary">AI 识别的项目分组：</Text>
              {uniqueNames.map((n) => {
                const hit = matchedByName.get(n)?.[0]
                return (
                  <Tag key={n} style={{ marginTop: 4 }} color={hit ? (hit.match_type === 'exact' ? 'green' : 'blue') : 'default'}>
                    {n}{hit ? ` → ${hit.name}` : '（未匹配，将全部导入所选项目）'}
                  </Tag>
                )
              })}
            </div>
          )}
        </Card>
      </Col>

      {/* 明细表 */}
      <Col span={24}>
        <Card
          size="small"
          title={<Space><AimOutlined style={{ color: '#4F46E5' }} /><Text strong>工作项明细</Text><Text type="secondary">（可直接编辑）</Text></Space>}
          extra={
            <Space>
              {dupResults.length > 0 && <Text type="secondary">查重完成</Text>}
              <Button
                type="primary"
                icon={<DownloadOutlined />}
                disabled={!selectedProjectId || wizard.importing || !!done}
                loading={wizard.importing}
                onClick={startImport}
              >
                导入到 PingCode
              </Button>
            </Space>
          }
        >
          {settings && !settings.pingcode.configured && (
            <Alert type="warning" showIcon style={{ marginBottom: 12 }}
              message="PingCode 未配置：无法导入。请在 config.yaml 填写 client_id / client_secret 后重启。" />
          )}
          <Table
            rowKey={(_, i) => String(i)}
            dataSource={items}
            size="middle"
            pagination={items.length > 10 ? { pageSize: 10, showSizeChanger: false } : false}
            columns={[
              {
                title: '查重', width: 92, dataIndex: 'match',
                render: (_, __, index) => <MatchBadge match={dupByIndex.get(index)} />,
              },
              { title: '标题', dataIndex: 'title', width: 260,
                render: (v, _, index) => <Input value={v} variant="borderless" onChange={(e) => patchItem(index, { title: e.target.value })} /> },
              { title: '项目', dataIndex: 'project_name', width: 130, ellipsis: true },
              { title: '类型', dataIndex: 'type_id', width: 110,
                render: (v, _, index) => <Select variant="borderless" value={v} style={{ width: '100%' }} options={TYPE_OPTIONS} onChange={(t) => patchItem(index, { type_id: t })} /> },
              { title: '优先级', dataIndex: 'priority', width: 130,
                render: (v, _, index) => <Select variant="borderless" value={v} style={{ width: '100%' }} options={PRIORITY_OPTIONS} onChange={(p) => patchItem(index, { priority: p })} /> },
              { title: '工时(h)', dataIndex: 'estimated_hours', width: 100,
                render: (v, _, index) => <InputNumber variant="borderless" min={0} value={v} onChange={(n) => patchItem(index, { estimated_hours: n ?? 0 })} /> },
              { title: '负责人', dataIndex: 'assignee_name', width: 110,
                render: (v, _, index) => <Input variant="borderless" value={v ?? ''} placeholder="默认自己" onChange={(e) => patchItem(index, { assignee_name: e.target.value })} /> },
              { title: '状态', dataIndex: 'state', width: 90,
                render: (v) => v ? <Tag style={{ margin: 0 }}>{v}</Tag> : <Tag style={{ margin: 0 }} color="default">待办</Tag> },
            ]}
            expandable={{
              rowExpandable: () => true,
              expandedRowRender: (r) => (
                <div style={{ padding: '4px 8px' }}>
                  <ParagraphBlock label="描述" text={r.description} />
                  <ParagraphBlock label="解决方案建议" text={r.solution_suggestion} />
                </div>
              ),
            }}
          />
        </Card>
      </Col>

      {/* 导入进度时间线 */}
      {(wizard.importing || done || importLog.length > 0) && (
        <Col span={24}>
          <Card size="small" title={<Text strong>导入进度</Text>} extra={done && (
            <Space>
              <CheckCircleTwoTone twoToneColor="#16a34a" />
              <Text>成功 {done.success}</Text>
              {done.failed > 0 && <><CloseCircleTwoTone twoToneColor="#dc2626" /><Text>失败 {done.failed}</Text></>}
              <Button size="small" icon={<HistoryOutlined />} onClick={() => navigate('/records')}>查看记录</Button>
            </Space>
          )}>
            {wizard.importing && (
              <Progress
                percent={Math.round((wizard.importProgress.current / Math.max(1, wizard.importProgress.total)) * 100)}
                size="small" style={{ maxWidth: 480, marginBottom: 12 }}
                format={() => `${wizard.importProgress.current}/${wizard.importProgress.total}`}
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

function ParagraphBlock({ label, text }: { label: string; text?: string }) {
  if (!text) return null
  return (
    <div style={{ marginBottom: 8 }}>
      <Text type="secondary" strong style={{ fontSize: 12 }}>{label}</Text>
      <div style={{ whiteSpace: 'pre-wrap', fontSize: 13, color: '#374151', marginTop: 2 }}>{text}</div>
    </div>
  )
}
