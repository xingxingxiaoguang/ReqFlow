import { useState } from 'react'
import {
  App, Button, Card, Empty, Segmented, Space, Table, Tag, Typography,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import {
  FileSearchOutlined, PauseCircleOutlined, PlayCircleOutlined, PlusOutlined, RocketOutlined,
} from '@ant-design/icons'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { v2TasksApi } from '../../api/v2/tasks'
import type { V2Task, V2TaskStatus } from '../../api/v2/types'
import { TaskStatusTag } from './status'

const { Text } = Typography

const FILTERS: { label: string; value?: V2TaskStatus }[] = [
  { label: '全部' },
  { label: '待审核', value: 'awaiting' },
  { label: '运行中', value: 'running' },
  { label: '已暂停', value: 'paused' },
  { label: '失败', value: 'failed' },
  { label: '已完成', value: 'succeeded' },
]

export default function V2Tasks() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { message } = App.useApp()
  const [status, setStatus] = useState<V2TaskStatus>()
  const [actingId, setActingId] = useState<string>()
  const query = useQuery({
    queryKey: ['v2-tasks', status],
    queryFn: () => v2TasksApi.list({ status, limit: 100 }),
  })

  const transition = async (task: V2Task, action: 'start' | 'pause' | 'resume') => {
    setActingId(task.id)
    try {
      await v2TasksApi[action](task.id)
      await queryClient.invalidateQueries({ queryKey: ['v2-tasks'] })
      message.success(action === 'pause' ? '已请求暂停' : action === 'resume' ? '任务已继续' : '任务已启动')
    } catch (error) {
      message.error((error as Error).message)
    } finally {
      setActingId(undefined)
    }
  }

  const columns: ColumnsType<V2Task> = [
    {
      title: '任务', dataIndex: 'title',
      render: (title: string, task) => (
        <Space direction="vertical" size={2}>
          <Space><Text strong>{title}</Text><Tag color="purple">{task.type}</Tag></Space>
          <Text type="secondary" style={{ fontSize: 12 }}>{task.id}</Text>
        </Space>
      ),
    },
    { title: '状态', dataIndex: 'status', width: 110, render: (value: V2TaskStatus) => <TaskStatusTag status={value} /> },
    { title: '当前步骤', dataIndex: 'current_step', width: 100, align: 'center' },
    { title: '更新时间', dataIndex: 'updated_at', width: 180, render: (value: string) => new Date(value).toLocaleString('zh-CN') },
    {
      title: '操作', width: 240,
      render: (_, task) => (
        <Space wrap>
          <Button size="small" icon={<FileSearchOutlined />} onClick={() => navigate(`/tasks/${task.id}`)}>详情</Button>
          {task.status === 'pending' && (
            <Button size="small" type="primary" loading={actingId === task.id} icon={<RocketOutlined />} onClick={() => transition(task, 'start')}>启动</Button>
          )}
          {task.status === 'running' && (
            <Button size="small" loading={actingId === task.id} icon={<PauseCircleOutlined />} onClick={() => transition(task, 'pause')}>暂停</Button>
          )}
          {task.status === 'paused' && (
            <Button size="small" type="primary" loading={actingId === task.id} icon={<PlayCircleOutlined />} onClick={() => transition(task, 'resume')}>继续</Button>
          )}
        </Space>
      ),
    },
  ]

  return (
    <Card
      title={<Space><Text strong>无代码任务运行</Text><Tag color="geekblue">V2</Tag></Space>}
      extra={(
        <Space><Segmented
          options={FILTERS.map((item) => item.label)}
          value={FILTERS.find((item) => item.value === status)?.label ?? '全部'}
          onChange={(value) => setStatus(FILTERS.find((item) => item.label === value)?.value)}
        /><Button type="primary" icon={<PlusOutlined />} onClick={() => navigate('/tasks/new')}>创建任务</Button></Space>
      )}
    >
      <Table<V2Task>
        rowKey="id"
        loading={query.isLoading}
        dataSource={query.data?.tasks ?? []}
        columns={columns}
        pagination={false}
        locale={{
          emptyText: query.error
            ? <Empty description={(query.error as Error).message} />
            : <Empty description="暂无 V2 任务；任务由已发布的 TaskDefinition 创建" />,
        }}
      />
    </Card>
  )
}
