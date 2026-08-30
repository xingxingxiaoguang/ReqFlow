import { useEffect, useMemo, useRef, useState } from 'react'
import {
  Alert, App, Button, Card, Col, Empty, Form, Input, Row, Select, Space, Steps, Tag, Typography,
} from 'antd'
import { ArrowLeftOutlined, BranchesOutlined, RocketOutlined, SaveOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { v2CatalogApi } from '../../api/v2/catalog'
import { v2TasksApi } from '../../api/v2/tasks'
import type { V2PortDefinition, V2TaskDefinition } from '../../api/v2/types'
import { STEP_KIND_LABEL } from './status'
import { RESOURCE_TYPE_LABEL } from './workflowBlocks'

const { Paragraph, Text, Title } = Typography

interface FormValues {
  definition_id: string
  title?: string
  bindings?: Record<string, string>
}

export default function NoCodeTaskNew() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const { message } = App.useApp()
  const [creating, setCreating] = useState<'create' | 'run'>()
  const selectedDefinitionRef = useRef<string>()
  const [form] = Form.useForm<FormValues>()
  const definitions = useQuery({ queryKey: ['v2-definitions', 'active'], queryFn: () => v2CatalogApi.listDefinitions({ status: 'active', limit: 200 }) })
  const datasets = useQuery({ queryKey: ['v2-datasets'], queryFn: () => v2CatalogApi.listDatasets({ status: 'active' }) })
  const assetSets = useQuery({ queryKey: ['v2-asset-sets'], queryFn: v2CatalogApi.listAssetSets })
  const retrievalSnapshots = useQuery({ queryKey: ['v2-retrieval-snapshots'], queryFn: v2CatalogApi.listRetrievalSnapshots })
  const artifacts = useQuery({ queryKey: ['v2-artifacts'], queryFn: v2CatalogApi.listArtifacts })
  const definitionID = Form.useWatch('definition_id', form)
  const selected = useMemo(() => (definitions.data?.task_definitions ?? []).find((item) => item.id === definitionID), [definitionID, definitions.data])

  useEffect(() => {
    const requested = searchParams.get('definition_id')
    if (requested && definitions.data?.task_definitions.some((item) => item.id === requested)) {
      form.setFieldValue('definition_id', requested)
    }
  }, [definitions.data, form, searchParams])

  useEffect(() => {
    if (selected && selectedDefinitionRef.current !== selected.id) {
      selectedDefinitionRef.current = selected.id
      form.setFieldsValue({ title: selected.name, bindings: {} })
    }
  }, [form, selected])

  const create = async (startNow: boolean) => {
    let values: FormValues
    try {
      values = await form.validateFields()
    } catch {
      return
    }
    if (!selected) return
    setCreating(startNow ? 'run' : 'create')
    try {
      const bindings = Object.entries(selected.input_ports)
        .map(([portName, port]) => ({
          port_name: portName,
          resource_type: port.resource_type,
          resource_id: values.bindings?.[portName]?.trim() ?? '',
        }))
        .filter((binding) => binding.resource_id)
      const created = await v2TasksApi.create({ definition_id: selected.id, title: values.title, bindings })
      if (startNow) {
        await v2TasksApi.start(created.task.id)
        message.success('任务已从流程派生并启动')
      } else {
        message.success('任务已从流程派生，当前为待启动状态')
      }
      navigate(`/tasks/${created.task.id}`)
    } catch (error) {
      message.error((error as Error).message)
    } finally {
      setCreating(undefined)
    }
  }

  const definitionOptions = (definitions.data?.task_definitions ?? []).map((item) => ({
    value: item.id,
    label: `${item.name} · ${item.steps.length} 步`,
  }))

  return (
    <Space direction="vertical" size={18} style={{ width: '100%' }}>
      <Card>
        <Space align="start">
          <Button type="text" icon={<ArrowLeftOutlined />} onClick={() => navigate('/tasks')} />
          <div>
            <Title level={3} style={{ margin: 0 }}>从流程创建任务</Title>
            <Paragraph type="secondary" style={{ marginBottom: 0 }}>流程定义“如何做”，任务只绑定“这次用什么数据”并负责一次执行。</Paragraph>
          </div>
        </Space>
      </Card>

      <Alert
        type="info"
        showIcon
        message="任务是流程定义的一次运行实例"
        description="创建任务不会生成或修改流程。系统会冻结所选流程版本与本次资源边界，因此同一流程可以安全地重复运行。"
      />

      <Form form={form} layout="vertical">
        <Card
          title="1. 选择已发布流程"
          extra={<Button icon={<BranchesOutlined />} onClick={() => navigate('/definitions/new')}>创建新流程</Button>}
        >
          {definitions.isLoading ? <Card loading /> : definitionOptions.length === 0 ? (
            <Empty description="还没有已发布流程">
              <Button type="primary" onClick={() => navigate('/definitions/new')}>先创建流程定义</Button>
            </Empty>
          ) : (
            <Form.Item name="definition_id" rules={[{ required: true, message: '请选择流程定义' }]} style={{ marginBottom: 0 }}>
              <Select showSearch optionFilterProp="label" options={definitionOptions} placeholder="选择本次任务采用的流程" size="large" />
            </Form.Item>
          )}
        </Card>

        {selected && <>
          <DefinitionPreview definition={selected} />
          <Card title="2. 填写本次任务信息与资源">
            <Form.Item name="title" label="任务名称" rules={[{ required: true, message: '请输入任务名称' }]}>
              <Input maxLength={200} placeholder={selected.name} />
            </Form.Item>
            <Row gutter={16}>
              {Object.entries(selected.input_ports).map(([portName, port]) => (
                <Col xs={24} lg={12} key={portName}>
                  <Form.Item
                    name={['bindings', portName]}
                    label={<Space>{port.description || portName}<Tag>{RESOURCE_TYPE_LABEL[port.resource_type] ?? port.resource_type}</Tag></Space>}
                    rules={port.required ? [{ required: true, message: `请选择${port.description || portName}` }] : undefined}
                    extra={port.required ? '流程要求本次任务必须提供' : '可选资源'}
                  >
                    <ResourceSelector
                      port={port}
                      datasets={datasets.data?.datasets ?? []}
                      assetSets={assetSets.data?.asset_sets ?? []}
                      snapshots={retrievalSnapshots.data?.retrieval_snapshots ?? []}
                      artifacts={artifacts.data?.artifacts ?? []}
                    />
                  </Form.Item>
                </Col>
              ))}
            </Row>
          </Card>
          <Card>
            <Space style={{ width: '100%', justifyContent: 'space-between' }}>
              <Text type="secondary">“仅创建”会保留为待启动；“创建并运行”会立即进入流程第一步。</Text>
              <Space>
                <Button size="large" icon={<SaveOutlined />} loading={creating === 'create'} onClick={() => void create(false)}>仅创建任务</Button>
                <Button type="primary" size="large" icon={<RocketOutlined />} loading={creating === 'run'} onClick={() => void create(true)}>创建并运行</Button>
              </Space>
            </Space>
          </Card>
        </>}
      </Form>
    </Space>
  )
}

function DefinitionPreview({ definition }: { definition: V2TaskDefinition }) {
  return (
    <Card title={<Space><span>流程预览</span><Tag color="success">已发布</Tag><Text code>{definition.definition_hash.slice(0, 12)}</Text></Space>}>
      <Paragraph type="secondary">{definition.description || '暂无业务说明'}</Paragraph>
      <Steps
        responsive
        current={-1}
        items={definition.steps.map((step) => ({
          title: step.name,
          description: STEP_KIND_LABEL[step.kind] ?? step.kind,
        }))}
      />
    </Card>
  )
}

function ResourceSelector({ port, datasets, assetSets, snapshots, artifacts, value, onChange }: {
  port: V2PortDefinition
  datasets: Array<{ id: string; name: string; purpose: string; item_count: number }>
  assetSets: Array<{ id: string; name: string }>
  snapshots: Array<{ id: string; dataset_id: string; source_seq: number }>
  artifacts: Array<{ id: string; name: string; kind: string }>
  value?: string
  onChange?: (value: string) => void
}) {
  if (port.resource_type === 'dataset' || port.resource_type === 'dataset_boundary') {
    return <Select value={value} onChange={onChange} showSearch optionFilterProp="label" placeholder="选择数据集" options={datasets.map((item) => ({ value: item.id, label: `${item.name} · ${item.purpose} · ${item.item_count} 条` }))} />
  }
  if (port.resource_type === 'asset_set') {
    return <Select value={value} onChange={onChange} showSearch optionFilterProp="label" placeholder="选择文件集" options={assetSets.map((item) => ({ value: item.id, label: item.name }))} />
  }
  if (port.resource_type === 'retrieval_snapshot') {
    return <Select value={value} onChange={onChange} showSearch optionFilterProp="label" placeholder="选择知识检索快照" options={snapshots.map((item) => ({ value: item.id, label: `${item.id.slice(0, 8)} · 数据集 ${item.dataset_id.slice(0, 8)} · seq ${item.source_seq}` }))} />
  }
  if (port.resource_type === 'artifact') {
    return <Select value={value} onChange={onChange} showSearch optionFilterProp="label" placeholder="选择业务制品" options={artifacts.map((item) => ({ value: item.id, label: `${item.name} · ${item.kind}` }))} />
  }
  return <Input value={value} onChange={(event) => onChange?.(event.target.value)} placeholder={`输入${RESOURCE_TYPE_LABEL[port.resource_type] ?? port.resource_type}资源 ID`} />
}
