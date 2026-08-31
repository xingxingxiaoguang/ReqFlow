import { ArrowLeftOutlined, CopyOutlined, InboxOutlined, RocketOutlined } from '@ant-design/icons'
import { Alert, App, Button, Card, Descriptions, Empty, Popconfirm, Space, Tag, Typography } from 'antd'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from 'react-router-dom'
import { v2CatalogApi } from '../../api/v2/catalog'
import type { V2TaskDefinition } from '../../api/v2/types'
import { STEP_KIND_LABEL } from './status'
import { RESOURCE_TYPE_LABEL } from './workflowBlocks'

const { Paragraph, Text, Title } = Typography

export default function V2DefinitionDetail() {
  const { message } = App.useApp()
  const { id } = useParams()
  const navigate = useNavigate()
  const client = useQueryClient()
  const definitionQuery = useQuery({ queryKey: ['v2-definition', id], queryFn: () => v2CatalogApi.getDefinition(id!), enabled: Boolean(id) })
  const extraction = useQuery({ queryKey: ['v2-extraction-profiles'], queryFn: v2CatalogApi.listExtractionProfiles })
  const retrieval = useQuery({ queryKey: ['v2-retrieval-profiles'], queryFn: v2CatalogApi.listRetrievalProfiles })
  const analysis = useQuery({ queryKey: ['v2-analysis-profiles'], queryFn: v2CatalogApi.listAnalysisProfiles })
  const definition = definitionQuery.data?.definition
  const ruleNames = new Map([
    ...(extraction.data?.extraction_profiles ?? []).map((item) => [item.id, item.name] as const),
    ...(retrieval.data?.retrieval_profiles ?? []).map((item) => [item.id, item.name] as const),
    ...(analysis.data?.analysis_profiles ?? []).map((item) => [item.id, item.name] as const),
  ])

  const archive = async () => {
    if (!definition) return
    try {
      await v2CatalogApi.archiveDefinition(definition.id)
      message.success(`流程「${definition.name}」已归档`)
      await client.invalidateQueries({ queryKey: ['v2-definitions'] })
      navigate('/definitions')
    } catch (error) {
      message.error((error as Error).message)
    }
  }

  if (definitionQuery.isLoading) return <Card loading />
  if (!definition) return <Card><Empty description="流程不存在"><Button onClick={() => navigate('/definitions')}>返回流程管理</Button></Empty></Card>

  return <Space direction="vertical" size={16} style={{ width: '100%' }}>
    <Card>
      <Space style={{ width: '100%', justifyContent: 'space-between' }} align="start">
        <Space align="start">
          <Button type="text" icon={<ArrowLeftOutlined />} onClick={() => navigate('/definitions')} />
          <div>
            <Space><Title level={3} style={{ margin: 0 }}>{definition.name}</Title><Tag color="success">已发布</Tag></Space>
            <Paragraph type="secondary" style={{ marginBottom: 0 }}>{definition.description || '暂无业务说明'}</Paragraph>
          </div>
        </Space>
        <Space wrap>
          <Button size="large" icon={<CopyOutlined />} onClick={() => navigate(`/definitions/new?copy=${definition.id}`)}>复制并编辑</Button>
          <Button type="primary" size="large" icon={<RocketOutlined />} onClick={() => navigate(`/tasks/new?definition_id=${definition.id}`)}>创建任务</Button>
          <Popconfirm title="归档这个流程？" description="已创建任务不会受影响；归档后不能再创建新任务。" okText="归档" onConfirm={() => void archive()}>
            <Button size="large" icon={<InboxOutlined />}>归档</Button>
          </Popconfirm>
        </Space>
      </Space>
    </Card>

    <Descriptions bordered column={2} size="small">
      <Descriptions.Item label="流程编码"><Text code>{definition.key}</Text></Descriptions.Item>
      <Descriptions.Item label="版本"><Text code>{definition.definition_hash.slice(0, 12)}</Text></Descriptions.Item>
      <Descriptions.Item label="步骤数">{definition.steps.length}</Descriptions.Item>
      <Descriptions.Item label="发布时间">{new Date(definition.created_at).toLocaleString('zh-CN')}</Descriptions.Item>
    </Descriptions>

    <Card title="任务输入">
      {Object.entries(definition.input_ports).length ? <Space wrap>{Object.entries(definition.input_ports).map(([name, port]) => <Tag color="geekblue" key={name}>{port.description || name} · {RESOURCE_TYPE_LABEL[port.resource_type] ?? port.resource_type}</Tag>)}</Space> : <Text type="secondary">无需外部输入</Text>}
    </Card>

    <Card title="执行步骤">
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        {definition.steps.map((step, index) => <Card
          key={step.id}
          size="small"
          title={<Space><Tag color="blue">{index + 1}</Tag><Text strong>{step.name}</Text></Space>}
          extra={<Tag>{STEP_KIND_LABEL[step.kind] ?? step.kind}</Tag>}
        >
          <Space direction="vertical" size={5} style={{ width: '100%' }}>
            {Object.entries(step.inputs ?? {}).map(([name, reference]) => <Text key={name} type="secondary">{name} ← {friendlyReference(reference, definition)}</Text>)}
            {ruleForStep(step.config, ruleNames) && <Alert type="success" showIcon message={ruleForStep(step.config, ruleNames)} />}
            {!!step.depends_on?.length && <Text type="secondary">等待：{step.depends_on.map((stepID) => definition.steps.find((item) => item.id === stepID)?.name ?? stepID).join('、')}</Text>}
          </Space>
        </Card>)}
      </Space>
    </Card>

    <Card title="流程产出">
      <Space wrap>{Object.entries(definition.output_ports ?? {}).map(([name, port]) => <Tag color="green" key={name}>{port.description || name} · {RESOURCE_TYPE_LABEL[port.resource_type] ?? port.resource_type}</Tag>)}</Space>
    </Card>
  </Space>
}

function ruleForStep(config: Record<string, unknown> | undefined, names: Map<string, string>) {
  if (!config) return undefined
  const pairs: Array<[string, string]> = [
    ['extraction_profile_id', '抽取规则'], ['analysis_profile_id', '分析规则'], ['retrieval_profile_id', '索引规则'],
  ]
  for (const [key, label] of pairs) {
    const id = config[key]
    if (typeof id === 'string') return `${label}：${names.get(id) ?? id}`
  }
  return undefined
}

function friendlyReference(reference: string, definition: V2TaskDefinition) {
  if (reference.startsWith('$task.')) {
    const port = reference.slice('$task.'.length)
    return `任务输入「${definition.input_ports[port]?.description || port}」`
  }
  const match = /^\$step\.([^.]+)\.([^.]+)$/.exec(reference)
  if (!match) return reference
  const step = definition.steps.find((item) => item.id === match[1])
  return `步骤「${step?.name ?? match[1]}」的产出 ${match[2]}`
}
