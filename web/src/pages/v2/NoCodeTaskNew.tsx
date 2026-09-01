import { useEffect, useMemo, useState } from 'react'
import {
  Alert, App, Button, Card, Col, Empty, Form, Input, Modal, Radio, Row, Select, Space, Steps, Tag, Typography, Upload,
} from 'antd'
import { ArrowLeftOutlined, InboxOutlined, PlusOutlined, RocketOutlined, SaveOutlined, ThunderboltOutlined } from '@ant-design/icons'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import type { UploadFile } from 'antd/es/upload/interface'
import { useNavigate } from 'react-router-dom'
import { v2CatalogApi } from '../../api/v2/catalog'
import { v2TasksApi } from '../../api/v2/tasks'
import type { V2Dataset, V2TaskDefinition } from '../../api/v2/types'
import { DATASET_PURPOSE_OPTIONS, datasetPurposeLabel } from './datasetPurpose'
import { schemaFieldOptions } from './SchemaFieldEditor'
import { STEP_KIND_LABEL } from './status'
import { RESOURCE_TYPE_LABEL } from './workflowBlocks'
import EmbeddedResourceCreate, { type EmbeddedResource } from './EmbeddedResourceCreate'

const { Paragraph, Text, Title } = Typography

type FlowKey = 'cleaning'

// v1：索引建立收敛为数据集上的隐式任务（数据管理 → 索引），任务发起页只保留数据清洗。

const FIXED_FLOWS: Array<{ key: FlowKey; definitionKey: string; name: string; description: string }> = [
  {
    key: 'cleaning', definitionKey: 'data_clean_import', name: '数据清洗入库',
    description: '解析文件、按字段结构抽取、清洗校验、人工审核后原子发布到数据集。',
  },
]

interface FormValues {
  title?: string
  extraction_profile_id?: string
  bindings?: Record<string, string>
  file_strategy?: 'per_file' | 'whole_set'
  split_asset_port?: string
}

/** v1：流程管理已收敛为两个固定流程；抽取/检索规则在创建任务时按数据集字段选择。 */
export default function NoCodeTaskNew() {
  const navigate = useNavigate()
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const [creating, setCreating] = useState<'create' | 'run'>()
  const [flow, setFlow] = useState<FlowKey>()
  const [profileCreator, setProfileCreator] = useState<'extraction'>()
  const [form] = Form.useForm<FormValues>()

  const definitions = useQuery({
    queryKey: ['v2-definitions', 'active'],
    queryFn: () => v2CatalogApi.listDefinitions({ status: 'active', limit: 200 }),
  })
  const datasets = useQuery({ queryKey: ['v2-datasets'], queryFn: () => v2CatalogApi.listDatasets({ status: 'active' }) })
  const schemas = useQuery({ queryKey: ['v2-schemas'], queryFn: v2CatalogApi.listSchemas })
  const extractionProfiles = useQuery({ queryKey: ['v2-extraction-profiles'], queryFn: () => v2CatalogApi.listExtractionProfiles() })
  const assetSets = useQuery({ queryKey: ['v2-asset-sets'], queryFn: v2CatalogApi.listAssetSets })

  const definitionByFlow = useMemo(() => {
    const map: Partial<Record<FlowKey, V2TaskDefinition>> = {}
    for (const item of FIXED_FLOWS) {
      const found = (definitions.data?.task_definitions ?? []).find((definition) => definition.key === item.definitionKey)
      if (found) map[item.key] = found
    }
    return map
  }, [definitions.data])
  const selected = flow ? definitionByFlow[flow] : undefined

  const extractionProfileID = Form.useWatch('extraction_profile_id', form)
  const selectedExtractionProfile = useMemo(
    () => (extractionProfiles.data?.extraction_profiles ?? []).find((profile) => profile.id === extractionProfileID),
    [extractionProfiles.data, extractionProfileID],
  )
  const targetSchemaId = selectedExtractionProfile?.target_schema_id
  const targetSchema = schemas.data?.schemas.find((schema) => schema.id === targetSchemaId)
  const fileStrategy = Form.useWatch('file_strategy', form)

  useEffect(() => {
    if (!flow && definitionByFlow.cleaning) {
      setFlow('cleaning')
      form.setFieldsValue({ title: definitionByFlow.cleaning.name })
    }
  }, [definitionByFlow.cleaning, flow, form])

  const chooseFlow = (key: FlowKey) => {
    setFlow(key)
    form.resetFields()
    const definition = definitionByFlow[key]
    if (definition) form.setFieldsValue({ title: definition.name })
  }

  const create = async (startNow: boolean) => {
    let values: FormValues
    try {
      values = await form.validateFields()
    } catch {
      return
    }
    if (!flow || !selected) return
    const bindings = Object.entries(selected.input_ports)
      .map(([portName, port]) => ({
        port_name: portName,
        resource_type: port.resource_type,
        resource_id: values.bindings?.[portName]?.trim() ?? '',
      }))
      .filter((binding) => binding.resource_id)
    const stepConfigs: Record<string, Record<string, unknown>> = {}
    if (flow === 'cleaning') {
      const dataset = datasets.data?.datasets.find((item) => item.id === values.bindings?.target)
      const profile = extractionProfiles.data?.extraction_profiles.find((item) => item.id === values.extraction_profile_id)
      if (dataset && profile && dataset.schema_id !== profile.target_schema_id) {
        message.error('抽取规则与目标数据集的字段结构不一致，请重新选择')
        return
      }
      const step = selected.steps.find((item) => item.kind === 'document.extract')
      if (!step || !values.extraction_profile_id) {
        message.error('请选择抽取规则')
        return
      }
      stepConfigs[step.id] = { extraction_profile_id: values.extraction_profile_id }
    }
    setCreating(startNow ? 'run' : 'create')
    try {
      const splitAssetPort = values.split_asset_port ?? Object.keys(selected.input_ports).find((portName) => selected.input_ports[portName].resource_type === 'asset_set')
      if (flow === 'cleaning' && values.file_strategy === 'per_file' && splitAssetPort) {
        const created = await v2TasksApi.createBatch({
          definition_id: selected.id,
          title: values.title,
          bindings,
          split_port_name: splitAssetPort,
          start_now: startNow,
          step_configs: stepConfigs,
        })
        message.success(startNow
          ? `已按文件创建并启动 ${created.batch.size} 个独立任务`
          : `已按文件创建 ${created.batch.size} 个独立待启动任务`)
        navigate('/tasks')
        return
      }
      const created = await v2TasksApi.create({ definition_id: selected.id, title: values.title, bindings, step_configs: stepConfigs })
      if (startNow) {
        await v2TasksApi.start(created.task.id)
        message.success('任务已创建并启动')
      } else {
        message.success('任务已创建，当前为待启动状态')
      }
      navigate(`/tasks/${created.task.id}`)
    } catch (error) {
      message.error((error as Error).message)
    } finally {
      setCreating(undefined)
    }
  }

  return (
    <Space direction="vertical" size={18} style={{ width: '100%' }}>
      <Card>
        <Space align="start">
          <Button type="text" icon={<ArrowLeftOutlined />} onClick={() => navigate('/tasks')} />
          <div>
            <Title level={3} style={{ margin: 0 }}>发起数据清洗任务</Title>
            <Paragraph type="secondary" style={{ marginBottom: 0 }}>
              流程已固定；抽取规则在创建任务时按目标数据集的字段结构选择。建立索引请到数据管理页的数据集上直接发起。
            </Paragraph>
          </div>
        </Space>
      </Card>

      <Card title="1. 选择业务流程">
        {definitions.isLoading ? <Card loading /> : (
          <Row gutter={[16, 16]}>
            {FIXED_FLOWS.map((item) => {
              const definition = definitionByFlow[item.key]
              const active = flow === item.key
              return <Col xs={24} md={12} key={item.key}>
                <Card
                  hoverable
                  onClick={() => chooseFlow(item.key)}
                  style={{ height: '100%', borderColor: active ? '#1677ff' : undefined }}
                  title={<Space><ThunderboltOutlined style={{ color: active ? '#1677ff' : undefined }} /><Text strong>{item.name}</Text>{active && <Tag color="blue">已选择</Tag>}</Space>}
                >
                  <Paragraph type="secondary" style={{ minHeight: 44 }}>{item.description}</Paragraph>
                  {definition ? <Steps
                    responsive
                    size="small"
                    current={-1}
                    items={definition.steps.map((step) => ({ title: step.name, description: STEP_KIND_LABEL[step.kind] ?? step.kind }))}
                  /> : <Alert type="warning" showIcon message="该固定流程尚未就绪" description="系统启动时会自动准备固定流程，请稍后刷新重试。" />}
                </Card>
              </Col>
            })}
          </Row>
        )}
      </Card>

      {flow && selected && <>
        <Card title="2. 填写本次任务信息">
          <Form form={form} layout="vertical">
            <Form.Item name="title" label="任务名称" rules={[{ required: true, message: '请输入任务名称' }]}>
              <Input maxLength={200} placeholder={selected.name} />
            </Form.Item>

            {flow === 'cleaning' && <>
              <Card size="small" title={<Space><InboxOutlined /><span>文件与目标</span></Space>} style={{ marginBottom: 18 }}>
                <Row gutter={16}>
                  <Col xs={24} lg={12}>
                    <Form.Item
                      name="extraction_profile_id"
                      label={<Space>抽取规则<Tag>按字段服务</Tag></Space>}
                      rules={[{ required: true, message: '请选择抽取规则' }]}
                      extra="抽取规则决定按哪套字段结构理解文件；选定后目标数据集会自动对齐。"
                    >
                      <Select
                        showSearch
                        optionFilterProp="label"
                        placeholder="选择抽取规则"
                        options={(extractionProfiles.data?.extraction_profiles ?? []).map((profile) => {
                          const schema = schemas.data?.schemas.find((item) => item.id === profile.target_schema_id)
                          return { value: profile.id, label: schema ? `${profile.name} · ${schema.name}` : profile.name }
                        })}
                        notFoundContent={<Empty description="还没有抽取规则" />}
                      />
                    </Form.Item>
                    <Button size="small" icon={<PlusOutlined />} onClick={() => setProfileCreator('extraction')}>就地创建抽取规则</Button>
                  </Col>
                  <Col xs={24} lg={12}>
                    <Form.Item
                      name={['bindings', 'target']}
                      label={<Space>目标数据集<Tag>{RESOURCE_TYPE_LABEL.dataset}</Tag></Space>}
                      rules={[{ required: true, message: '请选择目标数据集' }]}
                      extra={targetSchema ? undefined : '先选择抽取规则，数据集会按字段结构自动过滤。'}
                    >
                      <DatasetSelector
                        datasets={datasets.data?.datasets ?? []}
                        targetSchemaId={targetSchemaId}
                        targetSchemaName={targetSchema?.name}
                        keyFieldOptions={schemaFieldOptions(targetSchema?.json_schema)}
                        disabled={!targetSchemaId}
                      />
                    </Form.Item>
                  </Col>
                </Row>
                <Row gutter={16}>
                  <Col xs={24} lg={12}>
                    <Form.Item
                      name={['bindings', 'assets']}
                      label={<Space>待解析文件集<Tag>{RESOURCE_TYPE_LABEL.asset_set}</Tag></Space>}
                      rules={[{ required: true, message: '请选择文件集' }]}
                    >
                      <AssetSetSelector assetSets={assetSets.data?.asset_sets ?? []} />
                    </Form.Item>
                  </Col>
                </Row>
              </Card>
              <Card size="small" title="多个文件怎么运行" style={{ marginBottom: 18 }}>
                <Form.Item name="file_strategy" style={{ marginBottom: 10 }} initialValue="per_file">
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
              </Card>
            </>}


            <Alert
              type="info"
              showIcon
              message="“仅创建”会保留为待启动；“创建并运行”会立即进入流程第一步。"
              description="所选规则只会注入本次任务的执行快照，不影响固定流程本身。"
            />
          </Form>
        </Card>
        <Card>
          <Space style={{ width: '100%', justifyContent: 'flex-end' }}>
            <Button size="large" icon={<SaveOutlined />} loading={creating === 'create'} onClick={() => void create(false)}>仅创建任务</Button>
            <Button type="primary" size="large" icon={<RocketOutlined />} loading={creating === 'run'} onClick={() => void create(true)}>创建并运行</Button>
          </Space>
        </Card>
      </>}

      <EmbeddedResourceCreate
        kind={profileCreator}
        fixedSchemaId={targetSchemaId}
        onCancel={() => setProfileCreator(undefined)}
        onCreated={(resource: EmbeddedResource) => {
          form.setFieldValue('extraction_profile_id', resource.id)
          void queryClient.invalidateQueries({ queryKey: ['v2-extraction-profiles'] })
          void queryClient.invalidateQueries({ queryKey: ['v2-schemas'] })
          setProfileCreator(undefined)
        }}
      />
    </Space>
  )
}

function DatasetSelector({ datasets, targetSchemaId, targetSchemaName, keyFieldOptions, disabled, value, onChange }: {
  datasets: V2Dataset[]
  targetSchemaId?: string
  targetSchemaName?: string
  keyFieldOptions: Array<{ value: string; label: string }>
  disabled?: boolean
  value?: string
  onChange?: (value?: string) => void
}) {
  const queryClient = useQueryClient()
  const { message } = App.useApp()
  const [open, setOpen] = useState(false)
  const [saving, setSaving] = useState(false)
  const [datasetForm] = Form.useForm<{ name: string; description?: string; purpose: string; key_fields: string[] }>()

  useEffect(() => {
    if (value && targetSchemaId && !datasets.some((dataset) => dataset.id === value && dataset.schema_id === targetSchemaId)) {
      onChange?.(undefined)
    }
  }, [datasets, onChange, targetSchemaId, value])

  const compatibleDatasets = targetSchemaId
    ? datasets.filter((dataset) => dataset.schema_id === targetSchemaId)
    : datasets
  const excludedCount = datasets.length - compatibleDatasets.length

  const close = () => {
    if (saving) return
    setOpen(false)
    datasetForm.resetFields()
  }

  const save = async (values: { name: string; description?: string; purpose: string; key_fields: string[] }) => {
    if (!targetSchemaId) return
    setSaving(true)
    try {
      const created = await v2CatalogApi.createDataset({
        name: values.name.trim(),
        description: values.description?.trim(),
        purpose: values.purpose,
        schema_id: targetSchemaId,
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
        disabled={disabled}
        placeholder={targetSchemaId ? `选择 ${targetSchemaName ?? ''} 数据集` : '先选择抽取规则'}
        options={compatibleDatasets.map((item) => ({ value: item.id, label: `${item.name} · ${datasetPurposeLabel(item.purpose)} · ${item.item_count} 条` }))}
        notFoundContent={targetSchemaId ? `还没有采用“${targetSchemaName ?? ''}”的数据集` : '先选择抽取规则'}
      />
      <Button icon={<PlusOutlined />} disabled={!targetSchemaId} onClick={() => {
        datasetForm.setFieldsValue({ purpose: 'base', key_fields: [] })
        setOpen(true)
      }}>就地新建</Button>
    </Space.Compact>
    {targetSchemaId && <Text type="secondary">
      仅显示字段结构为“{targetSchemaName}”的数据集{excludedCount > 0 ? `，已排除 ${excludedCount} 个不兼容数据集` : ''}。
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
        message={`字段结构已由抽取规则固定为“${targetSchemaName ?? ''}”`}
        description="新数据集创建后会立即绑定到任务，无需离开当前页面。"
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
          <Select mode="multiple" options={keyFieldOptions} placeholder="用于识别同一条业务数据" />
        </Form.Item>
      </Form>
    </Modal>
  </>
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
