import { useState } from 'react'
import { App, Button, Card, Col, Empty, Form, Input, InputNumber, Row, Segmented, Select, Slider, Space, Switch, Table, Tag, Typography } from 'antd'
import { SearchOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { v2CatalogApi } from '../../api/v2/catalog'

const { Paragraph, Text, Title } = Typography

export default function V2Retrieval() {
  const { message } = App.useApp(); const [form] = Form.useForm(); const [searching, setSearching] = useState(false); const [result, setResult] = useState<any>()
  const snapshots = useQuery({ queryKey: ['v2-retrieval-snapshots'], queryFn: v2CatalogApi.listRetrievalSnapshots })
  const profiles = useQuery({ queryKey: ['v2-retrieval-profiles'], queryFn: v2CatalogApi.listRetrievalProfiles })
  const search = async (values: any) => {
    setSearching(true)
    try {
      const response = await v2CatalogApi.search({ retrieval_snapshot_id: values.snapshot, query: values.query, filters: {}, strategy: { mode: values.mode, lexical_weight: values.lexical_weight, semantic_weight: 1 - values.lexical_weight, score_threshold: values.score_threshold, recall_limit: values.recall_limit, top_k: values.top_k, rerank_enabled: values.rerank_enabled, rerank_top_n: values.rerank_top_n } })
      setResult(response.search)
    } catch (error) { message.error((error as Error).message) } finally { setSearching(false) }
  }
  const active = (snapshots.data?.retrieval_snapshots ?? []).filter((item) => item.status === 'active')
  return <Space direction="vertical" size={16} style={{ width: '100%' }}>
    <Card title={<div><Title level={4} style={{ margin: 0 }}>混合检索工作台</Title><Paragraph type="secondary" style={{ margin: 0 }}>单次查询可调精准/语义权重、阈值、召回数和重排序 Top N；Profile 只负责不可变索引合同。</Paragraph></div>}>
      <Form form={form} layout="vertical" onFinish={search} initialValues={{ mode: 'hybrid', lexical_weight: 0.5, score_threshold: 0.3, recall_limit: 100, top_k: 20, rerank_enabled: true, rerank_top_n: 20 }}>
        <Row gutter={16}><Col xs={24} lg={16}><Form.Item name="snapshot" label="检索快照" rules={[{ required: true }]}><Select showSearch optionFilterProp="label" options={active.map((item) => ({ value: item.id, label: `${item.id.slice(0, 8)} · Dataset ${item.dataset_id.slice(0, 8)} · seq ${item.source_seq}` }))} /></Form.Item></Col><Col xs={24} lg={8}><Form.Item name="mode" label="搜索模式"><Segmented block options={[{ label: '精准', value: 'lexical' }, { label: '语义', value: 'semantic' }, { label: '混合', value: 'hybrid' }]} /></Form.Item></Col></Row>
        <Form.Item name="query" label="查询" rules={[{ required: true }]}><Input.Search enterButton={false} size="large" placeholder="输入业务问题或关键词" /></Form.Item>
        <Row gutter={24}><Col xs={24} md={12}><Form.Item name="lexical_weight" label="精准搜索权重（其余为语义权重）"><Slider min={0} max={1} step={0.05} marks={{ 0: '纯语义', 0.5: '均衡', 1: '纯精准' }} /></Form.Item></Col><Col xs={24} md={12}><Form.Item name="score_threshold" label="相关性阈值（重排序分）"><Slider min={0} max={1} step={0.05} /></Form.Item></Col></Row>
        <Row gutter={16}><Col span={6}><Form.Item name="recall_limit" label="召回数量"><InputNumber min={1} max={1000} style={{ width: '100%' }} /></Form.Item></Col><Col span={6}><Form.Item name="top_k" label="融合返回 Top K"><InputNumber min={1} max={200} style={{ width: '100%' }} /></Form.Item></Col><Col span={6}><Form.Item name="rerank_enabled" label="启用重排序" valuePropName="checked"><Switch /></Form.Item></Col><Col span={6}><Form.Item name="rerank_top_n" label="重排序返回 Top N"><InputNumber min={1} max={200} style={{ width: '100%' }} /></Form.Item></Col></Row>
        <Button type="primary" htmlType="submit" icon={<SearchOutlined />} loading={searching}>执行检索</Button>
      </Form>
    </Card>
    <Card title="检索结果" extra={result && <Space><Tag color="blue">{result.hits?.length ?? 0} 条</Tag><Text type="secondary">{result.took_ms} ms</Text></Space>}>
      <Table rowKey={(item) => `${item.dataset_item_id}-${item.chunk_id ?? ''}`} pagination={false} dataSource={result?.hits ?? []} locale={{ emptyText: <Empty description="选择快照并执行检索" /> }} columns={[
        { title: '#', width: 55, render: (_: unknown, __: unknown, index: number) => index + 1 }, { title: '分数', dataIndex: 'score', width: 100, render: (value: number) => value?.toFixed(4) },
        { title: '数据', render: (_: unknown, item: any) => <Space direction="vertical"><Text>{item.chunk_text || JSON.stringify(item.fields)}</Text><Text type="secondary">Item {item.dataset_item_id} · seq {item.commit_seq}</Text></Space> },
        { title: '排名证据', dataIndex: 'ranks', width: 220, render: (value: unknown) => <Text code>{JSON.stringify(value)}</Text> },
      ]} />
    </Card>
    <Card size="small" title="已配置检索 Profile"><Space wrap>{(profiles.data?.retrieval_profiles ?? []).map((item) => <Tag key={item.id} color="geekblue">{item.name}</Tag>)}</Space></Card>
  </Space>
}
