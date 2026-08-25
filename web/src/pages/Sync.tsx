import { Card, Typography, Space, Button, Tag, Timeline, Row, Col, Statistic, List } from 'antd'
import { CloudSyncOutlined, ReloadOutlined } from '@ant-design/icons'
import { useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../api/client'
import { postSSE } from '../api/sse'
import type { Overview, SyncedProject, SyncedWorkItem } from '../api/types'

const { Text } = Typography

/** 数据同步页：连接状态 + SSE 同步日志 + 已同步项目/工作项浏览 */
export default function Sync() {
  const qc = useQueryClient()
  const [syncing, setSyncing] = useState(false)
  const [logs, setLogs] = useState<{ stage: string; message: string }[]>([])
  const [summary, setSummary] = useState<string>('')

  const { data: overview } = useQuery({ queryKey: ['overview'], queryFn: () => api.get<Overview>('/api/overview') })
  const { data: projects } = useQuery({
    queryKey: ['projects'],
    queryFn: () => api.get<{ projects: SyncedProject[] }>('/api/projects'),
  })
  const [projectFilter, setProjectFilter] = useState<string>()
  const { data: workItems } = useQuery({
    queryKey: ['workItems', projectFilter],
    queryFn: () => {
      const q = new URLSearchParams({ limit: '50', ...(projectFilter ? { project_id: projectFilter } : {}) })
      return api.get<{ items: SyncedWorkItem[]; total: number }>(`/api/work-items?${q}`)
    },
  })

  const runSync = async () => {
    setSyncing(true)
    setLogs([])
    setSummary('')
    try {
      await postSSE('/api/sync', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' }, (event, data) => {
        if (event === 'progress') {
          setLogs((l) => [...l, { stage: data.stage, message: data.message }])
        } else if (event === 'complete') {
          const r = data.result
          setSummary(`项目 ${r.projects}（+${r.addedProjects}/×${r.archivedProjects}）· 工作项 ${r.workItems}（+${r.addedWorkItems}/~${r.updatedWorkItems}/×${r.archivedWorkItems}）${r.semanticDisabled ? ' · 语义向量未启用' : ''}`)
        } else if (event === 'error') {
          setSummary(`同步失败：${data.message}`)
        }
      })
      await qc.invalidateQueries()
    } finally {
      setSyncing(false)
    }
  }

  return (
    <Row gutter={[16, 16]}>
      <Col span={24}>
        <Card
          title={<Space><CloudSyncOutlined style={{ color: '#4F46E5' }} /><Text strong>同步 PingCode 数据</Text></Space>}
          extra={
            <Button type="primary" icon={<ReloadOutlined />} loading={syncing} onClick={runSync}>
              {syncing ? '同步中…' : '立即同步'}
            </Button>
          }
        >
          <Row gutter={24}>
            <Col><Statistic title="已同步项目" value={overview?.projects ?? 0} /></Col>
            <Col><Statistic title="已同步工作项" value={overview?.workItems ?? 0} /></Col>
          </Row>
          {(logs.length > 0 || summary) && (
            <div style={{ marginTop: 16 }}>
              <Timeline
                items={[
                  ...logs.map((l) => ({ children: <Text type="secondary" style={{ fontSize: 13 }}>{l.message}</Text> })),
                  ...(summary ? [{ color: summary.includes('失败') ? 'red' : 'green', children: <Text strong style={{ fontSize: 13 }}>{summary}</Text> }] : []),
                ]}
              />
            </div>
          )}
          <Text type="secondary">
            同步采用增量策略：仅拉取远端有变更的项目/工作项并重建向量；平台侧已删除的条目本地软归档。首次使用请先同步，为项目匹配与查重建立语料库。
          </Text>
        </Card>
      </Col>

      <Col span={10}>
        <Card size="small" title={<Text strong>项目</Text>} styles={{ body: { padding: 0 } }}>
          <List
            size="small"
            style={{ maxHeight: 420, overflow: 'auto' }}
            dataSource={projects?.projects ?? []}
            locale={{ emptyText: '尚未同步任何项目' }}
            renderItem={(p) => (
              <List.Item
                style={{ cursor: 'pointer', padding: '8px 16px', background: projectFilter === p.ID ? '#eef2ff' : undefined }}
                onClick={() => setProjectFilter(projectFilter === p.ID ? undefined : p.ID)}
              >
                <List.Item.Meta title={<Text style={{ fontSize: 13 }}>{p.Name}</Text>} description={p.Description?.slice(0, 60) || '—'} />
              </List.Item>
            )}
          />
        </Card>
      </Col>

      <Col span={14}>
        <Card
          size="small"
          title={<Space><Text strong>工作项</Text>{projectFilter && <Tag color="geekblue">已按项目过滤 · 点击左侧项目取消</Tag>}</Space>}
          extra={<Text type="secondary">共 {workItems?.total ?? 0} 条</Text>}
          styles={{ body: { padding: 0 } }}
        >
          <List
            size="small"
            style={{ maxHeight: 420, overflow: 'auto' }}
            dataSource={workItems?.items ?? []}
            locale={{ emptyText: '该项目暂无工作项' }}
            renderItem={(w) => (
              <List.Item style={{ padding: '8px 16px' }}>
                <Space direction="vertical" size={0} style={{ width: '100%' }}>
                  <Space>
                    {w.Identifier && <Tag style={{ margin: 0 }}>{w.Identifier}</Tag>}
                    {w.Kind && <Tag style={{ margin: 0 }} color="purple">{w.Kind}</Tag>}
                    <Text strong style={{ fontSize: 13 }}>{w.Title}</Text>
                  </Space>
                </Space>
              </List.Item>
            )}
          />
        </Card>
      </Col>
    </Row>
  )
}
