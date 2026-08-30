import { useEffect, useMemo, useState } from 'react'
import {
  Alert, App, Button, Card, Col, Collapse, Form, Input, InputNumber,
  Modal, Row, Select, Space, Tag,
} from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { v2CatalogApi } from '../../api/v2/catalog'
import type {
  V2AnalysisProfile, V2ExtractionProfile, V2JSONSchema,
  V2RetrievalProfile, V2Schema,
} from '../../api/v2/types'
import {
  buildObjectSchema, createSchemaField, SchemaFieldEditor,
  schemaFieldOptions, validateSchemaFields, type SchemaFieldDraft,
} from './SchemaFieldEditor'

export type EmbeddedResourceKind = 'schema' | 'analysis' | 'extraction' | 'retrieval'
export type EmbeddedResource = V2Schema | V2AnalysisProfile | V2ExtractionProfile | V2RetrievalProfile

interface Props {
  kind?: EmbeddedResourceKind
  fixedSchemaId?: string
  onCancel: () => void
  onCreated: (resource: EmbeddedResource) => void
}

const analysisPresets: Record<string, {
  label: string
  description: string
  instruction: string
  outputSchema?: V2JSONSchema
}> = {
  spec: {
    label: '产品方案',
    description: '生成包含目标、范围、用户流程、验收标准和风险的 Markdown 方案。',
    instruction: '基于产品与需求知识生成可执行产品方案，覆盖目标、范围、用户流程、验收标准、风险与待决策项。只使用有来源依据的事实，report 输出完整 Markdown。',
    outputSchema: {
      type: 'object', additionalProperties: false, required: ['report'],
      properties: { report: { type: 'string', title: '产品方案' } },
    },
  },
  bug: {
    label: 'Bug 分析',
    description: '生成问题摘要、根因、严重程度、修复建议和完整报告。',
    instruction: '分析问题现象与产品知识，输出可验证的根因、影响、严重程度和修复建议。无法确认时明确列为待核实，report 输出完整 Markdown。',
    outputSchema: {
      type: 'object', additionalProperties: false,
      required: ['summary', 'root_cause', 'severity', 'fix_suggestion', 'report'],
      properties: {
        summary: { type: 'string', title: '问题摘要' },
        root_cause: { type: 'string', title: '根因' },
        severity: { type: 'string', title: '严重程度' },
        fix_suggestion: { type: 'string', title: '修复建议' },
        report: { type: 'string', title: '分析报告' },
      },
    },
  },
  custom: {
    label: '自定义分析',
    description: '自行定义 AI 需要输出的结构化字段。',
    instruction: '',
  },
}

function initialSchemaFields(): SchemaFieldDraft[] {
  return [createSchemaField({
    name: 'id', title: '唯一标识', description: '每条数据的稳定唯一编号',
    kind: 'string', required: true,
  })]
}

function initialAnalysisFields(): SchemaFieldDraft[] {
  return [createSchemaField({
    name: 'report', title: '分析报告', description: 'AI 最终生成的完整报告',
    kind: 'string', required: true,
  })]
}

const retrievalDefaults = {
  analyzer: 'standard', chunk_size: 800, chunk_overlap: 100,
  chunker_version: 'rune_v1', method: 'rrf', rank_constant: 60,
  lexical_candidates: 100, vector_candidates: 100,
  lexical_fields: [], vector_fields: [], filter_fields: [],
}

export default function EmbeddedResourceCreate({ kind, fixedSchemaId, onCancel, onCreated }: Props) {
  const { message } = App.useApp()
  const client = useQueryClient()
  const [form] = Form.useForm<Record<string, any>>()
  const [saving, setSaving] = useState(false)
  const [schemaFields, setSchemaFields] = useState<SchemaFieldDraft[]>(initialSchemaFields)
  const [analysisFields, setAnalysisFields] = useState<SchemaFieldDraft[]>(initialAnalysisFields)
  const [analysisPreset, setAnalysisPreset] = useState('spec')
  const [nestedSchemaOpen, setNestedSchemaOpen] = useState(false)
  const schemas = useQuery({ queryKey: ['v2-schemas'], queryFn: v2CatalogApi.listSchemas })
  // Hook 必须在每次渲染中保持完全相同的调用顺序；不能放进 fixedSchemaId 的短路表达式。
  const watchedSchemaID = Form.useWatch('schema_id', form) as string | undefined
  const schemaID = fixedSchemaId || watchedSchemaID
  const selectedSchema = useMemo(
    () => (schemas.data?.schemas ?? []).find((item) => item.id === schemaID),
    [schemaID, schemas.data],
  )
  const textFields = schemaFieldOptions(selectedSchema?.json_schema, true)
  const filterFields = schemaFieldOptions(selectedSchema?.json_schema)

  useEffect(() => {
    if (!kind) return
    form.resetFields()
    if (kind === 'schema') setSchemaFields(initialSchemaFields())
    if (kind === 'analysis') {
      setAnalysisPreset('spec')
      setAnalysisFields(initialAnalysisFields())
      form.setFieldsValue({ preset: 'spec', instruction: analysisPresets.spec.instruction })
    }
    if (kind === 'extraction') form.setFieldsValue({
      schema_id: fixedSchemaId, record_granularity: '每个业务实体一条记录',
    })
    if (kind === 'retrieval') form.setFieldsValue({ ...retrievalDefaults, schema_id: fixedSchemaId })
  }, [fixedSchemaId, form, kind])

  const presetChanged = (preset: string) => {
    setAnalysisPreset(preset)
    if (preset === 'custom') {
      setAnalysisFields(initialAnalysisFields())
      form.setFieldValue('instruction', '')
    } else {
      form.setFieldValue('instruction', analysisPresets[preset].instruction)
    }
  }

  const save = async (values: Record<string, any>) => {
    if (!kind) return
    setSaving(true)
    try {
      let created: EmbeddedResource
      if (kind === 'schema') {
        const issue = validateSchemaFields(schemaFields)
        if (issue) throw new Error(issue)
        const response = await v2CatalogApi.createSchema({
          name: values.name,
          description: values.description,
          json_schema: buildObjectSchema(schemaFields, values.name),
          ui_schema: {},
        })
        created = response.schema
      } else if (kind === 'analysis') {
        if (analysisPreset === 'custom') {
          const issue = validateSchemaFields(analysisFields, '输出字段')
          if (issue) throw new Error(issue)
        }
        const response = await v2CatalogApi.createAnalysisProfile({
          name: values.name,
          instruction: values.instruction,
          output_schema: analysisPreset === 'custom'
            ? buildObjectSchema(analysisFields, values.name)
            : analysisPresets[analysisPreset].outputSchema!,
        })
        created = response.analysis_profile
      } else if (kind === 'extraction') {
        const response = await v2CatalogApi.createExtractionProfile({
          name: values.name,
          target_schema_id: values.schema_id,
          record_granularity: values.record_granularity,
          system_instruction: values.instruction,
          field_guides: {}, examples: [], normalization_rules: {}, validation_rules: {},
        })
        created = response.extraction_profile
      } else {
        const lexicalFields = Object.fromEntries((values.lexical_fields ?? []).map((field: string) => [field, 1]))
        const response = await v2CatalogApi.createRetrievalProfile({
          name: values.name,
          dataset_schema_id: fixedSchemaId || values.schema_id,
          lexical: { fields: lexicalFields, analyzer: values.analyzer },
          vector: {
            fields: values.vector_fields ?? [], chunk_size: values.chunk_size,
            chunk_overlap: values.chunk_overlap, chunker_version: values.chunker_version,
            embedding_model: '',
          },
          filter_fields: values.filter_fields ?? [],
          fusion: {
            method: values.method, rank_constant: values.rank_constant,
            lexical_candidates: values.lexical_candidates,
            vector_candidates: values.vector_candidates,
          },
        })
        created = response.retrieval_profile
      }
      await client.invalidateQueries({ queryKey: queryKeyFor(kind) })
      message.success(`${kindLabel(kind)}已创建并选中`)
      onCreated(created)
      onCancel()
    } catch (error) {
      message.error((error as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const chooseOrCreateSchema = (label: string) => <Form.Item label={label} required>
    <Space.Compact style={{ width: '100%' }}>
      <Form.Item name="schema_id" noStyle rules={[{ required: true, message: '请选择数据结构' }]}>
        <Select
          showSearch
          optionFilterProp="label"
          style={{ width: 'calc(100% - 112px)' }}
          options={(schemas.data?.schemas ?? []).map((item) => ({ value: item.id, label: item.name }))}
          onChange={() => form.setFieldsValue({ lexical_fields: [], vector_fields: [], filter_fields: [] })}
          placeholder="选择已有数据结构"
        />
      </Form.Item>
      <Button icon={<PlusOutlined />} onClick={() => setNestedSchemaOpen(true)}>新建结构</Button>
    </Space.Compact>
  </Form.Item>

  return <>
    <Modal
      width={kind === 'schema' || kind === 'analysis' ? 980 : 760}
      title={kind ? `创建${kindLabel(kind)}` : ''}
      open={Boolean(kind)}
      onCancel={() => !saving && onCancel()}
      onOk={() => form.submit()}
      okText="创建并选中"
      confirmLoading={saving}
      destroyOnHidden
      styles={{ body: { maxHeight: '72vh', overflowY: 'auto', paddingTop: 12 } }}
    >
      <Alert
        type="info" showIcon style={{ marginBottom: 16 }}
        message="在当前操作中创建，无需离开页面"
        description="创建完成后会立即回填到当前步骤或数据集，操作上下文不会中断。"
      />
      <Form form={form} layout="vertical" onFinish={save} requiredMark="optional">
        <Form.Item name="name" label="名称" rules={[{ required: true, message: '请输入便于业务识别的名称' }]}>
          <Input placeholder="例如：产品资料抽取规则 2026-08" />
        </Form.Item>

        {kind === 'schema' && <>
          <Form.Item name="description" label="用途说明"><Input /></Form.Item>
          <Card size="small" title="一条数据包含哪些字段">
            <SchemaFieldEditor fields={schemaFields} onChange={setSchemaFields} />
          </Card>
        </>}

        {kind === 'analysis' && <>
          <Form.Item name="preset" label="分析类型" rules={[{ required: true }]}>
            <Select options={Object.entries(analysisPresets).map(([value, preset]) => ({ value, label: preset.label }))} onChange={presetChanged} />
          </Form.Item>
          <Alert
            type="success" showIcon style={{ marginBottom: 16 }}
            message={analysisPresets[analysisPreset].description}
          />
          <Form.Item name="instruction" label="分析要求" rules={[{ required: true, message: '请描述分析要求' }]}>
            <Input.TextArea rows={6} placeholder="描述判断标准、重点和禁止事项" />
          </Form.Item>
          {analysisPreset === 'custom' && <Card size="small" title="AI 需要输出哪些字段">
            <SchemaFieldEditor fields={analysisFields} onChange={setAnalysisFields} />
          </Card>}
        </>}

        {kind === 'extraction' && <>
          {fixedSchemaId
            ? <Form.Item label="目标数据结构"><Tag color="blue">{selectedSchema?.name ?? fixedSchemaId}</Tag></Form.Item>
            : chooseOrCreateSchema('抽取到哪种数据结构')}
          {fixedSchemaId && <Form.Item name="schema_id" hidden><Input /></Form.Item>}
          <Form.Item name="record_granularity" label="一条记录代表什么" rules={[{ required: true }]}>
            <Input placeholder="例如：每个产品一条记录" />
          </Form.Item>
          <Form.Item name="instruction" label="抽取要求" rules={[{ required: true, message: '请描述抽取要求' }]}>
            <Input.TextArea rows={6} placeholder="例如：只抽取原文明确出现的值；缺失字段留空，不要猜测。" />
          </Form.Item>
        </>}

        {kind === 'retrieval' && <>
          {fixedSchemaId
            ? <Form.Item label="索引数据结构"><Tag color="blue">{selectedSchema?.name ?? fixedSchemaId}</Tag></Form.Item>
            : chooseOrCreateSchema('为哪种数据建立索引')}
          {fixedSchemaId && <Form.Item name="schema_id" hidden><Input /></Form.Item>}
          <Row gutter={16}>
            <Col span={12}><Form.Item name="lexical_fields" label="精准搜索字段" rules={[{ required: true, message: '至少选择一个字段' }]}><Select mode="multiple" options={textFields} disabled={!selectedSchema} /></Form.Item></Col>
            <Col span={12}><Form.Item name="vector_fields" label="语义搜索字段" rules={[{ required: true, message: '至少选择一个字段' }]}><Select mode="multiple" options={textFields} disabled={!selectedSchema} /></Form.Item></Col>
          </Row>
          <Form.Item name="filter_fields" label="筛选字段"><Select mode="multiple" options={filterFields} disabled={!selectedSchema} /></Form.Item>
          <Collapse items={[{
            key: 'advanced', label: '高级索引参数（通常保持默认即可）', children: <>
              <Row gutter={12}>
                <Col span={12}><Form.Item name="chunk_size" label="语义片段长度"><InputNumber min={32} max={8000} style={{ width: '100%' }} /></Form.Item></Col>
                <Col span={12}><Form.Item name="chunk_overlap" label="相邻片段重叠"><InputNumber min={0} style={{ width: '100%' }} /></Form.Item></Col>
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
      </Form>
    </Modal>

    {nestedSchemaOpen && kind !== 'schema' && <EmbeddedResourceCreate
      kind="schema"
      onCancel={() => setNestedSchemaOpen(false)}
      onCreated={(resource) => {
        const schema = resource as V2Schema
        form.setFieldValue('schema_id', schema.id)
        form.setFieldsValue({ lexical_fields: [], vector_fields: [], filter_fields: [] })
      }}
    />}
  </>
}

function kindLabel(kind: EmbeddedResourceKind) {
  return ({ schema: '数据结构', analysis: '分析规则', extraction: '抽取规则', retrieval: '索引规则' } as const)[kind]
}

function queryKeyFor(kind: EmbeddedResourceKind) {
  return ({ schema: ['v2-schemas'], analysis: ['v2-analysis-profiles'], extraction: ['v2-extraction-profiles'], retrieval: ['v2-retrieval-profiles'] } as const)[kind]
}
