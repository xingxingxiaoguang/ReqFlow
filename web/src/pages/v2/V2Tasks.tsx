import { useMemo, useState } from 'react'
import {
  App, Button, Card, Drawer, Empty, Segmented, Space, Table, Tag, Typography,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import {
  DownloadOutlined, FileDoneOutlined, FileSearchOutlined, PauseCircleOutlined,
  PlayCircleOutlined, PlusOutlined, RocketOutlined,
} from '@ant-design/icons'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { v2TasksApi } from '../../api/v2/tasks'
import type { V2Artifact, V2Task, V2TaskStatus } from '../../api/v2/types'
import { v2CatalogApi } from '../../api/v2/catalog'
import { TaskStatusTag } from './status'

const { Link, Paragraph, Text, Title } = Typography

const FILTERS: { label: string; value?: V2TaskStatus }[] = [
  { label: '全部' }, { label: '待审核', value: 'awaiting' },
  { label: '运行中', value: 'running' }, { label: '已暂停', value: 'paused' },
  { label: '失败', value: 'failed' }, { label: '已完成', value: 'succeeded' },
]

export default function V2Tasks() {
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { message } = App.useApp()
  const [status, setStatus] = useState<V2TaskStatus>()
  const [actingId, setActingId] = useState<string>()
  const [resultTask, setResultTask] = useState<V2Task>()
  const tasks = useQuery({ queryKey: ['v2-tasks', status], queryFn: () => v2TasksApi.list({ status, limit: 100 }) })
  const definitions = useQuery({ queryKey: ['v2-definitions'], queryFn: () => v2CatalogApi.listDefinitions({ limit: 200 }) })
  const artifacts = useQuery({ queryKey: ['v2-artifacts'], queryFn: v2CatalogApi.listArtifacts })
  const definitionById = new Map((definitions.data?.task_definitions ?? []).map((item) => [item.id, item]))
  const artifactsByTask = useMemo(() => {
    const grouped = new Map<string, V2Artifact[]>()
    for (const artifact of artifacts.data?.artifacts ?? []) {
      const current = grouped.get(artifact.source_task_id) ?? []
      current.push(artifact)
      grouped.set(artifact.source_task_id, current)
    }
    for (const values of grouped.values()) values.sort((a, b) => b.created_at.localeCompare(a.created_at))
    return grouped
  }, [artifacts.data])
  const selectedArtifacts = resultTask ? artifactsByTask.get(resultTask.id) ?? [] : []

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
      title: '任务',
      render: (_, task) => <Space direction="vertical" size={2}>
        <Text strong>{task.title}</Text>
        {task.batch_id && <Space size={6} wrap>
          <Tag color="cyan">批量任务 {task.batch_ordinal}/{task.batch_size}</Tag>
          <Text type="secondary">独立处理：{task.source_filename}</Text>
        </Space>}
        <Text type="secondary">{task.id}</Text>
      </Space>,
    },
    {
      title: '来源流程', width: 210,
      render: (_, task) => {
        const definition = definitionById.get(task.definition_id)
        return <Space direction="vertical" size={1}><Text>{definition?.name ?? task.type}</Text><Text type="secondary">{task.definition_id.slice(0, 8)}</Text></Space>
      },
    },
    { title: '状态', dataIndex: 'status', width: 105, render: (value: V2TaskStatus) => <TaskStatusTag status={value} /> },
    {
      title: '执行结果', width: 230,
      render: (_, task) => {
        const results = artifactsByTask.get(task.id) ?? []
        if (!results.length) return <Text type="secondary">尚无业务制品</Text>
        return <Button type="link" style={{ padding: 0, height: 'auto' }} icon={<FileDoneOutlined />} onClick={() => setResultTask(task)}>
          {results[0].name}<Tag color="purple" style={{ marginInlineStart: 7 }}>{results.length}</Tag>
        </Button>
      },
    },
    { title: '更新时间', dataIndex: 'updated_at', width: 180, render: (value: string) => new Date(value).toLocaleString('zh-CN') },
    {
      title: '操作', width: 220,
      render: (_, task) => <Space wrap>
        <Button size="small" icon={<FileSearchOutlined />} onClick={() => navigate(`/tasks/${task.id}`)}>详情</Button>
        {task.status === 'pending' && <Button size="small" type="primary" loading={actingId === task.id} icon={<RocketOutlined />} onClick={() => void transition(task, 'start')}>启动</Button>}
        {task.status === 'running' && <Button size="small" loading={actingId === task.id} icon={<PauseCircleOutlined />} onClick={() => void transition(task, 'pause')}>暂停</Button>}
        {task.status === 'paused' && <Button size="small" type="primary" loading={actingId === task.id} icon={<PlayCircleOutlined />} onClick={() => void transition(task, 'resume')}>继续</Button>}
      </Space>,
    },
  ]

  return <>
    <Card
      title={<div><Title level={4} style={{ margin: 0 }}>任务管理</Title><Paragraph type="secondary" style={{ margin: 0 }}>跟踪流程执行，并在任务上直接查看生成的业务制品。</Paragraph></div>}
      extra={<Space>
        <Segmented options={FILTERS.map((item) => item.label)} value={FILTERS.find((item) => item.value === status)?.label ?? '全部'} onChange={(value) => setStatus(FILTERS.find((item) => item.label === value)?.value)} />
        <Button type="primary" icon={<PlusOutlined />} onClick={() => navigate('/tasks/new')}>创建任务</Button>
      </Space>}
    >
      <Table<V2Task>
        rowKey="id"
        loading={tasks.isLoading || artifacts.isLoading}
        dataSource={tasks.data?.tasks ?? []}
        columns={columns}
        pagination={false}
        locale={{ emptyText: tasks.error ? <Empty description={(tasks.error as Error).message} /> : <Empty description="还没有任务" /> }}
      />
    </Card>

    <Drawer
      width={760}
      open={Boolean(resultTask)}
      onClose={() => setResultTask(undefined)}
      title={resultTask ? `${resultTask.title} · 执行结果` : ''}
      extra={resultTask && <Button onClick={() => navigate(`/tasks/${resultTask.id}`)}>查看任务详情</Button>}
    >
      <Paragraph type="secondary">按生成时间倒序展示，最新业务制品位于最前。</Paragraph>
      {selectedArtifacts.length ? <Space direction="vertical" size={12} style={{ width: '100%' }}>
        {selectedArtifacts.map((artifact, index) => <Card
          key={artifact.id}
          size="small"
          title={<Space><Text strong>{artifact.name}</Text>{index === 0 && <Tag color="green">最新</Tag>}<Tag color="purple">{artifact.kind}</Tag></Space>}
          extra={<Link href={`/api/v2/artifacts/${artifact.id}/content`}><DownloadOutlined /> 下载</Link>}
        >
          <Space direction="vertical" size={3}>
            <Text type="secondary">生成于 {new Date(artifact.created_at).toLocaleString('zh-CN')}</Text>
            <Text type="secondary">内容哈希 <Text code>{artifact.content_hash.slice(0, 16)}</Text></Text>
          </Space>
        </Card>)}
      </Space> : <Empty description="该任务没有生成业务制品" />}
    </Drawer>
  </>
}
