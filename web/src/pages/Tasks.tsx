import { useState } from 'react'
import { Card, Table, Tag, Button, Space, Typography, Segmented, App, Popconfirm } from 'antd'
import { FileAddOutlined, FileSearchOutlined, PauseCircleOutlined, PlayCircleOutlined, CheckCircleOutlined, InboxOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { tasksApi } from '../api/tasks'
import type { TaskStatus } from '../api/types'
import { TASK_STATUS_TAG, TYPE_LABEL } from './tasks/TaskDetail'

const { Text } = Typography

const FILTERS: { value?: TaskStatus; label: string }[] = [
  { label: '全部' },
  { value: 'running', label: '进行中' },
  { value: 'awaiting', label: '等待确认' },
  { value: 'paused', label: '已暂停' },
  { value: 'succeeded', label: '成功' },
]

/** 任务列表页（替代原导入记录）：状态筛选 + 生命周期操作 + 详情入口 */
export default function Tasks() {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { message } = App.useApp()
  const [status, setStatus] = useState<TaskStatus | undefined>()

  const { data } = useQuery({
    queryKey: ['tasks', status],
    queryFn: () => tasksApi.list({ status, limit: 100 }),
  })

  const lifecycle = async (fn: () => Promise<unknown>, okMsg: string) => {
    try {
      await fn()
      qc.invalidateQueries({ queryKey: ['tasks'] })
      if (okMsg) message.success(okMsg)
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const onArchive = async (id: string, title: string) => {
    try {
      await tasksApi.archiveTask(id)
      qc.invalidateQueries({ queryKey: ['tasks'] })
      qc.invalidateQueries({ queryKey: ['archives'] })
      message.success(`「${title}」已归档（可在归档页恢复）`)
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  return (
    <Card
      title={<Text strong>任务管理</Text>}
      extra={
        <Space>
          <Segmented
            options={FILTERS.map((f) => f.label)}
            value={FILTERS.find((f) => f.value === status)?.label ?? '全部'}
            onChange={(v) => {
              const f = FILTERS.find((x) => x.label === v)
              setStatus(f?.value)
            }}
          />
          <Button type="primary" icon={<FileAddOutlined />} onClick={() => navigate('/tasks/new')}>新建需求导入</Button>
        </Space>
      }
    >
      <Table
        rowKey="ID"
        size="middle"
        dataSource={data?.tasks ?? []}
        locale={{ emptyText: '暂无任务，点「新建需求导入」开始' }}
        columns={[
          {
            title: '任务', dataIndex: 'Title', render: (v, r) => (
              <Space size={8}>
                <Text strong>{v}</Text>
                <Tag color="purple">{TYPE_LABEL[r.Type] ?? r.Type}</Tag>
              </Space>
            ),
          },
          {
            title: '状态', dataIndex: 'Status', width: 110,
            render: (v) => <Tag color={TASK_STATUS_TAG[v]?.color ?? 'default'}>{TASK_STATUS_TAG[v]?.label ?? v}</Tag>,
          },
          {
            title: '步骤', width: 100, render: (_, r) => {
              const stepNames = ['上传解析', '确认解析', 'AI 分析', '生成数据集']
              return <Text type="secondary" style={{ fontSize: 12 }}>{r.CurrentStep > 0 ? stepNames[r.CurrentStep - 1] : '未开始'}</Text>
            },
          },
          {
            title: '数据集', width: 100, align: 'center', render: (_, r) => (
              r.OutputDatasetID
                ? <Tag color="green">已产出</Tag>
                : <Text type="secondary" style={{ fontSize: 12 }}>—</Text>
            ),
          },
          { title: '创建时间', dataIndex: 'CreatedAt', width: 160, render: (v) => new Date(v).toLocaleString('zh-CN') },
          {
            title: '操作', width: 200,
            render: (_, r) => (
              <Space>
                <Button size="small" icon={<FileSearchOutlined />} onClick={() => navigate(`/tasks/${r.ID}`)}>详情</Button>
                {r.Status === 'running' && (
                  <Button size="small" icon={<PauseCircleOutlined />} onClick={() => lifecycle(() => tasksApi.pause(r.ID), '已暂停')}>暂停</Button>
                )}
                {r.Status === 'paused' && (
                  <Button size="small" type="primary" icon={<PlayCircleOutlined />} onClick={() => lifecycle(() => tasksApi.resume(r.ID), '已继续')}>继续</Button>
                )}
                {r.Status === 'awaiting' && (
                  <Button size="small" icon={<CheckCircleOutlined />} onClick={() => lifecycle(() => tasksApi.complete(r.ID), '已完成')}>完成</Button>
                )}
                {r.Status !== 'running' && (
                  <Popconfirm
                    title="归档该任务？"
                    description="任务将移出主列表（含明细快照），不影响其已产出的数据集；可在归档页恢复。"
                    okText="归档"
                    okButtonProps={{ danger: true }}
                    onConfirm={() => onArchive(r.ID, r.Title)}
                  >
                    <Button size="small" danger type="text" icon={<InboxOutlined />}>归档</Button>
                  </Popconfirm>
                )}
              </Space>
            ),
          },
        ]}
      />
    </Card>
  )
}
