import { Card, Col, Row, Statistic, Typography, Button, Tag, List, Empty, Space } from 'antd'
import {
  FileAddOutlined, BugOutlined, CloudSyncOutlined, CheckCircleTwoTone,
  WarningTwoTone, RightOutlined,
} from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../api/client'
import type { Overview, SettingsView } from '../api/types'

const { Text, Title } = Typography

const STATUS_TAG: Record<string, { color: string; label: string }> = {
  analyzed: { color: 'default', label: '已分析' },
  importing: { color: 'processing', label: '导入中' },
  success: { color: 'success', label: '导入成功' },
  partial_success: { color: 'warning', label: '部分成功' },
  failed: { color: 'error', label: '导入失败' },
}

/** 概览页：数据概览 + 配置健康度 + 快捷入口 */
export default function Overview() {
  const navigate = useNavigate()
  const { data: ov } = useQuery({ queryKey: ['overview'], queryFn: () => api.get<Overview>('/api/overview') })
  const { data: settings } = useQuery({ queryKey: ['settings'], queryFn: () => api.get<SettingsView>('/api/settings') })

  const issues: string[] = []
  if (settings) {
    if (!settings.llm.configured) issues.push('LLM 未配置 —— 需求分析不可用')
    if (!settings.pingcode.configured) issues.push('PingCode 未配置 —— 同步与导入不可用')
    if (!settings.embedding.configured) issues.push('Embedding 未配置 —— 语义匹配降级为仅精确匹配')
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
              <Button type="primary" size="large" icon={<FileAddOutlined />} onClick={() => navigate('/import/upload')}>
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

      <Col span={8}><Card><Statistic title="已同步项目" value={ov?.projects ?? 0} prefix={<CloudSyncOutlined style={{ color: '#4F46E5' }} />} /></Card></Col>
      <Col span={8}><Card><Statistic title="已同步工作项" value={ov?.workItems ?? 0} /></Card></Col>
      <Col span={8}><Card><Statistic title="导入批次" value={ov?.records ?? 0} /></Card></Col>

      <Col span={16}>
        <Card size="small" title={<Text strong>最近导入</Text>} extra={<Button type="link" size="small" onClick={() => navigate('/records')}>全部记录 <RightOutlined /></Button>}
          styles={{ body: { padding: 0 } }}>
          {ov?.recentRecords?.length ? (
            <List
              size="small" dataSource={ov.recentRecords}
              renderItem={(r) => (
                <List.Item style={{ padding: '10px 16px' }}>
                  <Space>
                    <Text strong style={{ fontSize: 13 }}>{r.FileName}</Text>
                    <Tag color={STATUS_TAG[r.Status]?.color}>{STATUS_TAG[r.Status]?.label}</Tag>
                    <Text type="secondary">{r.ImportedCount}/{r.ItemsCount} 项</Text>
                  </Space>
                </List.Item>
              )}
            />
          ) : (
            <div style={{ padding: 24 }}><Empty description="还没有导入记录" image={Empty.PRESENTED_IMAGE_SIMPLE} /></div>
          )}
        </Card>
      </Col>

      <Col span={8}>
        <Card size="small" title={<Text strong>下一步</Text>}>
          <Space direction="vertical" style={{ width: '100%' }}>
            <Button block icon={<CheckCircleTwoTone twoToneColor="#16a34a" />} style={{ justifyContent: 'flex-start' }} onClick={() => navigate('/import/upload')}>
              需求导入向导
            </Button>
            <Button block icon={<CloudSyncOutlined />} style={{ justifyContent: 'flex-start' }} onClick={() => navigate('/sync')}>
              同步 PingCode 数据
            </Button>
            <Button block icon={<BugOutlined />} style={{ justifyContent: 'flex-start' }} onClick={() => navigate('/bugs')}>
              Bug 处理（第二波）
            </Button>
          </Space>
        </Card>
      </Col>
    </Row>
  )
}
