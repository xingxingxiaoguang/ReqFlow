import { Alert, App, Button, Card, Collapse, Empty, Input, List, Select, Space, Spin, Tag, Typography } from 'antd'
import { ArrowLeftOutlined, CheckCircleOutlined, DeleteOutlined, EyeOutlined, RocketOutlined, SaveOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useMemo, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { workflowsApi, type Capability, type ValidationIssue } from '../../api/workflows'

export default function WorkflowDesigner() {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const { message } = App.useApp()
  const client = useQueryClient()
  const workflow = useQuery({ queryKey: ['workflow', id], queryFn: () => workflowsApi.get(id), enabled: Boolean(id) })
  const capabilities = useQuery({ queryKey: ['workflow-capabilities'], queryFn: workflowsApi.capabilities })
  const [preview, setPreview] = useState<{ id: string; draft_revision: number }>()
  const [caseName, setCaseName] = useState('代表性样本')
  const [caseInput, setCaseInput] = useState('{}')
  const [contract, setContractText] = useState('{}')
  const draft = workflow.data?.draft
  const catalog = capabilities.data?.capabilities ?? []
  const issues = workflow.data?.issues ?? []
  const errors = issues.filter((item) => item.severity === 'error')
  const command = useMutation({
    mutationFn: ({ type, payload }: { type: string; payload: unknown }) => workflowsApi.command(id, draft?.revision ?? 0, type, payload),
    onSuccess: () => { void client.invalidateQueries({ queryKey: ['workflow', id] }); message.success('草稿已更新') },
    onError: (error) => message.error((error as Error).message),
  })
  const addable = useMemo(() => {
    if (!draft) return []
    if (draft.nodes.length === 0) return catalog.filter((item) => item.inputs.some((port) => port.role === 'primary' && port.resource_type === draft.inputs[0]?.resource_type) && item.outputs.some((port) => port.role === 'primary' && port.resource_type === draft.outputs[0]?.resource_type))
    const tail = draft.nodes[draft.nodes.length - 1]
    const tailCapability = catalog.find((item) => item.ref.kind === tail.capability.kind && item.ref.version === tail.capability.version)
    const tailPort = tailCapability?.outputs.find((port) => port.role === 'primary')
    return catalog.filter((item) => item.inputs.some((port) => port.role === 'primary' && port.resource_type === tailPort?.resource_type))
  }, [catalog, draft])
  const addNode = (capability: Capability) => {
    const input = capability.inputs.find((port) => port.role === 'primary')
    const output = capability.outputs.find((port) => port.role === 'primary')
    if (!input || !output || !draft) return
    const node = { id: `${capability.ref.kind.replaceAll('.', '_')}_${draft.nodes.length + 1}`, name: capability.label, capability: capability.ref, config: capability.default_config ?? {} }
    if (draft.nodes.length === 0) {
      command.mutate({ type: 'create_from_blank', payload: { node, input_port: input.name, output_port: output.name } })
      return
    }
    command.mutate({ type: 'append_after', payload: { after_node_id: draft.nodes[draft.nodes.length - 1].id, node } })
  }
  const saveContract = () => {
    try {
      const value = JSON.parse(contract)
      command.mutate({ type: 'set_data_contract', payload: value })
    } catch { message.error('DataContract 必须是合法 JSON') }
  }
  const createPreview = async () => {
    if (!draft) return
    try {
      const value = JSON.parse(caseInput)
      const result = await workflowsApi.preview(id, draft.revision, value)
      setPreview({ id: result.id, draft_revision: result.draft_revision })
      message.success('临时预览已通过')
    } catch (error) { message.error((error as Error).message) }
  }
  const saveCase = () => {
    if (!draft) return
    try {
      command.mutate({ type: 'upsert_acceptance_case', payload: { id: 'sample_case', name: caseName, input: JSON.parse(caseInput), expectation: {} } })
    } catch { message.error('样本输入必须是合法 JSON') }
  }
  const runCase = async () => {
    if (!preview) return message.warning('先运行当前 revision 的预览')
    try { await workflowsApi.runAcceptance(id, 'sample_case', preview.id); await client.invalidateQueries({ queryKey: ['workflow', id] }); message.success('验收用例已通过') } catch (error) { message.error((error as Error).message) }
  }
  const publish = async () => {
    if (!draft) return
    try { await workflowsApi.publish(id, draft.revision); message.success('Revision 已发布'); void client.invalidateQueries({ queryKey: ['workflow', id] }) } catch (error) { message.error((error as Error).message) }
  }
  if (workflow.isLoading || capabilities.isLoading) return <Spin fullscreen tip="加载工作流…" />
  if (!draft) return <Card style={{ margin: 24 }}><Empty description="工作流不存在"><Button onClick={() => navigate('/workflows')}>返回目录</Button></Empty></Card>
  return (
    <div style={{ padding: 24, maxWidth: 1180, margin: '0 auto' }}>
      <Space style={{ marginBottom: 18 }}><Button type="text" icon={<ArrowLeftOutlined />} onClick={() => navigate('/workflows')} /><Typography.Title level={3} style={{ margin: 0 }}>{draft.name}</Typography.Title><Tag color="blue">Draft {draft.revision}</Tag></Space>
      <div style={{ display: 'grid', gridTemplateColumns: 'minmax(0, 1fr) 360px', gap: 18, alignItems: 'start' }}>
        <Card title="线性主链" extra={<Select placeholder="添加能力" style={{ width: 230 }} value={undefined} onChange={(kind) => { const capability = addable.find((item) => `${item.ref.kind}@${item.ref.version}` === kind); if (capability) addNode(capability) }} options={addable.map((item) => ({ value: `${item.ref.kind}@${item.ref.version}`, label: item.label }))} />}>
          {draft.nodes.length === 0 ? <Empty description="从兼容当前边界的 Capability 开始" /> : <List dataSource={draft.nodes} renderItem={(node, index) => {
            const capability = catalog.find((item) => item.ref.kind === node.capability.kind && item.ref.version === node.capability.version)
            return <List.Item actions={index > 0 && index < draft.nodes.length - 1 ? [<Button key="remove" danger type="text" icon={<DeleteOutlined />} onClick={() => command.mutate({ type: 'remove_and_bridge', payload: { node_id: node.id } })}>删除</Button>] : []}>
              <List.Item.Meta avatar={<Tag color={capability?.requires_llm ? 'purple' : 'blue'}>{index + 1}</Tag>} title={<Space>{node.name}{capability?.requires_llm && <Tag>模型/人工</Tag>}{capability?.has_side_effects && <Tag color="orange">副作用</Tag>}</Space>} description={capability?.description} />
            </List.Item>
          }} />}
        </Card>
        <Space direction="vertical" size={18} style={{ width: '100%' }}>
          <Card title="校验状态" extra={<Button icon={<SaveOutlined />} onClick={() => void workflowsApi.validate(id, 'draft')}>刷新</Button>}>
            {issues.length === 0 ? <Alert type="success" message="当前 Draft 结构有效" /> : <List size="small" dataSource={issues} renderItem={(issue: ValidationIssue) => <List.Item><Space><Tag color={issue.severity === 'error' ? 'red' : 'gold'}>{issue.severity}</Tag><Typography.Text>{issue.message}</Typography.Text></Space></List.Item>} />}
          </Card>
          <Collapse items={[{ key: 'contract', label: 'DataContract（内联）', children: <Space direction="vertical" style={{ width: '100%' }}><Typography.Text type="secondary">规则保存在 Workflow Draft 中，发布后随 Revision 固化。</Typography.Text><Input.TextArea rows={7} value={contract} onChange={(event) => setContractText(event.target.value)} placeholder='{"record_granularity":"一条记录","key_fields":["id"],"fields":[]}' /><Button onClick={saveContract}>保存合同</Button></Space> }]} />
          <Card title="样本验收" extra={<Tag color={draft.acceptance_cases?.some((item) => item.last_passed) ? 'green' : 'gold'}>{draft.acceptance_cases?.some((item) => item.last_passed) ? '已通过' : '待运行'}</Tag>}>
            <Space direction="vertical" style={{ width: '100%' }}><Input value={caseName} onChange={(event) => setCaseName(event.target.value)} placeholder="用例名称" /><Input.TextArea rows={3} value={caseInput} onChange={(event) => setCaseInput(event.target.value)} placeholder="样本 input JSON" /><Space wrap><Button onClick={saveCase}>保存用例</Button><Button icon={<EyeOutlined />} onClick={() => void createPreview()}>预览</Button><Button icon={<CheckCircleOutlined />} disabled={!preview} onClick={() => void runCase()}>通过验收</Button></Space></Space>
          </Card>
          <Button type="primary" size="large" block icon={<RocketOutlined />} disabled={errors.length > 0 || !draft.acceptance_cases?.some((item) => item.last_passed)} onClick={() => void publish()}>发布不可变 Revision</Button>
        </Space>
      </div>
    </div>
  )
}
