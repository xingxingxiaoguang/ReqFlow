import { Card, Table, Tag, Button, Drawer, Descriptions, Modal, Typography, Space, App } from 'antd'
import { FileSearchOutlined, RollbackOutlined } from '@ant-design/icons'
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../api/client'
import type { ImportRecord, DraftItem } from '../api/types'
import { useImportWizard } from '../stores/importWizard'

const { Text } = Typography

const STATUS_TAG: Record<string, { color: string; label: string }> = {
  analyzed: { color: 'default', label: '已分析' },
  importing: { color: 'processing', label: '导入中' },
  success: { color: 'success', label: '导入成功' },
  partial_success: { color: 'warning', label: '部分成功' },
  failed: { color: 'error', label: '导入失败' },
}

/** 导入记录页：历史、明细、原文、恢复（回填向导继续编辑导入） */
export default function Records() {
  const navigate = useNavigate()
  const { message } = App.useApp()
  const restore = useImportWizard((s) => s.restoreFromRecord)
  const reset = useImportWizard((s) => s.reset)

  const { data, refetch } = useQuery({
    queryKey: ['records'],
    queryFn: () => api.get<{ records: ImportRecord[] }>('/api/records?limit=50'),
  })
  const [openId, setOpenId] = useState<string>()
  const { data: detail } = useQuery({
    queryKey: ['record', openId],
    queryFn: () => api.get<{ record: ImportRecord; items: DraftItem[] }>(`/api/records/${openId}`),
    enabled: !!openId,
  })
  const { data: source, refetch: refetchSource } = useQuery({
    queryKey: ['recordSource', openId],
    queryFn: () => api.get<{ text: string }>(`/api/records/${openId}/source`),
    enabled: false,
  })
  const [sourceOpen, setSourceOpen] = useState(false)

  const onRestore = (recordId: string) => {
    if (!detail || detail.record.ID !== recordId) {
      message.info('请先打开该记录的明细')
      return
    }
    reset()
    restore(recordId, detail.items)
    navigate('/import/result')
  }

  return (
    <Card title={<Text strong>导入记录</Text>} extra={<Button onClick={() => refetch()}>刷新</Button>}>
      <Table
        rowKey="ID"
        size="middle"
        dataSource={data?.records ?? []}
        locale={{ emptyText: '暂无记录，去「需求导入」开始第一次分析' }}
        columns={[
          { title: '文档', dataIndex: 'FileName', render: (v) => <Text strong>{v}</Text> },
          {
            title: '状态', dataIndex: 'Status', width: 110,
            render: (v) => <Tag color={STATUS_TAG[v]?.color ?? 'default'}>{STATUS_TAG[v]?.label ?? v}</Tag>,
          },
          { title: '工作项', dataIndex: 'ItemsCount', width: 90, align: 'center' },
          { title: '成功', dataIndex: 'ImportedCount', width: 80, align: 'center', render: (v) => <Text type="success">{v}</Text> },
          { title: '失败', dataIndex: 'FailedCount', width: 80, align: 'center', render: (v) => (v ? <Text type="danger">{v}</Text> : <Text type="secondary">0</Text>) },
          { title: '目标项目', dataIndex: 'TargetProjectName', width: 130, ellipsis: true, render: (v) => v || '—' },
          { title: '时间', dataIndex: 'CreatedAt', width: 170, render: (v) => new Date(v).toLocaleString('zh-CN') },
          {
            title: '操作', width: 200,
            render: (_, r) => (
              <Space>
                <Button size="small" icon={<FileSearchOutlined />} onClick={() => setOpenId(r.ID)}>明细</Button>
                <Button size="small" icon={<RollbackOutlined />} onClick={() => onRestore(r.ID)}>恢复</Button>
              </Space>
            ),
          },
        ]}
      />

      <Drawer
        title={detail?.record.FileName ?? '明细'}
        width={720} open={!!openId} onClose={() => setOpenId(undefined)}
        extra={
          <Space>
            <Button size="small" onClick={async () => { setSourceOpen(true); await refetchSource() }}>查看原文</Button>
            <Button size="small" type="primary" onClick={() => openId && onRestore(openId)}>恢复到向导</Button>
          </Space>
        }
      >
        {detail && (
          <>
            <Descriptions size="small" column={2} style={{ marginBottom: 12 }}>
              <Descriptions.Item label="状态"><Tag color={STATUS_TAG[detail.record.Status]?.color}>{STATUS_TAG[detail.record.Status]?.label}</Tag></Descriptions.Item>
              <Descriptions.Item label="导入">{detail.record.ImportedCount} 成功 / {detail.record.FailedCount} 失败</Descriptions.Item>
            </Descriptions>
            <Table
              rowKey={(_, i) => String(i)}
              size="small"
              dataSource={detail.items}
              pagination={false}
              scroll={{ y: 420 }}
              columns={[
                { title: '标题', dataIndex: 'title' },
                { title: '项目', dataIndex: 'project_name', width: 110, ellipsis: true },
                { title: '类型', dataIndex: 'type_id', width: 80 },
                { title: '优先级', dataIndex: 'priority', width: 90 },
                {
                  title: '结果', dataIndex: 'status', width: 90,
                  render: (v) => (v === 'success' ? <Tag color="green">成功</Tag> : v === 'failed' ? <Tag color="red">失败</Tag> : <Tag>待导入</Tag>),
                },
              ]}
            />
          </>
        )}
      </Drawer>

      <Modal
        title="分析原文" open={sourceOpen} onCancel={() => setSourceOpen(false)} footer={null} width={760}
      >
        <pre style={{ maxHeight: 480, overflow: 'auto', fontSize: 12.5, whiteSpace: 'pre-wrap' }}>
          {source?.text ?? '加载中…'}
        </pre>
      </Modal>
    </Card>
  )
}
