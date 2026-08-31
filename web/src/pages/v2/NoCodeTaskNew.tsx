import { useEffect, useMemo, useRef, useState } from 'react'
import {
  Alert, App, Button, Card, Col, Empty, Form, Input, Modal, Radio, Row, Select, Space, Steps, Tag, Typography, Upload,
} from 'antd'
import { ArrowLeftOutlined, BranchesOutlined, InboxOutlined, PlusOutlined, RocketOutlined, SaveOutlined } from '@ant-design/icons'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import type { UploadFile } from 'antd/es/upload/interface'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { v2CatalogApi } from '../../api/v2/catalog'
import { v2TasksApi } from '../../api/v2/tasks'
import type {
  V2Dataset, V2ExtractionProfile, V2PortDefinition, V2Schema, V2StepDefinition, V2TaskDefinition,
} from '../../api/v2/types'
import { DATASET_PURPOSE_OPTIONS, datasetPurposeLabel } from './datasetPurpose'
import { schemaFieldOptions } from './SchemaFieldEditor'
import { STEP_KIND_LABEL } from './status'
import { RESOURCE_TYPE_LABEL } from './workflowBlocks'

const { Paragraph, Text, Title } = Typography

interface FormValues {
  definition_id: string
  title?: string
  bindings?: Record<string, string>
  file_strategy?: 'per_file' | 'whole_set'
  split_asset_port?: string
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
  const schemas = useQuery({ queryKey: ['v2-schemas'], queryFn: v2CatalogApi.listSchemas })
  const extractionProfiles = useQuery({ queryKey: ['v2-extraction-profiles'], queryFn: v2CatalogApi.listExtractionProfiles })
  const assetSets = useQuery({ queryKey: ['v2-asset-sets'], queryFn: v2CatalogApi.listAssetSets })
  const retrievalSnapshots = useQuery({ queryKey: ['v2-retrieval-snapshots'], queryFn: v2CatalogApi.listRetrievalSnapshots })
  const artifacts = useQuery({ queryKey: ['v2-artifacts'], queryFn: v2CatalogApi.listArtifacts })
  const definitionID = Form.useWatch('definition_id', form)
  const selected = useMemo(() => (definitions.data?.task_definitions ?? []).find((item) => item.id === definitionID), [definitionID, definitions.data])
  const assetSetPorts = useMemo(
    () => selected ? Object.entries(selected.input_ports).filter(([, port]) => port.resource_type === 'asset_set') : [],
    [selected],
  )
  const targetDatasetPorts = useMemo(
    () => new Set(selected ? Object.keys(selected.input_ports).filter((portName) => isTargetDatasetPort(selected, portName)) : []),
    [selected],
  )
  const targetSchemaByPort = useMemo(
    () => selected ? inferTargetDatasetSchemas(selected, extractionProfiles.data?.extraction_profiles ?? []) : {},
    [extractionProfiles.data, selected],
  )
  const fileStrategy = Form.useWatch('file_strategy', form)

  useEffect(() => {
    const requested = searchParams.get('definition_id')
    if (requested && definitions.data?.task_definitions.some((item) => item.id === requested)) {
      form.setFieldValue('definition_id', requested)
    }
  }, [definitions.data, form, searchParams])

  useEffect(() => {
    if (selected && selectedDefinitionRef.current !== selected.id) {
      selectedDefinitionRef.current = selected.id
      form.setFieldsValue({
        title: selected.name,
        bindings: {},
        file_strategy: assetSetPorts.length ? 'per_file' : undefined,
        split_asset_port: assetSetPorts[0]?.[0],
      })
    }
  }, [assetSetPorts, form, selected])

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
      for (const [portName, schemaId] of Object.entries(targetSchemaByPort)) {
        const datasetId = values.bindings?.[portName]
        const dataset = datasets.data?.datasets.find((item) => item.id === datasetId)
        if (dataset && dataset.schema_id !== schemaId) {
          throw new Error(`${selected.input_ports[portName]?.description || portName}的数据结构与流程提取规则不一致`)
        }
      }
      const bindings = Object.entries(selected.input_ports)
        .map(([portName, port]) => ({
          port_name: portName,
          resource_type: port.resource_type,
          resource_id: values.bindings?.[portName]?.trim() ?? '',
        }))
        .filter((binding) => binding.resource_id)
      const splitAssetPort = values.split_asset_port ?? assetSetPorts[0]?.[0]
      if (values.file_strategy === 'per_file' && splitAssetPort) {
        const created = await v2TasksApi.createBatch({
          definition_id: selected.id,
          title: values.title,
          bindings,
          split_port_name: splitAssetPort,
          start_now: startNow,
        })
        message.success(startNow
          ? `已按文件创建并启动 ${created.batch.size} 个独立任务`
          : `已按文件创建 ${created.batch.size} 个独立待启动任务`)
        navigate('/tasks')
        return
      }
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
            {assetSetPorts.length > 0 && <Card size="small" title="多个文件怎么运行" style={{ marginBottom: 18 }}>
              <Form.Item name="file_strategy" style={{ marginBottom: 10 }}>
                <Radio.Group optionType="button" buttonStyle="solid">
                  <Radio.Button value="per_file">每个文件一个独立任务（推荐）</Radio.Button>
                  <Radio.Button value="whole_set">全部文件放在一个任务中</Radio.Button>
                </Radio.Group>
              </Form.Item>
              {fileStrategy === 'per_file' && <Alert
                type="success"
                showIcon
                message="单个文件失败不会影响其他文件"
                description="系统会为文件集中的每个文件创建独立任务；它们共享目标数据集等其他配置，可分别查看、暂停和重试。"
              />}
              {fileStrategy === 'whole_set' && <Alert
                type="warning"
                showIcon
                message="任一文件抽取失败会使整个任务失败"
                description="仅在多个文件必须放在一起理解时使用这种方式。"
              />}
              {assetSetPorts.length > 1 && fileStrategy === 'per_file' && <Form.Item
                name="split_asset_port"
                label="按哪个文件集拆分"
                rules={[{ required: true, message: '请选择要拆分的文件集' }]}
                style={{ marginTop: 12, marginBottom: 0 }}
              >
                <Select options={assetSetPorts.map(([name, port]) => ({ value: name, label: port.description || name }))} />
              </Form.Item>}
            </Card>}
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
                      portName={portName}
                      port={port}
                      datasets={datasets.data?.datasets ?? []}
                      schemas={schemas.data?.schemas ?? []}
                      isTargetDataset={targetDatasetPorts.has(portName)}
                      targetSchemaPending={targetDatasetPorts.has(portName) && extractionProfiles.isLoading}
                      targetSchemaId={targetSchemaByPort[portName]}
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

function ResourceSelector({ portName, port, datasets, schemas, isTargetDataset, targetSchemaPending, targetSchemaId, assetSets, snapshots, artifacts, value, onChange }: {
  portName: string
  port: V2PortDefinition
  datasets: V2Dataset[]
  schemas: V2Schema[]
  isTargetDataset: boolean
  targetSchemaPending: boolean
  targetSchemaId?: string
  assetSets: Array<{ id: string; name: string }>
  snapshots: Array<{ id: string; dataset_id: string; source_seq: number }>
  artifacts: Array<{ id: string; name: string; kind: string }>
  value?: string
  onChange?: (value?: string) => void
}) {
  if (port.resource_type === 'dataset' || port.resource_type === 'dataset_boundary') {
    return <DatasetSelector
      portName={portName}
      datasets={datasets}
      schemas={schemas}
      isTargetDataset={isTargetDataset}
      targetSchemaPending={targetSchemaPending}
      targetSchemaId={targetSchemaId}
      value={value}
      onChange={onChange}
    />
  }
  if (port.resource_type === 'asset_set') {
    return <AssetSetSelector assetSets={assetSets} value={value} onChange={onChange} />
  }
  if (port.resource_type === 'retrieval_snapshot') {
    return <Select value={value} onChange={onChange} showSearch optionFilterProp="label" placeholder="选择知识检索快照" options={snapshots.map((item) => ({ value: item.id, label: `${item.id.slice(0, 8)} · 数据集 ${item.dataset_id.slice(0, 8)} · seq ${item.source_seq}` }))} />
  }
  if (port.resource_type === 'artifact') {
    return <Select value={value} onChange={onChange} showSearch optionFilterProp="label" placeholder="选择业务制品" options={artifacts.map((item) => ({ value: item.id, label: `${item.name} · ${item.kind}` }))} />
  }
  return <Input value={value} onChange={(event) => onChange?.(event.target.value)} placeholder={`输入${RESOURCE_TYPE_LABEL[port.resource_type] ?? port.resource_type}资源 ID`} />
}

function DatasetSelector({ portName, datasets, schemas, isTargetDataset, targetSchemaPending, targetSchemaId, value, onChange }: {
  portName: string
  datasets: V2Dataset[]
  schemas: V2Schema[]
  isTargetDataset: boolean
  targetSchemaPending: boolean
  targetSchemaId?: string
  value?: string
  onChange?: (value?: string) => void
}) {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [saving, setSaving] = useState(false)
  const [datasetForm] = Form.useForm<{ name: string; description?: string; purpose: string; key_fields: string[] }>()
  const targetSchema = schemas.find((schema) => schema.id === targetSchemaId)
  const compatibleDatasets = isTargetDataset
    ? targetSchemaId ? datasets.filter((dataset) => dataset.schema_id === targetSchemaId) : []
    : datasets
  const excludedCount = datasets.length - compatibleDatasets.length
  const canCreate = Boolean(targetSchemaId && targetSchema)

  useEffect(() => {
    if (value && isTargetDataset && targetSchemaId && !compatibleDatasets.some((dataset) => dataset.id === value)) {
      onChange?.(undefined)
    }
  }, [compatibleDatasets, isTargetDataset, onChange, targetSchemaId, value])

  const showCreate = () => {
    datasetForm.setFieldsValue({ purpose: 'base', key_fields: [] })
    setOpen(true)
  }

  const close = () => {
    if (saving) return
    setOpen(false)
    datasetForm.resetFields()
  }

  const save = async (values: { name: string; description?: string; purpose: string; key_fields: string[] }) => {
    if (!targetSchema) return
    setSaving(true)
    try {
      const created = await v2CatalogApi.createDataset({
        name: values.name.trim(),
        description: values.description?.trim(),
        purpose: values.purpose,
        schema_id: targetSchema.id,
        key_fields: values.key_fields,
      })
      await queryClient.invalidateQueries({ queryKey: ['v2-datasets'] })
      onChange?.(created.dataset.id)
      message.success(`数据集“${created.dataset.name}”已创建并选中`)
      setOpen(false)
      datasetForm.resetFields()
    } catch (error) {
      message.error((error as Error).message)
    } finally {
      setSaving(false)
    }
  }

  return <>
    <Space.Compact block>
      <Select
        style={{ flex: 1 }}
        value={value}
        onChange={onChange}
        showSearch
        optionFilterProp="label"
        disabled={isTargetDataset && (!targetSchemaId || !targetSchema)}
        loading={targetSchemaPending}
        placeholder={targetSchemaPending ? '正在解析流程目标结构…' : targetSchema ? `选择 ${targetSchema.name} 数据集` : '选择数据集'}
        options={compatibleDatasets.map((item) => ({ value: item.id, label: `${item.name} · ${datasetPurposeLabel(item.purpose)} · ${item.item_count} 条` }))}
        notFoundContent={targetSchema ? `还没有采用“${targetSchema.name}”的数据集` : '还没有可用数据集'}
      />
      {isTargetDataset && <Button icon={<PlusOutlined />} disabled={!canCreate} onClick={showCreate}>就地新建</Button>}
    </Space.Compact>
    {targetSchema && <Text type="secondary">
      仅显示数据结构为“{targetSchema.name}”的数据集{excludedCount > 0 ? `，已排除 ${excludedCount} 个不兼容数据集` : ''}。
    </Text>}
    {isTargetDataset && !targetSchemaPending && !targetSchema && <Text type="danger">
      无法从流程提取规则确定目标数据结构，请先修正流程配置。
    </Text>}
    <Modal
      title="新建并使用目标数据集"
      open={open}
      onCancel={close}
      onOk={() => datasetForm.submit()}
      okText="创建并选中"
      confirmLoading={saving}
      destroyOnHidden
    >
      <Alert
        type="info"
        showIcon
        message={`数据结构已由流程固定为“${targetSchema?.name ?? targetSchemaId}”`}
        description={`新数据集创建后会立即绑定到任务输入端口 ${portName}，无需离开当前页面。`}
        style={{ marginBottom: 16 }}
      />
      <Form form={datasetForm} layout="vertical" onFinish={(values) => void save(values)} requiredMark="optional">
        <Form.Item name="name" label="数据集名称" rules={[{ required: true, whitespace: true, message: '请输入数据集名称' }]}>
          <Input maxLength={200} placeholder="例如：本次产品资料库" autoFocus />
        </Form.Item>
        <Form.Item name="description" label="用途说明">
          <Input.TextArea rows={2} placeholder="说明这批数据服务的业务场景" />
        </Form.Item>
        <Form.Item
          name="purpose"
          label="这批数据用于什么"
          extra="请选择最贴近实际业务场景的一项，系统会据此判断它能在哪些流程步骤中使用。"
          rules={[{ required: true, message: '请选择这批数据的业务用途' }]}
        >
          <Select options={DATASET_PURPOSE_OPTIONS} />
        </Form.Item>
        <Form.Item name="key_fields" label="业务唯一键" rules={[{ required: true, message: '至少选择一个唯一键字段' }]}>
          <Select mode="multiple" options={schemaFieldOptions(targetSchema?.json_schema)} placeholder="用于识别同一条业务数据" />
        </Form.Item>
      </Form>
    </Modal>
  </>
}

function inferTargetDatasetSchemas(
  definition: V2TaskDefinition,
  profiles: V2ExtractionProfile[],
): Record<string, string> {
  const stepById = new Map(definition.steps.map((step) => [step.id, step]))
  const profileById = new Map(profiles.map((profile) => [profile.id, profile]))
  const result: Record<string, string> = {}

  for (const [portName, port] of Object.entries(definition.input_ports)) {
    if (port.resource_type !== 'dataset') continue
    const validators = definition.steps.filter((step) =>
      step.kind === 'data.validate' && step.inputs?.dataset === `$task.${portName}`,
    )
    const schemaIds = new Set<string>()
    for (const validator of validators) {
      for (const ancestor of upstreamSteps(validator, stepById)) {
        if (ancestor.kind !== 'llm.extract') continue
        const profileId = ancestor.config?.extraction_profile_id
        if (typeof profileId !== 'string') continue
        const schemaId = profileById.get(profileId)?.target_schema_id
        if (schemaId) schemaIds.add(schemaId)
      }
    }
    if (schemaIds.size === 1) result[portName] = [...schemaIds][0]
  }
  return result
}

function isTargetDatasetPort(definition: V2TaskDefinition, portName: string): boolean {
  return definition.steps.some((step) =>
    step.kind === 'data.validate' && step.inputs?.dataset === `$task.${portName}`,
  )
}

function upstreamSteps(step: V2StepDefinition, stepById: Map<string, V2StepDefinition>): V2StepDefinition[] {
  const visited = new Set<string>()
  const queue = [...(step.depends_on ?? []), ...Object.values(step.inputs ?? {}).flatMap(stepReferenceId)]
  const ancestors: V2StepDefinition[] = []
  while (queue.length) {
    const stepId = queue.shift()!
    if (visited.has(stepId)) continue
    visited.add(stepId)
    const ancestor = stepById.get(stepId)
    if (!ancestor) continue
    ancestors.push(ancestor)
    queue.push(...(ancestor.depends_on ?? []), ...Object.values(ancestor.inputs ?? {}).flatMap(stepReferenceId))
  }
  return ancestors
}

function stepReferenceId(reference: string): string[] {
  const match = /^\$step\.([^.]+)\./.exec(reference)
  return match ? [match[1]] : []
}

function AssetSetSelector({ assetSets, value, onChange }: {
  assetSets: Array<{ id: string; name: string }>
  value?: string
  onChange?: (value?: string) => void
}) {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const [open, setOpen] = useState(false)
  const [saving, setSaving] = useState(false)
  const [assetForm] = Form.useForm<{ name: string; files: UploadFile[] }>()

  const close = () => {
    if (saving) return
    setOpen(false)
    assetForm.resetFields()
  }

  const save = async ({ name, files }: { name: string; files: UploadFile[] }) => {
    const selectedFiles = files.flatMap((item) => item.originFileObj ? [item.originFileObj] : [])
    if (!selectedFiles.length) return
    setSaving(true)
    try {
      const uploaded = await Promise.all(selectedFiles.map((file) => v2CatalogApi.uploadAsset(file)))
      const created = await v2CatalogApi.createAssetSet({
        name: name.trim(),
        asset_ids: uploaded.map((item) => item.asset.id),
      })
      onChange?.(created.asset_set.id)
      await queryClient.invalidateQueries({ queryKey: ['v2-asset-sets'] })
      message.success(`文件集“${created.asset_set.name}”已创建并选中`)
      setOpen(false)
      assetForm.resetFields()
    } catch (error) {
      message.error((error as Error).message)
    } finally {
      setSaving(false)
    }
  }

  return <>
    <Space.Compact block>
      <Select
        style={{ flex: 1 }}
        value={value}
        onChange={onChange}
        showSearch
        optionFilterProp="label"
        placeholder="选择已有文件集"
        options={assetSets.map((item) => ({ value: item.id, label: item.name }))}
      />
      <Button icon={<PlusOutlined />} onClick={() => setOpen(true)}>就地新建</Button>
    </Space.Compact>
    <Modal
      title="新建并使用文件集"
      open={open}
      onCancel={close}
      onOk={() => assetForm.submit()}
      okText="创建并选中"
      confirmLoading={saving}
      destroyOnHidden
    >
      <Alert
        type="info"
        showIcon
        message="文件上传后会组成一个固定文件集，并立即绑定到当前任务。"
        style={{ marginBottom: 16 }}
      />
      <Form form={assetForm} layout="vertical" onFinish={(values) => void save(values)} requiredMark="optional">
        <Form.Item name="name" label="文件集名称" rules={[{ required: true, whitespace: true, message: '请输入文件集名称' }]}>
          <Input maxLength={200} placeholder="例如：本次需求评审资料" autoFocus />
        </Form.Item>
        <Form.Item
          name="files"
          label="上传文件"
          valuePropName="fileList"
          getValueFromEvent={(event) => Array.isArray(event) ? event : event?.fileList}
          rules={[{ required: true, message: '请至少选择一个文件' }]}
        >
          <Upload.Dragger multiple beforeUpload={() => false}>
            <p><InboxOutlined style={{ fontSize: 28 }} /></p>
            <p>拖入文件或点击选择</p>
            <p className="ant-upload-hint">可一次选择多个文件，创建任务前不会离开当前页面。</p>
          </Upload.Dragger>
        </Form.Item>
      </Form>
    </Modal>
  </>
}
