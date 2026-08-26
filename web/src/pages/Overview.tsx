import { Card, Col, Row, Statistic, Typography, Button, Tag, List, Empty, Space } from 'antd'
import {
  FileAddOutlined, DatabaseOutlined, WarningTwoTone, RightOutlined,
} from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../api/client'
import type { Overview, SettingsView, TaskStatus } from '../api/types'

const { Text, Title } = Typography

const TASK_STATUS_TAG: Record<TaskStatus, { color: string; label: string }> = {
  pending: { color: 'default', label: '待开始' },
  running: { color: 'processing', label: '进行中' },
  awaiting: { color: 'warning', label: '等待确认' },
  paused: { color: 'gold', label: '已暂停' },
  succeeded: { color: 'success', label: '成功' },
  failed: { color: 'error', label: '失败' },
}

const DATASET_TYPE_LABEL: Record<string, string> = { requirement: '需求' }

/** 概览页：数据概览 + 配置健康度 + 快捷入口 */
export default function Overview() {
  const navigate = useNavigate()
  const { data: ov } = useQuery({ queryKey: ['overview'], queryFn: () => api.get<Overview>('/api/overview') })
  const { data: settings } = useQuery({ queryKey: ['settings'], queryFn: () => api.get<SettingsView>('/api/settings') })

  const issues: string[] = []
  if (settings) {
    if (!settings.llm.configured) issues.push('LLM 未配置 —— 需求分析不可用')
    if (!settings.embedding.configured) issues.push('Embedding 未配置 —— 语义查重与 agent 查证降级为仅精确匹配')
    if (!settings.mineru.configured) issues.push('MinerU 未配置 —— PDF 解析不可用')
  }

  return (
    <Row gutter={[16, 16]}>
      <Col span={24}>
        <Card>
          <Row gutter={16} align="middle">
            <Col flex="auto">
              <Title level={4} style={{ margin: 0 }}>
                {settings?.workspaceName ?? 'ReqFlow'} · 需求与缺陷导入工作台
              </Title>
              <Text type="secondary">上传需求文档 → AI 解析结构化工作项 → 匹配查重 → 批量建单到协作平台</Text>
            </Col>
            <Col>
              <Button type="primary" size="large" icon={<FileAddOutlined />} onClick={() => navigate('/tasks/new')}>
                开始导入需求
              </Button>
            </Col>
          </Row>
        </Card>
      </Col>

      {issues.length > 0 && (
        <Col span={24}>
          <Card size="small">
            <Space direction="vertical" size={4}>
              {issues.map((i) => (
                <Space key={i}><WarningTwoTone twoToneColor="#f59e0b" /><Text style={{ fontSize: 13 }}>{i}</Text>
                  <Button type="link" size="small" onClick={() => navigate('/settings')}>去查看</Button>
                </Space>
              ))}
            </Space>
          </Card>
        </Col>
      )}

      <Col span={8}><Card><Statistic title="数据集" value={ov?.datasets ?? 0} prefix={<DatabaseOutlined style={{ color: '#4F46E5' }} />} /></Card></Col>
      <Col span={8}><Card><Statistic title="数据集条目" value={ov?.datasetItems ?? 0} /></Card></Col>
      <Col span={8}><Card><Statistic title="任务" value={ov?.tasks ?? 0} /></Card></Col>

      <Col span={16}>
        <Card size="small" title={<Text strong>最近任务</Text>} extra={<Button type="link" size="small" onClick={() => navigate('/tasks')}>全部任务 <RightOutlined /></Button>}
          styles={{ body: { padding: 0 } }}>
          {ov?.recentTasks?.length ? (
            <List
              size="small" dataSource={ov.recentTasks}
              renderItem={(r) => (
                <List.Item
                  style={{ padding: '10px 16px', cursor: 'pointer' }}
                  onClick={() => navigate(`/tasks/${r.ID}`)}
                >
                  <Space>
                    <Text strong style={{ fontSize: 13 }}>{r.Title}</Text>
                    <Tag color={TASK_STATUS_TAG[r.Status]?.color}>{TASK_STATUS_TAG[r.Status]?.label}</Tag>
                    {r.OutputDatasetID && <Tag color="purple">已产出数据集</Tag>}
                  </Space>
                </List.Item>
              )}
            />
          ) : (
            <div style={{ padding: 24 }}><Empty description="还没有任务" image={Empty.PRESENTED_IMAGE_SIMPLE} /></div>
          )}
        </Card>
      </Col>

      <Col span={8}>
        <Card size="small" title={<Text strong>最近数据集</Text>} extra={<Button type="link" size="small" onClick={() => navigate('/datasets')}>全部数据集 <RightOutlined /></Button>}
          styles={{ body: { padding: 0 } }}>
          {ov?.recentDatasets?.length ? (
            <List
              size="small" dataSource={ov.recentDatasets}
              renderItem={(r) => (
                <List.Item
                  style={{ padding: '10px 16px', cursor: 'pointer' }}
                  onClick={() => navigate('/datasets')}
                >
                  <Space>
                    <DatabaseOutlined style={{ color: '#7c3aed' }} />
                    <Text strong style={{ fontSize: 13 }}>{r.Name}</Text>
                    <Tag color="purple">{DATASET_TYPE_LABEL[r.Type] ?? r.Type}</Tag>
                    <Text type="secondary">{r.ItemCount} 条</Text>
                  </Space>
                </List.Item>
              )}
            />
          ) : (
            <div style={{ padding: 24 }}><Empty description="还没有数据集" image={Empty.PRESENTED_IMAGE_SIMPLE} /></div>
          )}
        </Card>
      </Col>
    </Row>
  )
}
