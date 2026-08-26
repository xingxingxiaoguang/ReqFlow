import { useState } from 'react'
import { Card, Table, Tag, Button, Space, Typography, Segmented, App, Empty } from 'antd'
import { InboxOutlined, UndoOutlined } from '@ant-design/icons'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { tasksApi } from '../api/tasks'
import type { ArchiveKind } from '../api/types'
import { TASK_STATUS_TAG, TYPE_LABEL } from './tasks/TaskDetail'

const { Text } = Typography

const TYPE_LABEL_DATASET: Record<string, string> = { requirement: '需求' }

/** 归档页：已归档任务与数据集的独立列表（不参与主业务循环），支持原样恢复 */
export default function Archives() {
  const qc = useQueryClient()
  const { message } = App.useApp()
  const [kind, setKind] = useState<ArchiveKind>('task')

  const { data, isLoading } = useQuery({
    queryKey: ['archives', kind],
    queryFn: () => tasksApi.listArchives({ kind, limit: 200 }),
  })

  const onRestore = async (k: ArchiveKind, id: string, name: string) => {
    try {
      await tasksApi.restoreArchive(k, id)
      qc.invalidateQueries({ queryKey: ['archives'] })
      qc.invalidateQueries({ queryKey: k === 'task' ? ['tasks'] : ['datasets'] })
      message.success(`「${name}」已恢复到主列表`)
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  return (
    <Card
      title={<Space><InboxOutlined style={{ color: '#6b7280' }} /><Text strong>归档</Text>
        <Text type="secondary" style={{ fontSize: 12, fontWeight: 400 }}>
          已归档数据不参与任务与数据集的主业务循环（列表 / 查重 / 检索 / 统计）；可随时恢复
        </Text></Space>}
      extra={
        <Segmented
          options={[
            { value: 'task', label: `任务${data?.tasks?.length ? ` (${data.tasks.length})` : ''}` },
            { value: 'dataset', label: `数据集${data?.datasets?.length ? ` (${data.datasets.length})` : ''}` },
          ]}
          value={kind}
          onChange={(v) => setKind(v as ArchiveKind)}
        />
      }
    >
      {kind === 'task' ? (
        <Table
          rowKey="ID" size="middle" loading={isLoading}
          dataSource={data?.tasks ?? []}
          locale={{ emptyText: <Empty description="没有已归档的任务" /> }}
          pagination={false}
          columns={[
            {
              title: '任务', dataIndex: 'Title', render: (v, r) => (
                <Space size={8}>
                  <Text>{v}</Text>
                  <Tag color="purple">{TYPE_LABEL[r.Type] ?? r.Type}</Tag>
                </Space>
              ),
            },
            {
              title: '归档时状态', dataIndex: 'Status', width: 120,
              render: (v) => <Tag color={TASK_STATUS_TAG[v]?.color ?? 'default'}>{TASK_STATUS_TAG[v]?.label ?? v}</Tag>,
            },
            {
              title: '明细', dataIndex: 'ItemsCount', width: 80, align: 'center',
              render: (v) => v || <Text type="secondary">—</Text>,
            },
            {
              title: '数据集产出', dataIndex: 'OutputDatasetID', width: 100, align: 'center',
              render: (v) => v ? <Tag color="green">已产出</Tag> : <Text type="secondary">—</Text>,
            },
            { title: '归档时间', dataIndex: 'ArchivedAt', width: 160, render: (v) => new Date(v).toLocaleString('zh-CN') },
            {
              title: '操作', width: 100,
              render: (_, r) => (
                <Button size="small" icon={<UndoOutlined />} onClick={() => onRestore('task', r.ID, r.Title)}>恢复</Button>
              ),
            },
          ]}
        />
      ) : (
        <Table
          rowKey="ID" size="middle" loading={isLoading}
          dataSource={data?.datasets ?? []}
          locale={{ emptyText: <Empty description="没有已归档的数据集" /> }}
          pagination={false}
          columns={[
            {
              title: '数据集', dataIndex: 'Name', render: (v, r) => (
                <Space size={8}>
                  <Text>{v}</Text>
                  <Tag color="purple">{TYPE_LABEL_DATASET[r.Type] ?? r.Type}</Tag>
                </Space>
              ),
            },
            { title: '条目', dataIndex: 'ItemCount', width: 90, align: 'center' },
            {
              title: '状态', dataIndex: 'Status', width: 100,
              render: (v) => <Tag color={v === 'ready' ? 'green' : 'gold'}>{v === 'ready' ? '已发布' : '写入中'}</Tag>,
            },
            { title: '创建时间', dataIndex: 'CreatedAt', width: 160, render: (v) => new Date(v).toLocaleString('zh-CN') },
            { title: '归档时间', dataIndex: 'ArchivedAt', width: 160, render: (v) => new Date(v).toLocaleString('zh-CN') },
            {
              title: '操作', width: 100,
              render: (_, r) => (
                <Button size="small" icon={<UndoOutlined />} onClick={() => onRestore('dataset', r.ID, r.Name)}>恢复</Button>
              ),
            },
          ]}
        />
      )}
    </Card>
  )
}
