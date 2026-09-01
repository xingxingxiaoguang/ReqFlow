import { useEffect, useMemo, useState } from 'react'
import {
  Alert, App, Button, Card, Col, Empty, Form, Input, InputNumber, Modal, Row, Segmented,
  Select, Slider, Space, Switch, Table, Tag, Typography,
} from 'antd'
import { ArrowLeftOutlined, DownOutlined, PlusOutlined, RocketOutlined, SearchOutlined, UpOutlined } from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from 'react-router-dom'
import { v2CatalogApi } from '../../api/v2/catalog'
import { v2TasksApi } from '../../api/v2/tasks'
import type { V2RetrievalProfile, V2TaskDefinition } from '../../api/v2/types'
import EmbeddedResourceCreate, { type EmbeddedResource } from './EmbeddedResourceCreate'

const { Paragraph, Text, Title } = Typography

interface SearchForm {
  query: string
  snapshot?: string
  mode: 'lexical' | 'semantic' | 'hybrid'
  lexical_weight: number
  score_threshold: number
  recall_limit: number
  top_k: number
  rerank_enabled: boolean
  rerank_top_n: number
  filters: string
}

interface SearchHit {
  dataset_item_id: string
  chunk_id?: string
  commit_seq: number
  score?: number
  chunk_text?: string
  fields?: Record<string, unknown>
  ranks?: unknown
}

interface SearchResult {
  hits?: SearchHit[]
  took_ms?: number
}

export default function V2DatasetDetail() {
  const { id } = useParams()
  const navigate = useNavigate()
  const { message } = App.useApp()
  const client = useQueryClient()
  const [form] = Form.useForm<SearchForm>()
  const rerankEnabled = Form.useWatch('rerank_enabled', form)
  const [advanced, setAdvanced] = useState(false)
  const [searching, setSearching] = useState(false)
  const [result, setResult] = useState<SearchResult>()
  const [indexOpen, setIndexOpen] = useState(false)
  const [profileCreateOpen, setProfileCreateOpen] = useState(false)
  const [selectedProfileID, setSelectedProfileID] = useState<string>()
  const [indexing, setIndexing] = useState(false)
  const [indexTaskID, setIndexTaskID] = useState<string>()
  const datasets = useQuery({ queryKey: ['v2-datasets'], queryFn: () => v2CatalogApi.listDatasets({ status: 'active' }) })
  const schemas = useQuery({ queryKey: ['v2-schemas'], queryFn: v2CatalogApi.listSchemas })
  const dataset = datasets.data?.datasets.find((item) => item.id === id)
  const schema = schemas.data?.schemas.find((item) => item.id === dataset?.schema_id)
  const snapshots = useQuery({
    queryKey: ['v2-retrieval-snapshots', id, 'active'],
    queryFn: () => v2CatalogApi.queryRetrievalSnapshots({ datasetId: id, status: 'active' }),
    enabled: Boolean(id),
  })
  const profiles = useQuery({
    queryKey: ['v2-retrieval-profiles', dataset?.schema_id],
    queryFn: () => v2CatalogApi.queryRetrievalProfiles({ datasetSchemaId: dataset!.schema_id }),
    enabled: Boolean(dataset?.schema_id),
  })
  const definitions = useQuery({
    queryKey: ['v2-definitions', 'active'],
    queryFn: () => v2CatalogApi.listDefinitions({ status: 'active', limit: 200 }),
    enabled: indexOpen,
  })
  const indexTask = useQuery({
    queryKey: ['v2-task', indexTaskID],
    queryFn: () => v2TasksApi.get(indexTaskID!),
    enabled: Boolean(indexTaskID),
    refetchInterval: (query) => {
      const status = query.state.data?.task.status
      return status && ['succeeded', 'failed'].includes(status) ? false : 1000
    },
  })
  const activeSnapshots = useMemo(() => [...(snapshots.data?.retrieval_snapshots ?? [])]
    .sort((a, b) => b.source_seq - a.source_seq), [snapshots.data])
  const items = useQuery({
    queryKey: ['v2-dataset-items', dataset?.id, dataset?.current_seq],
    queryFn: () => v2CatalogApi.listDatasetItems(dataset!.id, 0, dataset!.current_seq, 200),
    enabled: Boolean(dataset && dataset.current_seq > 0),
  })

  useEffect(() => {
    if (!dataset || snapshots.isLoading) return
    const selected = form.getFieldValue('snapshot')
    if (!activeSnapshots.some((item) => item.id === selected)) {
      form.setFieldValue('snapshot', activeSnapshots[0]?.id)
    }
  }, [activeSnapshots, dataset, form, snapshots.isLoading])

  useEffect(() => {
    const available = profiles.data?.retrieval_profiles ?? []
    if (!available.some((item) => item.id === selectedProfileID)) {
      setSelectedProfileID(available[0]?.id)
    }
  }, [profiles.data, selectedProfileID])

  useEffect(() => {
    const status = indexTask.data?.task.status
    if (status === 'succeeded') {
      setIndexTaskID(undefined)
      void snapshots.refetch()
      message.success('索引已建立，搜索已自动启用')
    } else if (status === 'failed') {
      setIndexTaskID(undefined)
      message.error(indexTask.data?.task.error_message || '索引任务执行失败')
    }
  }, [indexTask.data?.task.error_message, indexTask.data?.task.status, message, snapshots])

  const startIndexing = async () => {
    if (!dataset || !selectedProfileID) return
    setIndexing(true)
    try {
      const availableDefinitions = definitions.data?.task_definitions ?? []
      let match = findIndexDefinition(availableDefinitions, selectedProfileID)
      if (!match) {
        const created = await v2CatalogApi.createDefinition(indexDefinitionInput(dataset.name, selectedProfileID))
        match = { definition: created.definition, portName: 'dataset', resourceType: 'dataset_boundary' }
        await client.invalidateQueries({ queryKey: ['v2-definitions'] })
      }
      const created = await v2TasksApi.create({
        definition_id: match.definition.id,
        title: `为「${dataset.name}」建立检索索引`,
        bindings: [{ port_name: match.portName, resource_type: match.resourceType, resource_id: dataset.id }],
      })
      await v2TasksApi.start(created.task.id)
      setIndexTaskID(created.task.id)
      setIndexOpen(false)
      message.success('索引任务已启动，完成后会自动启用搜索')
    } catch (error) {
      message.error((error as Error).message)
    } finally {
      setIndexing(false)
    }
  }

  const profileCreated = (resource: EmbeddedResource) => {
    const profile = resource as V2RetrievalProfile
    setSelectedProfileID(profile.id)
  }

  const search = async (values: SearchForm) => {
    // snapshot 选择器藏在"展开搜索参数"里，未展开时字段未挂载、表单值可能为空：
    // 与界面承诺一致地回退到覆盖数据最多的最新快照。
    const snapshotID = values.snapshot ?? activeSnapshots[0]?.id
    if (!snapshotID) {
      message.warning('该数据集还没有可用索引快照，请先通过流程构建索引')
      return
    }
    let filters: Record<string, unknown> = {}
    try {
      filters = values.filters?.trim() ? JSON.parse(values.filters) : {}
    } catch {
      message.error('筛选条件必须是合法 JSON 对象')
      return
    }
    setSearching(true)
    try {
      const response = await v2CatalogApi.search({
        retrieval_snapshot_id: snapshotID,
        query: values.query,
        filters,
        strategy: {
          mode: values.mode,
          lexical_weight: values.lexical_weight,
          semantic_weight: 1 - values.lexical_weight,
          score_threshold: values.score_threshold,
          recall_limit: values.recall_limit,
          top_k: values.top_k,
          rerank_enabled: values.rerank_enabled,
          rerank_top_n: values.rerank_top_n,
        },
      })
      setResult(response.search as unknown as SearchResult)
    } catch (error) {
      message.error((error as Error).message)
    } finally {
      setSearching(false)
    }
  }

  if (datasets.isLoading || schemas.isLoading) return <Card loading />
  if (!dataset) return <Card><Empty description="数据集不存在或已归档"><Button onClick={() => navigate('/datasets')}>返回数据管理</Button></Empty></Card>

  const fieldEntries = Object.entries(schema?.json_schema.properties ?? {})
  const itemColumns: ColumnsType<{ id: string; commit_seq: number; fields: Record<string, unknown> }> = [
    { title: '序号', dataIndex: 'commit_seq', width: 76, fixed: 'left' },
    ...fieldEntries.map(([name, property]) => ({
      title: property.title || name,
      key: name,
      width: 180,
      render: (_: unknown, item: { fields: Record<string, unknown> }) => renderValue(item.fields[name]),
    })),
  ]

  return <Space direction="vertical" size={16} style={{ width: '100%' }}>
    <Card>
      <Space align="start">
        <Button type="text" icon={<ArrowLeftOutlined />} onClick={() => navigate('/datasets')} />
        <div>
          <Space wrap><Title level={3} style={{ margin: 0 }}>{dataset.name}</Title><Tag color="geekblue">{dataset.item_count} 条</Tag><Tag>{schema?.name ?? dataset.schema_id}</Tag></Space>
          <Paragraph type="secondary" style={{ marginBottom: 0 }}>{dataset.description || '浏览数据条目，或在同一页直接执行混合检索。'}</Paragraph>
        </div>
      </Space>
    </Card>

    <Card
      title={<Space><SearchOutlined /><span>搜索数据</span>{result && <Tag color="blue">{result.hits?.length ?? 0} 条 · {result.took_ms ?? 0} ms</Tag>}</Space>}
      extra={<Button type="link" icon={advanced ? <UpOutlined /> : <DownOutlined />} onClick={() => setAdvanced((value) => !value)}>{advanced ? '收起搜索参数' : '展开搜索参数'}</Button>}
    >
      <Form<SearchForm>
        form={form}
        layout="vertical"
        onFinish={search}
        initialValues={{ mode: 'hybrid', lexical_weight: 0.5, score_threshold: 0.3, recall_limit: 100, top_k: 20, rerank_enabled: true, rerank_top_n: 20, filters: '' }}
      >
        <Form.Item name="query" rules={[{ required: true, whitespace: true, message: '请输入搜索内容' }]} style={{ marginBottom: advanced ? 18 : 0 }}>
          <Input.Search size="large" enterButton="搜索" loading={searching} disabled={!snapshots.isLoading && activeSnapshots.length === 0} placeholder={activeSnapshots.length > 0 ? '输入关键词或业务问题' : '请先为当前数据集建立索引'} onSearch={() => form.submit()} />
        </Form.Item>
        {/* 高级参数用 CSS 隐藏而不是条件渲染：条件渲染时未展开的字段不随表单提交，
            后端拿自己的默认值兜底，界面显示的阈值/重排序等参数形同虚设。 */}
        <Card size="small" type="inner" title="混合检索参数" style={{ display: advanced ? undefined : 'none' }}>
          <Row gutter={16}>
            <Col xs={24} lg={14}><Form.Item name="snapshot" label="索引版本" extra="默认自动使用覆盖数据最多的最新版本"><Select loading={snapshots.isLoading} disabled={activeSnapshots.length === 0} placeholder="暂无可用索引" options={activeSnapshots.map((item) => ({ value: item.id, label: `数据边界 ${item.source_seq} · ${new Date(item.activated_at ?? item.created_at).toLocaleString('zh-CN')}` }))} /></Form.Item></Col>
            <Col xs={24} lg={10}><Form.Item name="mode" label="检索模式"><Segmented block options={[{ label: '精准', value: 'lexical' }, { label: '语义', value: 'semantic' }, { label: '混合', value: 'hybrid' }]} /></Form.Item></Col>
          </Row>
          <Row gutter={24}>
            <Col xs={24} md={12}><Form.Item name="lexical_weight" label="精准权重（其余为语义权重）"><Slider min={0} max={1} step={0.05} marks={{ 0: '纯语义', 0.5: '均衡', 1: '纯精准' }} /></Form.Item></Col>
            <Col xs={24} md={12}><Form.Item name="score_threshold" label="相关性阈值（重排序分）" extra="过滤重排序后的最终分数（校准的 0..1 相关性）；关闭重排序时阈值不生效"><Slider min={0} max={1} step={0.05} disabled={!rerankEnabled} /></Form.Item></Col>
          </Row>
          <Row gutter={16}>
            <Col xs={12} md={6}><Form.Item name="recall_limit" label="召回数"><InputNumber min={1} max={1000} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} md={6}><Form.Item name="top_k" label="融合 Top K"><InputNumber min={1} max={200} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} md={6}><Form.Item name="rerank_enabled" label="重排序" valuePropName="checked"><Switch /></Form.Item></Col>
            <Col xs={12} md={6}><Form.Item name="rerank_top_n" label="重排 Top N"><InputNumber min={1} max={200} style={{ width: '100%' }} /></Form.Item></Col>
          </Row>
          <Form.Item name="filters" label="字段筛选（JSON，可选）"><Input.TextArea rows={2} placeholder={'例如：{"status":"active"}'} /></Form.Item>
        </Card>
      </Form>
      {!snapshots.isLoading && activeSnapshots.length === 0 && <Alert
        type="warning"
        showIcon
        message={indexTaskID ? '正在建立检索索引' : '当前数据集还不能执行混合检索'}
        description={indexTaskID ? '索引任务完成后，搜索框会自动启用。' : '选择或创建适用于当前数据结构的索引规则，系统会通过标准流程任务建立首个可用索引。'}
        action={indexTaskID
          ? <Button onClick={() => navigate(`/tasks/${indexTaskID}`)}>查看任务</Button>
          : <Button type="primary" icon={<RocketOutlined />} onClick={() => setIndexOpen(true)}>立即建立索引</Button>}
      />}
    </Card>

    <Card
      title={result ? '搜索结果' : '数据条目'}
      extra={result && <Button onClick={() => { setResult(undefined); form.setFieldValue('query', '') }}>返回全部条目</Button>}
    >
      {result ? <Table<SearchHit>
        rowKey={(item) => `${item.dataset_item_id}-${item.chunk_id ?? ''}`}
        loading={searching}
        pagination={{ pageSize: 20 }}
        dataSource={result.hits ?? []}
        locale={{ emptyText: <Empty description="没有匹配结果" /> }}
        columns={[
          { title: '#', width: 55, render: (_, __, index) => index + 1 },
          { title: '分数', dataIndex: 'score', width: 90, render: (value?: number) => value?.toFixed(4) ?? '—' },
          { title: '匹配内容', render: (_, item) => <Space direction="vertical" size={2}><Text>{item.chunk_text || renderValue(item.fields)}</Text><Text type="secondary">Item {item.dataset_item_id} · seq {item.commit_seq}</Text></Space> },
          { title: '排名证据', dataIndex: 'ranks', width: 190, render: renderValue },
        ]}
      /> : <Table
        rowKey="id"
        loading={items.isLoading}
        dataSource={items.data?.items ?? []}
        columns={itemColumns}
        scroll={{ x: Math.max(800, fieldEntries.length * 180) }}
        pagination={{ pageSize: 20 }}
        locale={{ emptyText: <Empty description="数据集暂时没有条目" /> }}
      />}
    </Card>

    <Modal
      title="为当前数据集建立索引"
      open={indexOpen}
      onCancel={() => !indexing && setIndexOpen(false)}
      onOk={() => void startIndexing()}
      okText="创建并运行索引任务"
      confirmLoading={indexing}
      okButtonProps={{ disabled: !selectedProfileID || definitions.isLoading }}
      destroyOnHidden
    >
      <Alert type="info" showIcon style={{ marginBottom: 16 }} message="索引会通过标准 retrieval.build 流程任务生成，不会绕过流程直接写入数据。" />
      <Space.Compact style={{ width: '100%' }}>
        <Select
          value={selectedProfileID}
          onChange={setSelectedProfileID}
          loading={profiles.isLoading}
          showSearch
          optionFilterProp="label"
          placeholder="选择适用于当前数据结构的索引规则"
          options={(profiles.data?.retrieval_profiles ?? []).map((item) => ({ value: item.id, label: item.name }))}
          style={{ width: 'calc(100% - 112px)' }}
        />
        <Button icon={<PlusOutlined />} onClick={() => setProfileCreateOpen(true)}>新建规则</Button>
      </Space.Compact>
      {!profiles.isLoading && profiles.data?.retrieval_profiles.length === 0 && <Text type="secondary">当前数据结构还没有索引规则，请直接新建。</Text>}
    </Modal>

    <EmbeddedResourceCreate
      kind={profileCreateOpen ? 'retrieval' : undefined}
      fixedSchemaId={dataset.schema_id}
      onCancel={() => setProfileCreateOpen(false)}
      onCreated={profileCreated}
    />
  </Space>
}

function findIndexDefinition(definitions: V2TaskDefinition[], profileID: string) {
  for (const definition of definitions) {
    const step = definition.steps.find((item) => item.kind === 'retrieval.build' && item.config?.retrieval_profile_id === profileID)
    if (!step) continue
    const input = Object.entries(definition.input_ports).find(([portName, port]) =>
      (port.resource_type === 'dataset' || port.resource_type === 'dataset_boundary')
      && Object.values(step.inputs ?? {}).includes(`$task.${portName}`))
    if (input) return { definition, portName: input[0], resourceType: input[1].resource_type }
  }
  return undefined
}

function indexDefinitionInput(datasetName: string, profileID: string) {
  return {
    key: `index_dataset_${Date.now().toString(36)}`,
    name: `为「${datasetName}」建立检索索引`,
    description: '由数据管理按需创建的单步骤索引流程',
    status: 'active' as const,
    input_ports: { dataset: { resource_type: 'dataset_boundary', required: true, description: '待建立索引的数据集边界' } },
    output_ports: { snapshot: { resource_type: 'retrieval_snapshot', description: '激活后的检索快照' } },
    output_bindings: { snapshot: '$step.build_index.snapshot' },
    steps: [{
      id: 'build_index', name: '建立检索索引', kind: 'retrieval.build',
      inputs: { dataset: '$task.dataset' }, outputs: { snapshot: 'retrieval_snapshot' },
      config: { retrieval_profile_id: profileID },
    }],
  }
}

function renderValue(value: unknown) {
  if (value === undefined || value === null || value === '') return <Text type="secondary">—</Text>
  if (typeof value === 'object') return <Text>{JSON.stringify(value)}</Text>
  return String(value)
}
