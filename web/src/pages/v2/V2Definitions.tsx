import { useMemo, useState } from 'react'
import { Button, Card, Empty, Popconfirm, Space, Table, Tag, Typography } from 'antd'
import { DeleteOutlined, EditOutlined, EyeOutlined, PlusOutlined, RocketOutlined } from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { v2CatalogApi } from '../../api/v2/catalog'
import type { V2TaskDefinition } from '../../api/v2/types'
import {
  listLocalWorkflowDrafts, removeLocalWorkflowDraft, type LocalWorkflowDraft,
} from './workflowDrafts'

const { Paragraph, Text, Title } = Typography

type FlowListItem =
  | { id: string; kind: 'draft'; draft: LocalWorkflowDraft }
  | { id: string; kind: 'published'; definition: V2TaskDefinition }

export default function V2Definitions() {
  const navigate = useNavigate()
  const [localDrafts, setLocalDrafts] = useState(listLocalWorkflowDrafts)
  const definitions = useQuery({
    queryKey: ['v2-definitions'],
    queryFn: () => v2CatalogApi.listDefinitions({ status: 'active', limit: 200 }),
  })
  const rows = useMemo<FlowListItem[]>(() => [
    ...localDrafts.map((draft) => ({ id: `draft-${draft.id}`, kind: 'draft' as const, draft })),
    ...(definitions.data?.task_definitions ?? [])
      .filter((definition) => definition.status === 'active')
      .map((definition) => ({ id: definition.id, kind: 'published' as const, definition })),
  ], [definitions.data, localDrafts])

  const columns: ColumnsType<FlowListItem> = [
    {
      title: '流程',
      render: (_, item) => {
        const flow = item.kind === 'draft' ? item.draft.definition : item.definition
        return <Space direction="vertical" size={1}>
          <Text strong>{flow.name || '未命名流程'}</Text>
          <Text type="secondary">{flow.description || flow.key}</Text>
        </Space>
      },
    },
    {
      title: '状态', width: 100,
      render: (_, item) => item.kind === 'draft'
        ? <Tag color="gold">草稿</Tag>
        : <Tag color="success">已发布</Tag>,
    },
    {
      title: '步骤', width: 90, align: 'center',
      render: (_, item) => (item.kind === 'draft' ? item.draft.definition.steps : item.definition.steps).length,
    },
    {
      title: '输入', width: 90, align: 'center',
      render: (_, item) => Object.keys(item.kind === 'draft' ? item.draft.definition.input_ports : item.definition.input_ports).length,
    },
    {
      title: '最近更新', width: 180,
      render: (_, item) => new Date(item.kind === 'draft' ? item.draft.saved_at : item.definition.updated_at).toLocaleString('zh-CN'),
    },
    {
      title: '操作', width: 250,
      render: (_, item) => item.kind === 'draft' ? <Space>
        <Button type="primary" size="small" icon={<EditOutlined />} onClick={() => navigate(`/definitions/new?draft=${item.draft.id}`)}>继续编辑</Button>
        <Popconfirm title="删除这份草稿？" onConfirm={() => {
          removeLocalWorkflowDraft(item.draft.id)
          setLocalDrafts(listLocalWorkflowDrafts())
        }}>
          <Button danger size="small" icon={<DeleteOutlined />}>删除</Button>
        </Popconfirm>
      </Space> : <Space>
        <Button size="small" icon={<EyeOutlined />} onClick={() => navigate(`/definitions/${item.definition.id}`)}>详情</Button>
        <Button type="primary" size="small" icon={<RocketOutlined />} onClick={() => navigate(`/tasks/new?definition_id=${item.definition.id}`)}>创建任务</Button>
      </Space>,
    },
  ]

  return <Card
    title={<div>
      <Title level={4} style={{ margin: 0 }}>流程管理</Title>
      <Paragraph type="secondary" style={{ margin: 0 }}>草稿和已发布流程统一管理；规则直接在对应步骤内选择或创建。</Paragraph>
    </div>}
    extra={<Button type="primary" icon={<PlusOutlined />} onClick={() => navigate('/definitions/new')}>创建流程</Button>}
  >
    <Table
      rowKey="id"
      loading={definitions.isLoading}
      dataSource={rows}
      columns={columns}
      pagination={false}
      locale={{ emptyText: <Empty description="还没有流程"><Button type="primary" onClick={() => navigate('/definitions/new')}>创建第一个流程</Button></Empty> }}
    />
  </Card>
}
