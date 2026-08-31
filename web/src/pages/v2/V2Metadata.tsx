import { useState } from 'react'
import {
  Alert,
  App,
  Button,
  Card,
  Col,
  Collapse,
  Form,
  Input,
  InputNumber,
  Modal,
  Row,
  Select,
  Space,
  Table,
  Tabs,
  Tag,
  Typography,
  Upload,
} from 'antd'
import { InboxOutlined, PlusOutlined } from '@ant-design/icons'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { v2CatalogApi } from '../../api/v2/catalog'
import type { JSONSchemaProperty, V2JSONSchema, V2Schema } from '../../api/v2/types'
import {
  buildObjectSchema,
  createSchemaField,
  SchemaFieldEditor,
  SchemaFieldSummary,
  schemaFieldOptions,
  schemaSummary,
  validateSchemaFields,
  type SchemaFieldDraft,
} from './SchemaFieldEditor'

const { Paragraph, Text, Title } = Typography
type CreateKind = 'schema' | 'analysis' | 'extraction' | 'retrieval' | 'assets'

const sourceReferenceSchema: JSONSchemaProperty = {
  type: 'object',
  additionalProperties: false,
  required: ['dataset_item_id'],
  properties: {
    dataset_item_id: { type: 'string', title: '来源数据 ID' },
    quote: { type: 'string', title: '引用原文' },
  },
}

const provenanceSchema: JSONSchemaProperty = {
  type: 'object',
  title: '来源依据',
  additionalProperties: false,
  required: ['source_refs'],
  properties: {
    source_refs: { type: 'array', minItems: 1, items: sourceReferenceSchema },
  },
}

function recordArraySchema(
  fields: Record<string, JSONSchemaProperty>,
  required: string[],
): JSONSchemaProperty {
  return {
    type: 'array',
    items: {
      type: 'object',
      additionalProperties: false,
      required: ['fields', 'provenance'],
      properties: {
        fields: { type: 'object', additionalProperties: false, required, properties: fields },
        provenance: provenanceSchema,
      },
    },
  }
}

interface AnalysisPreset {
  label: string
  description: string
  outputs: string[]
  instruction: string
  schema: V2JSONSchema
}

const analysisPresets: Record<string, AnalysisPreset> = {
  bug: {
    label: 'Bug 分析',
    description: '产出结构化问题记录和一份可下载的 Markdown 分析报告。',
    outputs: ['问题记录', '根因', '严重程度', '修复建议', '来源引用', 'Markdown 报告'],
    instruction: '分析问题现象与产品知识，输出可验证的根因、影响、修复建议。records 中每条记录必须包含来源引用，report 输出完整 Markdown 报告。',
    schema: {
      type: 'object',
      additionalProperties: false,
      required: ['records', 'report'],
      properties: {
        records: recordArraySchema({
          id: { type: 'string' },
          title: { type: 'string' },
          root_cause: { type: 'string' },
          severity: { type: 'string' },
          fix_suggestion: { type: 'string' },
        }, ['id', 'title', 'root_cause', 'severity', 'fix_suggestion']),
        report: { type: 'string', title: 'Markdown 报告' },
      },
    },
  },
  spec: {
    label: '产品方案',
    description: '基于检索到的业务知识生成一份完整、可交付的产品方案。',
    outputs: ['目标', '范围', '用户流程', '验收标准', '风险', 'Markdown 报告'],
    instruction: '基于产品与需求知识生成可执行产品方案，覆盖目标、范围、用户流程、验收标准、风险与待决策项，report 输出 Markdown。',
    schema: {
      type: 'object',
      additionalProperties: false,
      required: ['report'],
      properties: { report: { type: 'string', title: 'Markdown 产品方案' } },
    },
  },
  graph: {
    label: '知识图谱',
    description: '提取实体节点和有方向关系，并保留每条结果的来源。',
    outputs: ['实体节点', '实体关系', '节点类型', '关系类型', '来源引用'],
    instruction: '从知识中抽取去重后的实体节点和有方向关系。nodes 与 edges 必须保留来源引用并分别可直接写入目标数据集。',
    schema: {
      type: 'object',
      additionalProperties: false,
      required: ['nodes', 'edges'],
      properties: {
        nodes: recordArraySchema({
          id: { type: 'string' },
          name: { type: 'string' },
          type: { type: 'string' },
        }, ['id', 'name', 'type']),
        edges: recordArraySchema({
          id: { type: 'string' },
          source_id: { type: 'string' },
          target_id: { type: 'string' },
          type: { type: 'string' },
        }, ['id', 'source_id', 'target_id', 'type']),
      },
    },
  },
  custom: {
    label: '自定义分析',
    description: '用可视化字段编辑器定义自己的结构化输出。',
    outputs: ['由你定义'],
    instruction: '',
    schema: { type: 'object', additionalProperties: false, properties: {} },
  },
}

function defaultDatasetFields(): SchemaFieldDraft[] {
  return [createSchemaField({
    name: 'id',
    title: '唯一标识',
    description: '每条数据的稳定唯一编号',
    kind: 'string',
    required: true,
  })]
}

function defaultAnalysisFields(): SchemaFieldDraft[] {
  return [createSchemaField({
    name: 'report',
    title: '分析报告',
    description: 'AI 最终生成的完整报告内容',
    kind: 'string',
    required: true,
  })]
}

export default function V2Metadata() {
  const { message } = App.useApp()
  const client = useQueryClient()
  const [kind, setKind] = useState<CreateKind>()
  const [saving, setSaving] = useState(false)
  const [schemaFields, setSchemaFields] = useState<SchemaFieldDraft[]>(defaultDatasetFields)
  const [analysisFields, setAnalysisFields] = useState<SchemaFieldDraft[]>(defaultAnalysisFields)
  const [analysisPreset, setAnalysisPreset] = useState('spec')
  const [form] = Form.useForm()

  const schemas = useQuery({ queryKey: ['v2-schemas'], queryFn: v2CatalogApi.listSchemas })
  const analysis = useQuery({ queryKey: ['v2-analysis-profiles'], queryFn: v2CatalogApi.listAnalysisProfiles })
  const extraction = useQuery({ queryKey: ['v2-extraction-profiles'], queryFn: v2CatalogApi.listExtractionProfiles })
  const retrieval = useQuery({ queryKey: ['v2-retrieval-profiles'], queryFn: v2CatalogApi.listRetrievalProfiles })
  const assets = useQuery({ queryKey: ['v2-asset-sets'], queryFn: v2CatalogApi.listAssetSets })

  const schemaOptions = (schemas.data?.schemas ?? []).map((item) => ({ value: item.id, label: item.name }))
  const selectedSchemaID = Form.useWatch('schema_id', form) as string | undefined
  const selectedSchema = (schemas.data?.schemas ?? []).find((item) => item.id === selectedSchemaID)
  const textFieldOptions = schemaFieldOptions(selectedSchema?.json_schema, true)
  const filterFieldOptions = filterOptions(selectedSchema)

  const open = (next: CreateKind) => {
    setKind(next)
    form.resetFields()
    if (next === 'schema') setSchemaFields(defaultDatasetFields())
    if (next === 'analysis') {
      setAnalysisPreset('spec')
      setAnalysisFields(defaultAnalysisFields())
      form.setFieldsValue({ preset: 'spec', instruction: analysisPresets.spec.instruction })
    }
    if (next === 'extraction') form.setFieldsValue({ record_granularity: '每个业务实体一条记录' })
    if (next === 'retrieval') form.setFieldsValue({
      analyzer: 'standard',
      chunk_size: 800,
      chunk_overlap: 100,
      chunker_version: 'rune_v1',
      method: 'rrf',
      rank_constant: 60,
      lexical_candidates: 100,
      vector_candidates: 100,
      lexical_fields: [],
      vector_fields: [],
      filter_fields: [],
    })
  }

  const close = () => {
    if (!saving) setKind(undefined)
  }

  const onAnalysisPresetChange = (preset: string) => {
    setAnalysisPreset(preset)
    if (preset === 'custom') {
      setAnalysisFields(defaultAnalysisFields())
      form.setFieldsValue({ instruction: '' })
      return
    }
    form.setFieldsValue({ instruction: analysisPresets[preset].instruction })
  }

  const save = async (values: Record<string, any>) => {
    setSaving(true)
    try {
      if (kind === 'schema') {
        const fieldError = validateSchemaFields(schemaFields)
        if (fieldError) throw new Error(fieldError)
        await v2CatalogApi.createSchema({
          name: values.name,
          description: values.description,
          json_schema: buildObjectSchema(schemaFields, values.name),
          ui_schema: {},
        })
      }
      if (kind === 'analysis') {
        const outputSchema = analysisPreset === 'custom'
          ? buildCustomAnalysisSchema(analysisFields, values.name)
          : analysisPresets[analysisPreset].schema
        await v2CatalogApi.createAnalysisProfile({
          name: values.name,
          instruction: values.instruction,
          output_schema: outputSchema,
        })
      }
      if (kind === 'extraction') await v2CatalogApi.createExtractionProfile({
        name: values.name,
        target_schema_id: values.schema_id,
        record_granularity: values.record_granularity,
        system_instruction: values.instruction,
        field_guides: {},
        examples: [],
        normalization_rules: [],
        validation_rules: [],
      })
      if (kind === 'retrieval') {
        const lexicalFields = Object.fromEntries((values.lexical_fields ?? []).map((field: string) => [field, 1]))
        await v2CatalogApi.createRetrievalProfile({
          name: values.name,
          dataset_schema_id: values.schema_id,
          lexical: { fields: lexicalFields, analyzer: values.analyzer },
          vector: {
            fields: values.vector_fields ?? [],
            chunk_size: values.chunk_size,
            chunk_overlap: values.chunk_overlap,
            chunker_version: values.chunker_version,
            embedding_model: '',
          },
          filter_fields: values.filter_fields ?? [],
          fusion: {
            method: values.method,
            rank_constant: values.rank_constant,
            lexical_candidates: values.lexical_candidates,
            vector_candidates: values.vector_candidates,
          },
        })
      }
      if (kind === 'assets') {
        const files: File[] = values.files?.fileList?.map((item: any) => item.originFileObj).filter(Boolean) ?? []
        const uploaded = await Promise.all(files.map((file) => v2CatalogApi.uploadAsset(file)))
        await v2CatalogApi.createAssetSet({ name: values.name, asset_ids: uploaded.map((item) => item.asset.id) })
      }
      message.success('新版本已创建，现有任务不会受到影响')
      setKind(undefined)
      await Promise.all([
        client.invalidateQueries({ queryKey: ['v2-schemas'] }),
        client.invalidateQueries({ queryKey: ['v2-analysis-profiles'] }),
        client.invalidateQueries({ queryKey: ['v2-extraction-profiles'] }),
        client.invalidateQueries({ queryKey: ['v2-retrieval-profiles'] }),
        client.invalidateQueries({ queryKey: ['v2-asset-sets'] }),
      ])
    } catch (error) {
      message.error((error as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const profileColumns = [
    { title: '名称', dataIndex: 'name' },
    { title: '版本指纹', dataIndex: 'profile_hash', render: (value: string) => <Text code>{value?.slice(0, 12)}</Text> },
    { title: '创建时间', dataIndex: 'created_at', render: formatTime },
  ]

  return <>
    <Card title={<div>
      <Title level={4} style={{ margin: 0 }}>元数据与资源</Title>
      <Paragraph type="secondary" style={{ margin: 0 }}>
        先定义数据长什么样，再配置 AI 如何抽取、分析和检索。所有版本创建后冻结，已有任务不会被后来修改影响。
      </Paragraph>
    </div>}>
      <Tabs items={[
        {
          key: 'schemas',
          label: '数据结构',
          children: <ResourceTable
            action={() => open('schema')}
            actionLabel="创建数据结构"
            description="用字段编辑器描述一条数据包含什么，无需编写 JSON Schema。"
            data={schemas.data?.schemas}
            columns={[
              { title: '名称', dataIndex: 'name' },
              { title: '包含字段', render: (_: unknown, item: V2Schema) => <SchemaFieldSummary schema={item.json_schema} /> },
              { title: '版本指纹', dataIndex: 'schema_hash', render: (value: string) => <Text code>{value.slice(0, 12)}</Text> },
            ]}
          />,
        },
        {
          key: 'analysis',
          label: '分析规则',
          children: <ResourceTable
            action={() => open('analysis')}
            actionLabel="创建分析规则"
            description="选择业务模板或自定义输出字段，告诉 AI 要分析什么、交付什么。"
            data={analysis.data?.analysis_profiles}
            columns={[
              { title: '名称', dataIndex: 'name' },
              { title: '输出内容', render: (_: unknown, item: any) => schemaSummary(item.output_schema).join('、') },
              ...profileColumns.slice(1),
            ]}
          />,
        },
        {
          key: 'extraction',
          label: '抽取规则',
          children: <ResourceTable
            action={() => open('extraction')}
            actionLabel="创建抽取规则"
            description="把非结构化文件抽取为指定数据结构。"
            data={extraction.data?.extraction_profiles}
            columns={[
              { title: '名称', dataIndex: 'name' },
              { title: '目标数据结构', dataIndex: 'target_schema_id', render: (value: string) => schemaName(schemas.data?.schemas, value) },
              ...profileColumns.slice(1),
            ]}
          />,
        },
        {
          key: 'retrieval',
          label: '检索规则',
          children: <ResourceTable
            action={() => open('retrieval')}
            actionLabel="创建检索规则"
            description="从数据结构中选择哪些字段用于精准搜索、语义搜索和筛选。"
            data={retrieval.data?.retrieval_profiles}
            columns={[
              { title: '名称', dataIndex: 'name' },
              { title: '目标数据结构', dataIndex: 'dataset_schema_id', render: (value: string) => schemaName(schemas.data?.schemas, value) },
              ...profileColumns.slice(1),
            ]}
          />,
        },
        {
          key: 'assets',
          label: '文件集',
          children: <ResourceTable
            action={() => open('assets')}
            actionLabel="上传文件集"
            description="把一组原始文件作为任务的固定输入。"
            data={assets.data?.asset_sets}
            columns={[
              { title: '文件集', dataIndex: 'name' },
              { title: '资源 ID', dataIndex: 'id', render: (value: string) => <Text code>{value}</Text> },
              { title: '创建时间', dataIndex: 'created_at', render: formatTime },
            ]}
          />,
        },
      ]} />
    </Card>

    <Modal
      width={kind === 'schema' || kind === 'analysis' ? 1000 : 760}
      title={`创建${kindLabel(kind)}`}
      open={Boolean(kind)}
      onCancel={close}
      onOk={() => form.submit()}
      okText="保存为新版本"
      confirmLoading={saving}
      styles={{ body: { maxHeight: '72vh', overflowY: 'auto', paddingTop: 12 } }}
    >
      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        message="新版本不会覆盖旧版本"
        description="已运行和已创建的任务继续使用原版本；新任务可选择这里创建的新版本。"
      />
      <Form form={form} layout="vertical" onFinish={save} requiredMark="optional">
        <Form.Item name="name" label="版本名称" rules={[{ required: true, message: '请输入一个便于业务识别的名称' }]}>
          <Input placeholder="例如：产品资料结构 2026-08" />
        </Form.Item>

        {kind === 'schema' && <>
          <Form.Item name="description" label="用途说明">
            <Input placeholder="例如：用于保存清洗后的产品规格与库存信息" />
          </Form.Item>
          <Card size="small" title="一条数据包含哪些字段" extra={<Text type="secondary">字段编码已自动生成，也可以修改</Text>}>
            <SchemaFieldEditor fields={schemaFields} onChange={setSchemaFields} />
          </Card>
        </>}

        {kind === 'analysis' && <>
          <Form.Item name="preset" label="这次要完成什么分析" rules={[{ required: true }]}>
            <Select
              options={Object.entries(analysisPresets).map(([value, preset]) => ({ value, label: preset.label }))}
              onChange={onAnalysisPresetChange}
            />
          </Form.Item>
          <Alert
            type="success"
            showIcon
            style={{ marginBottom: 16 }}
            message={analysisPresets[analysisPreset].description}
            description={<Space wrap style={{ marginTop: 6 }}>
              {analysisPresets[analysisPreset].outputs.map((output) => <Tag color="green" key={output}>{output}</Tag>)}
            </Space>}
          />
          <Form.Item
            name="instruction"
            label="分析要求"
            extra="用业务语言描述判断标准、重点和禁止事项，不需要编写提示词模板。"
            rules={[{ required: true, message: '请描述分析要求' }]}
          >
            <Input.TextArea rows={6} placeholder="例如：只使用有来源依据的事实；无法确认时列为待决策项。" />
          </Form.Item>
          {analysisPreset === 'custom' && <Card size="small" title="AI 需要输出哪些字段">
            <SchemaFieldEditor fields={analysisFields} onChange={setAnalysisFields} />
          </Card>}
        </>}

        {kind === 'extraction' && <>
          <Form.Item name="schema_id" label="抽取到哪种数据结构" rules={[{ required: true, message: '请选择目标数据结构' }]}>
            <Select showSearch optionFilterProp="label" options={schemaOptions} placeholder="选择数据结构" />
          </Form.Item>
          <Form.Item
            name="record_granularity"
            label="一条记录代表什么"
            extra="例如：每个产品一条记录、每个功能点一条记录、每个故障一条记录。"
            rules={[{ required: true }]}
          >
            <Input />
          </Form.Item>
          <Form.Item name="instruction" label="抽取要求" rules={[{ required: true, message: '请描述抽取要求' }]}>
            <Input.TextArea rows={6} placeholder="例如：只抽取原文明确出现的值；缺失字段留空，不要猜测。" />
          </Form.Item>
        </>}

        {kind === 'retrieval' && <>
          <Form.Item name="schema_id" label="为哪种数据建立索引" rules={[{ required: true, message: '请选择数据结构' }]}>
            <Select
              showSearch
              optionFilterProp="label"
              options={schemaOptions}
              placeholder="选择数据结构"
              onChange={() => form.setFieldsValue({ lexical_fields: [], vector_fields: [], filter_fields: [] })}
            />
          </Form.Item>
          <Row gutter={16}>
            <Col span={12}>
              <Form.Item
                name="lexical_fields"
                label="用于精准搜索的字段"
                extra="适合编号、名称、术语等需要精确命中的文本。"
                rules={[{ required: true, message: '至少选择一个精准搜索字段' }]}
              >
                <Select mode="multiple" options={textFieldOptions} disabled={!selectedSchema} placeholder="选择文本字段" />
              </Form.Item>
            </Col>
            <Col span={12}>
              <Form.Item
                name="vector_fields"
                label="用于语义搜索的字段"
                extra="适合标题、描述、正文等自然语言文本。"
                rules={[{ required: true, message: '至少选择一个语义搜索字段' }]}
              >
                <Select mode="multiple" options={textFieldOptions} disabled={!selectedSchema} placeholder="选择文本字段" />
              </Form.Item>
            </Col>
          </Row>
          <Form.Item name="filter_fields" label="可作为筛选条件的字段" extra="例如：状态、类型、日期、是否启用。">
            <Select mode="multiple" options={filterFieldOptions} disabled={!selectedSchema} placeholder="可不选" />
          </Form.Item>
          <Collapse items={[{
            key: 'advanced',
            label: '高级索引参数（通常保持默认即可）',
            children: <>
              <Row gutter={12}>
                <Col span={12}><Form.Item name="chunk_size" label="语义片段长度"><InputNumber min={32} max={8000} style={{ width: '100%' }} /></Form.Item></Col>
                <Col span={12}><Form.Item name="chunk_overlap" label="相邻片段重叠长度"><InputNumber min={0} style={{ width: '100%' }} /></Form.Item></Col>
              </Row>
              <Row gutter={12}>
                <Col span={12}><Form.Item name="lexical_candidates" label="精准候选数量"><InputNumber min={1} max={1000} style={{ width: '100%' }} /></Form.Item></Col>
                <Col span={12}><Form.Item name="vector_candidates" label="语义候选数量"><InputNumber min={1} max={1000} style={{ width: '100%' }} /></Form.Item></Col>
              </Row>
            </>,
          }]} />
          <Form.Item name="analyzer" hidden><Input /></Form.Item>
          <Form.Item name="method" hidden><Input /></Form.Item>
          <Form.Item name="rank_constant" hidden><InputNumber /></Form.Item>
          <Form.Item name="chunker_version" hidden><Input /></Form.Item>
        </>}

        {kind === 'assets' && <Form.Item name="files" label="上传文件" rules={[{ required: true, message: '请至少选择一个文件' }]}>
          <Upload.Dragger multiple beforeUpload={() => false}>
            <p><InboxOutlined style={{ fontSize: 28 }} /></p>
            <p>拖入文件或点击选择</p>
          </Upload.Dragger>
        </Form.Item>}
      </Form>
    </Modal>
  </>
}

function buildCustomAnalysisSchema(fields: SchemaFieldDraft[], title: string) {
  const fieldError = validateSchemaFields(fields, '输出字段')
  if (fieldError) throw new Error(fieldError)
  return buildObjectSchema(fields, title)
}

function filterOptions(schema?: V2Schema) {
  return Object.entries(schema?.json_schema.properties ?? {})
    .filter(([, property]) => {
      const allowed = ['string', 'number', 'integer', 'boolean']
      return allowed.includes(property.type as string)
        || (property.type === 'array' && allowed.includes(property.items?.type as string))
    })
    .map(([name, property]) => ({ value: name, label: property.title ? `${property.title}（${name}）` : name }))
}

function ResourceTable({
  action,
  actionLabel,
  description,
  data,
  columns,
}: {
  action: () => void
  actionLabel: string
  description: string
  data?: any[]
  columns: any[]
}) {
  return <Space direction="vertical" size={12} style={{ width: '100%' }}>
    <Space style={{ width: '100%', justifyContent: 'space-between' }}>
      <Text type="secondary">{description}</Text>
      <Button type="primary" icon={<PlusOutlined />} onClick={action}>{actionLabel}</Button>
    </Space>
    <Table rowKey="id" pagination={false} dataSource={data ?? []} columns={columns} />
  </Space>
}

function schemaName(schemas: V2Schema[] | undefined, id: string) {
  return schemas?.find((item) => item.id === id)?.name ?? id
}

function formatTime(value: string) {
  return new Date(value).toLocaleString('zh-CN')
}

function kindLabel(kind?: CreateKind) {
  return ({
    schema: '数据结构版本',
    analysis: '分析规则版本',
    extraction: '抽取规则版本',
    retrieval: '检索规则版本',
    assets: '文件集',
  } as Record<string, string>)[kind ?? ''] ?? ''
}
