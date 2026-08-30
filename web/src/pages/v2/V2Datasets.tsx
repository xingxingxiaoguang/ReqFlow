import { useMemo, useState } from 'react'
import {
  App, Button, Card, Descriptions, Drawer, Empty, Form, Input, Modal,
  Popconfirm, Select, Space, Table, Tag, Typography,
} from 'antd'
import { EyeOutlined, InboxOutlined, PlusOutlined, ProfileOutlined } from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { v2CatalogApi } from '../../api/v2/catalog'
import type { JSONSchemaProperty, V2Dataset, V2RetrievalProfile } from '../../api/v2/types'
import EmbeddedResourceCreate, { type EmbeddedResource, type EmbeddedResourceKind } from './EmbeddedResourceCreate'
import { schemaFieldOptions } from './SchemaFieldEditor'

const { Paragraph, Text, Title } = Typography

interface DatasetForm {
  name: string
  description?: string
  purpose: string
  schema_id: string
  retrieval_profile_id: string
  key_fields: string[]
}

interface ResourceRequest {
  kind: Extract<EmbeddedResourceKind, 'schema' | 'retrieval'>
  target: 'dataset' | 'schema-drawer'
  fixedSchemaId?: string
}

export default function V2Datasets() {
  const { message } = App.useApp()
  const client = useQueryClient()
  const navigate = useNavigate()
  const [form] = Form.useForm<DatasetForm>()
  const [createOpen, setCreateOpen] = useState(false)
  const [schemaDataset, setSchemaDataset] = useState<V2Dataset>()
  const [resourceRequest, setResourceRequest] = useState<ResourceRequest>()
  const [creating, setCreating] = useState(false)
  const datasets = useQuery({ queryKey: ['v2-datasets'], queryFn: () => v2CatalogApi.listDatasets({ status: 'active' }) })
  const schemas = useQuery({ queryKey: ['v2-schemas'], queryFn: v2CatalogApi.listSchemas })
  const retrievalProfiles = useQuery({ queryKey: ['v2-retrieval-profiles'], queryFn: v2CatalogApi.listRetrievalProfiles })
  const schemaID = Form.useWatch('schema_id', form)
  const schemaMap = useMemo(() => new Map((schemas.data?.schemas ?? []).map((item) => [item.id, item])), [schemas.data])
  const selectedCreateSchema = schemaMap.get(schemaID)
  const drawerSchema = schemaDataset ? schemaMap.get(schemaDataset.schema_id) : undefined
  const matchingRules = (retrievalProfiles.data?.retrieval_profiles ?? []).filter((item) => item.dataset_schema_id === schemaID)
  const drawerRules = (retrievalProfiles.data?.retrieval_profiles ?? []).filter((item) => item.dataset_schema_id === schemaDataset?.schema_id)

  const openCreate = () => {
    form.resetFields()
    form.setFieldsValue({ purpose: 'base', key_fields: [] } as Partial<DatasetForm>)
    setCreateOpen(true)
  }

  const create = async (values: DatasetForm) => {
    setCreating(true)
    try {
      await v2CatalogApi.createDataset({
        name: values.name,
        description: values.description,
        purpose: values.purpose,
        schema_id: values.schema_id,
        key_fields: values.key_fields,
      })
      message.success('数据集已创建，数据结构与索引规则已就绪')
      setCreateOpen(false)
      form.resetFields()
      await client.invalidateQueries({ queryKey: ['v2-datasets'] })
    } catch (error) {
      message.error((error as Error).message)
    } finally {
      setCreating(false)
    }
  }

  const archive = async (item: V2Dataset) => {
    try {
      await v2CatalogApi.archiveDataset(item.id)
      message.success('数据集已移入归档')
      await client.invalidateQueries({ queryKey: ['v2-datasets'] })
    } catch (error) {
      message.error((error as Error).message)
    }
  }

  const createdResource = (resource: EmbeddedResource) => {
    if (!resourceRequest || resourceRequest.target !== 'dataset') return
    if (resourceRequest.kind === 'schema') {
      form.setFieldsValue({ schema_id: resource.id, retrieval_profile_id: undefined, key_fields: [] })
    } else {
      form.setFieldValue('retrieval_profile_id', resource.id)
    }
  }

  const columns: ColumnsType<V2Dataset> = [
    {
      title: '数据集',
      render: (_, item) => <Space direction="vertical" size={1}><Text strong>{item.name}</Text><Text type="secondary">{item.description || item.id}</Text></Space>,
    },
    { title: '用途', dataIndex: 'purpose', width: 120, render: (value) => <Tag color="geekblue">{purposeLabel(value)}</Tag> },
    { title: '数据结构', dataIndex: 'schema_id', width: 180, render: (value: string) => schemaMap.get(value)?.name ?? value.slice(0, 8) },
    { title: '条目', dataIndex: 'item_count', width: 90, align: 'right' },
    { title: '更新时间', dataIndex: 'updated_at', width: 180, render: (value: string) => new Date(value).toLocaleString('zh-CN') },
    {
      title: '操作', width: 260,
      render: (_, item) => <Space>
        <Button size="small" icon={<ProfileOutlined />} onClick={() => setSchemaDataset(item)}>字段</Button>
        <Button size="small" type="primary" icon={<EyeOutlined />} onClick={() => navigate(`/datasets/${item.id}`)}>详情</Button>
        <Popconfirm title="将数据集移入归档？" onConfirm={() => void archive(item)}>
          <Button size="small" danger icon={<InboxOutlined />}>归档</Button>
        </Popconfirm>
      </Space>,
    },
  ]

  return <>
    <Card
      title={<div><Title level={4} style={{ margin: 0 }}>数据管理</Title><Paragraph type="secondary" style={{ margin: 0 }}>以数据集为主体，字段、索引与检索都在数据所在位置完成。</Paragraph></div>}
      extra={<Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>创建数据集</Button>}
    >
      <Table
        rowKey="id"
        loading={datasets.isLoading}
        dataSource={datasets.data?.datasets ?? []}
        columns={columns}
        pagination={false}
        locale={{ emptyText: <Empty description="还没有数据集"><Button type="primary" onClick={openCreate}>创建第一个数据集</Button></Empty> }}
      />
    </Card>

    <Modal
      width={760}
      title="创建数据集"
      open={createOpen}
      onCancel={() => !creating && setCreateOpen(false)}
      onOk={() => form.submit()}
      okText="创建数据集"
      confirmLoading={creating}
      destroyOnHidden
    >
      <Form form={form} layout="vertical" onFinish={create} requiredMark="optional">
        <Form.Item name="name" label="数据集名称" rules={[{ required: true, whitespace: true }]}><Input placeholder="例如：产品知识库" /></Form.Item>
        <Form.Item name="description" label="用途说明"><Input.TextArea rows={2} /></Form.Item>
        <Form.Item name="purpose" label="数据用途" rules={[{ required: true }]}>
          <Select options={[
            { value: 'base', label: '基础业务数据' }, { value: 'query', label: '检索派生数据' },
            { value: 'analysis', label: '分析结果数据' }, { value: 'graph_node', label: '图谱节点' },
            { value: 'graph_edge', label: '图谱关系' },
          ]} />
        </Form.Item>

        <Form.Item label="数据结构" required>
          <Space.Compact style={{ width: '100%' }}>
            <Form.Item name="schema_id" noStyle rules={[{ required: true, message: '请选择或创建数据结构' }]}>
              <Select
                showSearch optionFilterProp="label" placeholder="选择已有数据结构"
                options={(schemas.data?.schemas ?? []).map((item) => ({ value: item.id, label: item.name }))}
                onChange={() => form.setFieldsValue({ retrieval_profile_id: undefined, key_fields: [] })}
                style={{ width: 'calc(100% - 112px)' }}
              />
            </Form.Item>
            <Button icon={<PlusOutlined />} onClick={() => setResourceRequest({ kind: 'schema', target: 'dataset' })}>新建结构</Button>
          </Space.Compact>
        </Form.Item>

        <Form.Item name="key_fields" label="业务唯一键" rules={[{ required: true, message: '至少选择一个唯一键字段' }]}>
          <Select mode="multiple" disabled={!selectedCreateSchema} options={schemaFieldOptions(selectedCreateSchema?.json_schema)} placeholder="用于识别同一条业务数据" />
        </Form.Item>

        <Form.Item label="索引规则" required>
          <Space.Compact style={{ width: '100%' }}>
            <Form.Item name="retrieval_profile_id" noStyle rules={[{ required: true, message: '请选择或创建索引规则' }]}>
              <Select
                showSearch optionFilterProp="label" disabled={!selectedCreateSchema}
                placeholder={selectedCreateSchema ? '选择适用于该结构的索引规则' : '请先选择数据结构'}
                options={matchingRules.map((item) => ({ value: item.id, label: item.name }))}
                style={{ width: 'calc(100% - 112px)' }}
              />
            </Form.Item>
            <Button
              icon={<PlusOutlined />}
              disabled={!selectedCreateSchema}
              onClick={() => setResourceRequest({ kind: 'retrieval', target: 'dataset', fixedSchemaId: selectedCreateSchema?.id })}
            >新建规则</Button>
          </Space.Compact>
        </Form.Item>
      </Form>
    </Modal>

    <Drawer
      width={820}
      title={schemaDataset ? `${schemaDataset.name} · 字段与索引` : ''}
      open={Boolean(schemaDataset)}
      onClose={() => setSchemaDataset(undefined)}
      extra={<Button icon={<PlusOutlined />} disabled={!drawerSchema} onClick={() => setResourceRequest({ kind: 'retrieval', target: 'schema-drawer', fixedSchemaId: drawerSchema?.id })}>建立索引规则</Button>}
    >
      {schemaDataset && <Space direction="vertical" size={16} style={{ width: '100%' }}>
        <Descriptions bordered size="small" column={2}>
          <Descriptions.Item label="数据结构">{drawerSchema?.name ?? schemaDataset.schema_id}</Descriptions.Item>
          <Descriptions.Item label="业务唯一键">{schemaDataset.key_fields.join('、')}</Descriptions.Item>
          <Descriptions.Item label="结构说明" span={2}>{drawerSchema?.description || '—'}</Descriptions.Item>
        </Descriptions>
        <Card size="small" title="字段定义">
          <Table
            rowKey="name" size="small" pagination={false}
            dataSource={Object.entries(drawerSchema?.json_schema.properties ?? {}).map(([name, property]) => ({ name, property }))}
            columns={fieldColumns(drawerSchema?.json_schema.required ?? [])}
            locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无字段" /> }}
          />
        </Card>
        <Card size="small" title={<Space><span>索引规则</span><Tag>{drawerRules.length}</Tag></Space>}>
          {drawerRules.length ? <Space direction="vertical" style={{ width: '100%' }}>{drawerRules.map((rule) => <IndexRuleCard key={rule.id} rule={rule} />)}</Space> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="尚未为这些字段建立索引规则"><Button type="primary" onClick={() => setResourceRequest({ kind: 'retrieval', target: 'schema-drawer', fixedSchemaId: drawerSchema?.id })}>建立第一条规则</Button></Empty>}
        </Card>
      </Space>}
    </Drawer>

    <EmbeddedResourceCreate
      kind={resourceRequest?.kind}
      fixedSchemaId={resourceRequest?.fixedSchemaId}
      onCancel={() => setResourceRequest(undefined)}
      onCreated={createdResource}
    />
  </>
}

function fieldColumns(required: string[]): ColumnsType<{ name: string; property: JSONSchemaProperty }> {
  return [
    { title: '字段', render: (_, item) => <Space direction="vertical" size={0}><Text strong>{item.property.title || item.name}</Text><Text code>{item.name}</Text></Space> },
    { title: '类型', width: 130, render: (_, item) => <Tag>{fieldType(item.property)}</Tag> },
    { title: '必填', width: 75, render: (_, item) => required.includes(item.name) ? <Tag color="red">是</Tag> : <Text type="secondary">否</Text> },
    { title: '说明', render: (_, item) => item.property.description || '—' },
  ]
}

function IndexRuleCard({ rule }: { rule: V2RetrievalProfile }) {
  const lexical = Object.keys((rule.lexical.fields ?? {}) as Record<string, unknown>)
  const vector = (rule.vector.fields ?? []) as string[]
  return <Card size="small" title={rule.name}>
    <Space wrap><Tag color="blue">精准：{lexical.join('、') || '未配置'}</Tag><Tag color="purple">语义：{vector.join('、') || '未配置'}</Tag>{rule.filter_fields.length > 0 && <Tag>筛选：{rule.filter_fields.join('、')}</Tag>}</Space>
  </Card>
}

function fieldType(property: JSONSchemaProperty) {
  if (property.type === 'array') return `数组<${property.items?.type ?? '对象'}>`
  return Array.isArray(property.type) ? property.type.join(' / ') : property.type || '对象'
}

function purposeLabel(value: string) {
  return ({ base: '基础数据', query: '检索数据', analysis: '分析结果', graph_node: '图谱节点', graph_edge: '图谱关系' } as Record<string, string>)[value] ?? value
}
