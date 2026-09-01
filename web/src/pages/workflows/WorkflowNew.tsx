import { App, Button, Card, Form, Input, Space, Typography } from 'antd'
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { workflowsApi, type WorkflowPort } from '../../api/workflows'

const defaultInputs: WorkflowPort[] = [{ name: 'input', label: '流程输入', resource_type: 'asset_set', required: true }]
const defaultOutputs: WorkflowPort[] = [{ name: 'result', label: '流程输出', resource_type: 'parsed_documents', required: true }]

export default function WorkflowNew() {
  const navigate = useNavigate()
  const { message } = App.useApp()
  const [saving, setSaving] = useState(false)
  const [form] = Form.useForm<{ key: string; name: string; description?: string }>()
  const submit = async (values: { key: string; name: string; description?: string }) => {
    setSaving(true)
    try {
      const result = await workflowsApi.create({ ...values, inputs: defaultInputs, outputs: defaultOutputs })
      message.success('工作流草稿已创建')
      navigate(`/workflows/${result.draft.id}/design`)
    } catch (error) {
      message.error((error as Error).message)
    } finally {
      setSaving(false)
    }
  }
  return (
    <Card style={{ maxWidth: 720, margin: '24px auto' }}>
      <Space direction="vertical" size={4} style={{ width: '100%', marginBottom: 24 }}>
        <Typography.Title level={3} style={{ margin: 0 }}>新建线性工作流</Typography.Title>
        <Typography.Text type="secondary">先定义流程边界，进入设计器后添加能力和内联规则。</Typography.Text>
      </Space>
      <Form form={form} layout="vertical" onFinish={submit} initialValues={{ key: 'new_workflow' }}>
        <Form.Item name="key" label="工作流编码" rules={[{ required: true }, { pattern: /^[a-z][a-z0-9_]{0,62}$/, message: '使用小写 snake_case' }]}><Input placeholder="product_import" /></Form.Item>
        <Form.Item name="name" label="名称" rules={[{ required: true }]}><Input placeholder="产品资料处理" /></Form.Item>
        <Form.Item name="description" label="说明"><Input.TextArea rows={3} placeholder="描述目标和主要交付物" /></Form.Item>
        <Space><Button onClick={() => navigate('/workflows')}>取消</Button><Button type="primary" htmlType="submit" loading={saving}>创建并设计</Button></Space>
      </Form>
    </Card>
  )
}
