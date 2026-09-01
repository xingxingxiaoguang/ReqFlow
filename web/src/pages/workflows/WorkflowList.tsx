import { Button, Card, Empty, Space, Table, Tag, Typography } from 'antd'
import { PlusOutlined, SettingOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { workflowsApi } from '../../api/workflows'

export default function WorkflowList() {
  const navigate = useNavigate()
  const query = useQuery({ queryKey: ['workflows'], queryFn: workflowsApi.list })
  const workflows = query.data?.workflows ?? []
  return (
    <Card
      title={<Space direction="vertical" size={0}><Typography.Title level={3} style={{ margin: 0 }}>线性工作流</Typography.Title><Typography.Text type="secondary">连接定义顺序，规则随流程发布</Typography.Text></Space>}
      extra={<Button type="primary" icon={<PlusOutlined />} onClick={() => navigate('/workflows/new')}>新建工作流</Button>}
      style={{ margin: 24 }}
    >
      <Table
        rowKey="id"
        loading={query.isLoading}
        dataSource={workflows}
        pagination={false}
        locale={{ emptyText: <Empty description="还没有新的线性工作流"><Button type="primary" onClick={() => navigate('/workflows/new')}>从空白开始</Button></Empty> }}
        columns={[
          { title: '工作流', render: (_: unknown, item: typeof workflows[number]) => <Space direction="vertical" size={0}><Typography.Text strong>{item.name}</Typography.Text><Typography.Text type="secondary">{item.key}</Typography.Text></Space> },
          { title: 'Draft revision', dataIndex: 'revision', render: (revision: number) => <Tag color="blue">{revision}</Tag> },
          { title: '状态', render: (_: unknown, item: typeof workflows[number]) => item.active_revision_id ? <Tag color="green">已发布</Tag> : <Tag>编辑中</Tag> },
          { title: '操作', width: 180, render: (_: unknown, item: typeof workflows[number]) => <Button icon={<SettingOutlined />} onClick={() => navigate(`/workflows/${item.id}/design`)}>打开设计器</Button> },
        ]}
      />
    </Card>
  )
}
