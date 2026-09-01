import { Alert, App, Button, Card, Empty, Form, Input, Space, Table, Tag, Typography } from 'antd'
import { PlusOutlined, ReloadOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { workflowRunsApi, type WorkflowRunSnapshot } from '../../api/workflowRuns'

const statusColor: Record<string, string> = {
  queued: 'blue', running: 'processing', pausing: 'gold', paused: 'orange', awaiting_manual_completion: 'purple',
  failed: 'red', succeeded: 'green',
}

export default function WorkflowRuns() {
  const navigate = useNavigate()
  const { message } = App.useApp()
  const client = useQueryClient()
  const [form] = Form.useForm<{ revision_id: string; inputs: string }>()
  const [showCreate, setShowCreate] = useState(false)
  const query = useQuery({ queryKey: ['workflow-runs'], queryFn: () => workflowRunsApi.list() })
  const create = useMutation({
    mutationFn: async (values: { revision_id: string; inputs: string }) => {
      const inputs = JSON.parse(values.inputs || '[]') as Array<{ port: string; resource_type: string; resource_id: string; boundary?: unknown }>
      return workflowRunsApi.create({ revision_id: values.revision_id.trim(), inputs })
    },
    onSuccess: (run) => { void client.invalidateQueries({ queryKey: ['workflow-runs'] }); setShowCreate(false); form.resetFields(); navigate(`/runs/${run.run.id}`) },
    onError: (error) => message.error((error as Error).message),
  })
  const runs = query.data?.runs ?? []
  return (
    <div style={{ padding: 24, maxWidth: 1240, margin: '0 auto' }}>
      <Card
        title={<Space direction="vertical" size={0}><Typography.Title level={3} style={{ margin: 0 }}>运行目录</Typography.Title><Typography.Text type="secondary">所有运行固定在不可变 Revision 上，节点按连接顺序推进</Typography.Text></Space>}
        extra={<Space><Button icon={<ReloadOutlined />} onClick={() => void query.refetch()}>刷新</Button><Button type="primary" icon={<PlusOutlined />} onClick={() => setShowCreate((value) => !value)}>创建运行</Button></Space>}
      >
        {showCreate && <Card type="inner" title="从 Revision 创建运行" style={{ marginBottom: 18 }}>
          <Form form={form} layout="vertical" onFinish={(values) => create.mutate(values)} initialValues={{ inputs: '[]' }}>
            <Form.Item name="revision_id" label="Revision ID" rules={[{ required: true, message: '请输入 Revision ID' }]}><Input placeholder="已发布 Revision 的 ID" /></Form.Item>
            <Form.Item name="inputs" label="流程输入 JSON 数组" rules={[{ required: true, message: '请输入 [] 或输入绑定数组' }]}><Input.TextArea rows={4} placeholder='[{"port":"assets","resource_type":"asset_set","resource_id":"..."}]' /></Form.Item>
            <Button type="primary" htmlType="submit" loading={create.isPending}>创建</Button>
          </Form>
        </Card>}
        {query.isError && <Alert type="error" message={(query.error as Error).message} showIcon />}
        <Table<WorkflowRunSnapshot>
          rowKey={(item) => item.run.id}
          loading={query.isLoading}
          dataSource={runs}
          pagination={false}
          locale={{ emptyText: <Empty description="还没有运行"><Button type="primary" onClick={() => setShowCreate(true)}>创建第一个运行</Button></Empty> }}
          columns={[
            { title: 'Revision', render: (_: unknown, item) => <Space direction="vertical" size={0}><Typography.Text strong>{item.run.revision.name}</Typography.Text><Typography.Text type="secondary">{item.run.revision.key} · {item.run.revision_id}</Typography.Text></Space> },
            { title: '状态', dataIndex: ['run', 'status'], render: (status: string) => <Tag color={statusColor[status] ?? 'default'}>{status}</Tag> },
            { title: '节点', render: (_: unknown, item) => `${item.nodes.filter((node) => node.status === 'succeeded').length}/${item.nodes.length}` },
            { title: '创建时间', dataIndex: ['run', 'created_at'], render: (value: string) => new Date(value).toLocaleString() },
            { title: '操作', render: (_: unknown, item) => <Button onClick={() => navigate(`/runs/${item.run.id}`)}>查看运行</Button> },
          ]}
        />
      </Card>
    </div>
  )
}
