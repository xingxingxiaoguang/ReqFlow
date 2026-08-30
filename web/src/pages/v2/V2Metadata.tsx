import { useState } from 'react'
import { App, Button, Card, Col, Form, Input, InputNumber, Modal, Row, Select, Space, Table, Tabs, Typography, Upload } from 'antd'
import { InboxOutlined, PlusOutlined } from '@ant-design/icons'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { v2CatalogApi } from '../../api/v2/catalog'
import type { V2JSONSchema } from '../../api/v2/types'

const { Paragraph, Text, Title } = Typography
type CreateKind = 'schema' | 'analysis' | 'extraction' | 'retrieval' | 'assets'

const analysisPresets: Record<string, { instruction: string; schema: V2JSONSchema }> = {
  bug: {
    instruction: '分析问题现象与产品知识，输出可验证的根因、影响、修复建议。records 中每条记录必须包含来源引用，report 输出完整 Markdown 报告。',
    schema: { type: 'object', additionalProperties: false, required: ['records', 'report'], properties: {
      records: { type: 'array', items: { type: 'object', additionalProperties: false, required: ['fields', 'provenance'], properties: {
        fields: { type: 'object', additionalProperties: false, required: ['id', 'title', 'root_cause', 'severity', 'fix_suggestion'], properties: {
          id: { type: 'string' }, title: { type: 'string' }, root_cause: { type: 'string' }, severity: { type: 'string' }, fix_suggestion: { type: 'string' },
        } },
        provenance: { type: 'object', additionalProperties: false, required: ['source_refs'], properties: { source_refs: { type: 'array', minItems: 1, items: { type: 'object', additionalProperties: false, required: ['dataset_item_id'], properties: { dataset_item_id: { type: 'string' }, quote: { type: 'string' } } } } } },
      } } }, report: { type: 'string' },
    } },
  },
  spec: {
    instruction: '基于产品与需求知识生成可执行产品方案，覆盖目标、范围、用户流程、验收标准、风险与待决策项，report 输出 Markdown。',
    schema: { type: 'object', additionalProperties: false, required: ['report'], properties: { report: { type: 'string' } } },
  },
  graph: {
    instruction: '从知识中抽取去重后的实体节点和有方向关系。nodes 与 edges 必须保留来源引用并分别可直接写入目标数据集。',
    schema: { type: 'object', additionalProperties: false, required: ['nodes', 'edges'], properties: {
      nodes: { type: 'array', items: { type: 'object', additionalProperties: false, required: ['fields', 'provenance'], properties: {
        fields: { type: 'object', additionalProperties: false, required: ['id', 'name', 'type'], properties: { id: { type: 'string' }, name: { type: 'string' }, type: { type: 'string' } } },
        provenance: { type: 'object', additionalProperties: false, required: ['source_refs'], properties: { source_refs: { type: 'array', minItems: 1, items: { type: 'object', additionalProperties: false, required: ['dataset_item_id'], properties: { dataset_item_id: { type: 'string' }, quote: { type: 'string' } } } } } },
      } } }, edges: { type: 'array', items: { type: 'object', additionalProperties: false, required: ['fields', 'provenance'], properties: {
        fields: { type: 'object', additionalProperties: false, required: ['id', 'source_id', 'target_id', 'type'], properties: { id: { type: 'string' }, source_id: { type: 'string' }, target_id: { type: 'string' }, type: { type: 'string' } } },
        provenance: { type: 'object', additionalProperties: false, required: ['source_refs'], properties: { source_refs: { type: 'array', minItems: 1, items: { type: 'object', additionalProperties: false, required: ['dataset_item_id'], properties: { dataset_item_id: { type: 'string' }, quote: { type: 'string' } } } } } },
      } } },
    } },
  },
}

export default function V2Metadata() {
  const { message } = App.useApp(); const client = useQueryClient(); const [kind, setKind] = useState<CreateKind>(); const [form] = Form.useForm()
  const schemas = useQuery({ queryKey: ['v2-schemas'], queryFn: v2CatalogApi.listSchemas })
  const analysis = useQuery({ queryKey: ['v2-analysis-profiles'], queryFn: v2CatalogApi.listAnalysisProfiles })
  const extraction = useQuery({ queryKey: ['v2-extraction-profiles'], queryFn: v2CatalogApi.listExtractionProfiles })
  const retrieval = useQuery({ queryKey: ['v2-retrieval-profiles'], queryFn: v2CatalogApi.listRetrievalProfiles })
  const assets = useQuery({ queryKey: ['v2-asset-sets'], queryFn: v2CatalogApi.listAssetSets })
  const schemaOptions = (schemas.data?.schemas ?? []).map((item) => ({ value: item.id, label: item.name }))
  const open = (next: CreateKind) => { setKind(next); form.resetFields(); if (next === 'retrieval') form.setFieldsValue({ analyzer: 'standard', chunk_size: 800, chunk_overlap: 100, chunker_version: 'v1', method: 'rrf', rank_constant: 60, lexical_candidates: 100, vector_candidates: 100 }) }
  const save = async (values: Record<string, any>) => {
    try {
      if (kind === 'schema') await v2CatalogApi.createSchema({ name: values.name, description: values.description, json_schema: JSON.parse(values.json_schema), ui_schema: {} })
      if (kind === 'analysis') await v2CatalogApi.createAnalysisProfile({ name: values.name, instruction: values.instruction, output_schema: JSON.parse(values.output_schema) })
      if (kind === 'extraction') await v2CatalogApi.createExtractionProfile({ name: values.name, target_schema_id: values.schema_id, record_granularity: values.record_granularity, system_instruction: values.instruction, field_guides: {}, examples: [], normalization_rules: {}, validation_rules: {} })
      if (kind === 'retrieval') {
        const lexicalFields = Object.fromEntries(values.lexical_fields.split(',').map((field: string) => [field.trim(), 1]).filter(([field]: [string, number]) => field))
        await v2CatalogApi.createRetrievalProfile({ name: values.name, dataset_schema_id: values.schema_id, lexical: { fields: lexicalFields, analyzer: values.analyzer }, vector: { fields: values.vector_fields.split(',').map((item: string) => item.trim()).filter(Boolean), chunk_size: values.chunk_size, chunk_overlap: values.chunk_overlap, chunker_version: values.chunker_version, embedding_model: '' }, filter_fields: (values.filter_fields ?? '').split(',').map((item: string) => item.trim()).filter(Boolean), fusion: { method: values.method, rank_constant: values.rank_constant, lexical_candidates: values.lexical_candidates, vector_candidates: values.vector_candidates } })
      }
      if (kind === 'assets') {
        const files: File[] = values.files?.fileList?.map((item: any) => item.originFileObj).filter(Boolean) ?? []
        const uploaded = await Promise.all(files.map((file) => v2CatalogApi.uploadAsset(file)))
        await v2CatalogApi.createAssetSet({ name: values.name, asset_ids: uploaded.map((item) => item.asset.id) })
      }
      message.success('不可变 V2 资源已创建'); setKind(undefined); client.invalidateQueries({ queryKey: ['v2-schemas'] }); client.invalidateQueries({ queryKey: ['v2-analysis-profiles'] }); client.invalidateQueries({ queryKey: ['v2-extraction-profiles'] }); client.invalidateQueries({ queryKey: ['v2-retrieval-profiles'] }); client.invalidateQueries({ queryKey: ['v2-asset-sets'] })
    } catch (error) { message.error((error as Error).message) }
  }
  const profileColumns = [{ title: '名称', dataIndex: 'name' }, { title: '版本哈希', dataIndex: 'profile_hash', render: (value: string) => <Text code>{value?.slice(0, 12)}</Text> }, { title: '创建时间', dataIndex: 'created_at', render: (value: string) => new Date(value).toLocaleString('zh-CN') }]
  return <>
    <Card title={<div><Title level={4} style={{ margin: 0 }}>V2 元数据与资源</Title><Paragraph type="secondary" style={{ margin: 0 }}>Schema 与 Profile 创建后不可修改；变更通过新版本表达，运行任务始终引用固定哈希。</Paragraph></div>}>
      <Tabs items={[
        { key: 'schemas', label: 'Dataset Schema', children: <ResourceTable action={() => open('schema')} data={schemas.data?.schemas} columns={[{ title: '名称', dataIndex: 'name' }, { title: '字段', render: (_: unknown, item: any) => Object.keys(item.json_schema?.properties ?? {}).join('、') }, { title: 'Schema Hash', dataIndex: 'schema_hash', render: (value: string) => <Text code>{value.slice(0, 12)}</Text> }]} /> },
        { key: 'analysis', label: '分析 Profile', children: <ResourceTable action={() => open('analysis')} data={analysis.data?.analysis_profiles} columns={profileColumns} /> },
        { key: 'extraction', label: '抽取 Profile', children: <ResourceTable action={() => open('extraction')} data={extraction.data?.extraction_profiles} columns={[{ title: '名称', dataIndex: 'name' }, { title: '目标 Schema', dataIndex: 'target_schema_id', render: (value: string) => (schemas.data?.schemas ?? []).find((item) => item.id === value)?.name ?? value }, ...profileColumns.slice(1)]} /> },
        { key: 'retrieval', label: '检索 Profile', children: <ResourceTable action={() => open('retrieval')} data={retrieval.data?.retrieval_profiles} columns={[{ title: '名称', dataIndex: 'name' }, { title: '目标 Schema', dataIndex: 'dataset_schema_id', render: (value: string) => (schemas.data?.schemas ?? []).find((item) => item.id === value)?.name ?? value }, ...profileColumns.slice(1)]} /> },
        { key: 'assets', label: '文件集', children: <ResourceTable action={() => open('assets')} data={assets.data?.asset_sets} columns={[{ title: '文件集', dataIndex: 'name' }, { title: 'ID', dataIndex: 'id', render: (value: string) => <Text code>{value}</Text> }, { title: '创建时间', dataIndex: 'created_at', render: (value: string) => new Date(value).toLocaleString('zh-CN') }]} /> },
      ]} />
    </Card>
    <Modal width={720} title={`创建${kindLabel(kind)}`} open={Boolean(kind)} onCancel={() => setKind(undefined)} onOk={() => form.submit()} okText="创建新版本">
      <Form form={form} layout="vertical" onFinish={save}>
        <Form.Item name="name" label="名称" rules={[{ required: true }]}><Input /></Form.Item>
        {kind === 'schema' && <><Form.Item name="description" label="说明"><Input /></Form.Item><Form.Item name="json_schema" label="JSON Schema" rules={[{ required: true }]} initialValue={'{\n  "type": "object",\n  "additionalProperties": false,\n  "required": ["id"],\n  "properties": {\n    "id": { "type": "string" }\n  }\n}'}><Input.TextArea rows={14} /></Form.Item></>}
        {kind === 'analysis' && <><Form.Item name="preset" label="业务模板"><Select options={[{ value: 'bug', label: 'Bug 分析' }, { value: 'spec', label: '产品方案' }, { value: 'graph', label: '知识图谱' }]} onChange={(value) => form.setFieldsValue({ instruction: analysisPresets[value].instruction, output_schema: JSON.stringify(analysisPresets[value].schema, null, 2) })} /></Form.Item><Form.Item name="instruction" label="分析指令" rules={[{ required: true }]}><Input.TextArea rows={5} /></Form.Item><Form.Item name="output_schema" label="结构化输出 Schema" rules={[{ required: true }]}><Input.TextArea rows={12} /></Form.Item></>}
        {kind === 'extraction' && <><Form.Item name="schema_id" label="目标 Schema" rules={[{ required: true }]}><Select options={schemaOptions} /></Form.Item><Form.Item name="record_granularity" label="记录粒度" rules={[{ required: true }]} initialValue="one_record_per_entity"><Input /></Form.Item><Form.Item name="instruction" label="抽取指令" rules={[{ required: true }]}><Input.TextArea rows={6} /></Form.Item></>}
        {kind === 'retrieval' && <><Form.Item name="schema_id" label="目标 Schema" rules={[{ required: true }]}><Select options={schemaOptions} /></Form.Item><Row gutter={16}><Col span={12}><Form.Item name="lexical_fields" label="精准检索字段（逗号）" rules={[{ required: true }]}><Input /></Form.Item></Col><Col span={12}><Form.Item name="vector_fields" label="语义检索字段（逗号）" rules={[{ required: true }]}><Input /></Form.Item></Col></Row><Form.Item name="filter_fields" label="过滤字段（逗号）"><Input /></Form.Item><Row gutter={12}><Col span={8}><Form.Item name="analyzer" label="Analyzer"><Input /></Form.Item></Col><Col span={8}><Form.Item name="chunk_size" label="Chunk 大小"><InputNumber min={100} style={{ width: '100%' }} /></Form.Item></Col><Col span={8}><Form.Item name="chunk_overlap" label="Chunk 重叠"><InputNumber min={0} style={{ width: '100%' }} /></Form.Item></Col></Row><Row gutter={12}><Col span={8}><Form.Item name="method" label="融合方法"><Select options={[{ value: 'rrf', label: 'RRF' }]} /></Form.Item></Col><Col span={8}><Form.Item name="lexical_candidates" label="精准候选"><InputNumber min={1} style={{ width: '100%' }} /></Form.Item></Col><Col span={8}><Form.Item name="vector_candidates" label="语义候选"><InputNumber min={1} style={{ width: '100%' }} /></Form.Item></Col></Row><Form.Item name="rank_constant" hidden><InputNumber /></Form.Item><Form.Item name="chunker_version" hidden><Input /></Form.Item></>}
        {kind === 'assets' && <Form.Item name="files" label="上传文件" rules={[{ required: true }]}><Upload.Dragger multiple beforeUpload={() => false}><p><InboxOutlined style={{ fontSize: 28 }} /></p><p>拖入文件或点击选择</p></Upload.Dragger></Form.Item>}
      </Form>
    </Modal>
  </>
}

function ResourceTable({ action, data, columns }: { action: () => void; data?: any[]; columns: any[] }) {
  return <Space direction="vertical" style={{ width: '100%' }}><Button type="primary" icon={<PlusOutlined />} onClick={action}>创建新版本</Button><Table rowKey="id" pagination={false} dataSource={data ?? []} columns={columns} /></Space>
}

function kindLabel(kind?: CreateKind) {
  return ({ schema: ' Dataset Schema', analysis: ' Analysis Profile', extraction: ' Extraction Profile', retrieval: ' Retrieval Profile', assets: '文件集' } as Record<string, string>)[kind ?? ''] ?? ''
}
