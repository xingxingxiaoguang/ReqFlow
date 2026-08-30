import { useEffect, useMemo, useState } from 'react'
import {
  App, Button, Card, Col, Empty, Form, Input, InputNumber, Row, Segmented,
  Select, Slider, Space, Switch, Table, Tag, Typography,
} from 'antd'
import { ArrowLeftOutlined, DownOutlined, SearchOutlined, UpOutlined } from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import { useQuery } from '@tanstack/react-query'
import { useNavigate, useParams } from 'react-router-dom'
import { v2CatalogApi } from '../../api/v2/catalog'

const { Paragraph, Text, Title } = Typography

interface SearchForm {
  query: string
  snapshot: string
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
  const [form] = Form.useForm<SearchForm>()
  const [advanced, setAdvanced] = useState(false)
  const [searching, setSearching] = useState(false)
  const [result, setResult] = useState<SearchResult>()
  const datasets = useQuery({ queryKey: ['v2-datasets'], queryFn: () => v2CatalogApi.listDatasets({ status: 'active' }) })
  const schemas = useQuery({ queryKey: ['v2-schemas'], queryFn: v2CatalogApi.listSchemas })
  const snapshots = useQuery({ queryKey: ['v2-retrieval-snapshots'], queryFn: v2CatalogApi.listRetrievalSnapshots })
  const dataset = datasets.data?.datasets.find((item) => item.id === id)
  const schema = schemas.data?.schemas.find((item) => item.id === dataset?.schema_id)
  const activeSnapshots = useMemo(() => (snapshots.data?.retrieval_snapshots ?? [])
    .filter((item) => item.dataset_id === id && item.status === 'active')
    .sort((a, b) => b.source_seq - a.source_seq), [id, snapshots.data])
  const items = useQuery({
    queryKey: ['v2-dataset-items', dataset?.id, dataset?.current_seq],
    queryFn: () => v2CatalogApi.listDatasetItems(dataset!.id, 0, dataset!.current_seq, 200),
    enabled: Boolean(dataset && dataset.current_seq > 0),
  })

  useEffect(() => {
    if (!form.getFieldValue('snapshot') && activeSnapshots[0]) form.setFieldValue('snapshot', activeSnapshots[0].id)
  }, [activeSnapshots, form])

  const search = async (values: SearchForm) => {
    if (!values.snapshot) {
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
        retrieval_snapshot_id: values.snapshot,
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
        initialValues={{ mode: 'hybrid', lexical_weight: 0.5, score_threshold: 0, recall_limit: 100, top_k: 20, rerank_enabled: true, rerank_top_n: 10, filters: '' }}
      >
        <Form.Item name="query" rules={[{ required: true, whitespace: true, message: '请输入搜索内容' }]} style={{ marginBottom: advanced ? 18 : 0 }}>
          <Input.Search size="large" enterButton="搜索" loading={searching} placeholder="输入关键词或业务问题" onSearch={() => form.submit()} />
        </Form.Item>
        {advanced && <Card size="small" type="inner" title="混合检索参数">
          <Row gutter={16}>
            <Col xs={24} lg={14}><Form.Item name="snapshot" label="索引快照" rules={[{ required: true, message: '暂无可用索引快照' }]}><Select placeholder="选择索引快照" options={activeSnapshots.map((item) => ({ value: item.id, label: `数据边界 ${item.source_seq} · ${new Date(item.activated_at ?? item.created_at).toLocaleString('zh-CN')}` }))} /></Form.Item></Col>
            <Col xs={24} lg={10}><Form.Item name="mode" label="检索模式"><Segmented block options={[{ label: '精准', value: 'lexical' }, { label: '语义', value: 'semantic' }, { label: '混合', value: 'hybrid' }]} /></Form.Item></Col>
          </Row>
          <Row gutter={24}>
            <Col xs={24} md={12}><Form.Item name="lexical_weight" label="精准权重（其余为语义权重）"><Slider min={0} max={1} step={0.05} marks={{ 0: '纯语义', 0.5: '均衡', 1: '纯精准' }} /></Form.Item></Col>
            <Col xs={24} md={12}><Form.Item name="score_threshold" label="最低召回分数"><Slider min={0} max={1} step={0.05} /></Form.Item></Col>
          </Row>
          <Row gutter={16}>
            <Col xs={12} md={6}><Form.Item name="recall_limit" label="召回数"><InputNumber min={1} max={1000} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} md={6}><Form.Item name="top_k" label="融合 Top K"><InputNumber min={1} max={200} style={{ width: '100%' }} /></Form.Item></Col>
            <Col xs={12} md={6}><Form.Item name="rerank_enabled" label="重排序" valuePropName="checked"><Switch /></Form.Item></Col>
            <Col xs={12} md={6}><Form.Item name="rerank_top_n" label="重排 Top N"><InputNumber min={1} max={200} style={{ width: '100%' }} /></Form.Item></Col>
          </Row>
          <Form.Item name="filters" label="字段筛选（JSON，可选）"><Input.TextArea rows={2} placeholder={'例如：{"status":"active"}'} /></Form.Item>
        </Card>}
      </Form>
      {activeSnapshots.length === 0 && <Text type="warning">当前没有可用索引快照；可先浏览数据，索引由流程的“构建检索索引”步骤生成。</Text>}
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
  </Space>
}

function renderValue(value: unknown) {
  if (value === undefined || value === null || value === '') return <Text type="secondary">—</Text>
  if (typeof value === 'object') return <Text>{JSON.stringify(value)}</Text>
  return String(value)
}
