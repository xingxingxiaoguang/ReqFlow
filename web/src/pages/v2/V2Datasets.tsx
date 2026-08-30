import { useMemo, useState } from 'react'
import { App, Button, Card, Descriptions, Drawer, Empty, Form, Input, Modal, Select, Space, Table, Tag, Typography } from 'antd'
import { DatabaseOutlined, InboxOutlined, PlusOutlined } from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { v2CatalogApi } from '../../api/v2/catalog'
import type { V2Dataset, V2DatasetBatch } from '../../api/v2/types'

const { Paragraph, Text, Title } = Typography

export default function V2Datasets() {
  const { message } = App.useApp(); const client = useQueryClient()
  const [createOpen, setCreateOpen] = useState(false); const [selected, setSelected] = useState<V2Dataset>()
  const [form] = Form.useForm()
  const datasets = useQuery({ queryKey: ['v2-datasets'], queryFn: () => v2CatalogApi.listDatasets({ status: 'active' }) })
  const schemas = useQuery({ queryKey: ['v2-schemas'], queryFn: v2CatalogApi.listSchemas })
  const batches = useQuery({ queryKey: ['v2-batches', selected?.id], queryFn: () => v2CatalogApi.listBatches(selected!.id), enabled: Boolean(selected) })
  const items = useQuery({ queryKey: ['v2-dataset-items', selected?.id, selected?.current_seq], queryFn: () => v2CatalogApi.listDatasetItems(selected!.id, 0, selected!.current_seq, 200), enabled: Boolean(selected && selected.current_seq > 0) })
  const schemaMap = useMemo(() => new Map((schemas.data?.schemas ?? []).map((item) => [item.id, item])), [schemas.data])
  const create = async (values: { name: string; description?: string; purpose: string; schema_id: string; key_fields: string }) => {
    try {
      await v2CatalogApi.createDataset({ ...values, key_fields: values.key_fields.split(',').map((item) => item.trim()).filter(Boolean) })
      message.success('V2 Dataset 已创建'); setCreateOpen(false); form.resetFields(); client.invalidateQueries({ queryKey: ['v2-datasets'] })
    } catch (error) { message.error((error as Error).message) }
  }
  const archive = async (item: V2Dataset) => {
    try { await v2CatalogApi.archiveDataset(item.id); message.success('数据集已归档'); client.invalidateQueries({ queryKey: ['v2-datasets'] }) } catch (error) { message.error((error as Error).message) }
  }
  const columns: ColumnsType<V2Dataset> = [
    { title: '数据集', render: (_, item) => <Space direction="vertical" size={1}><Text strong>{item.name}</Text><Text type="secondary">{item.id}</Text></Space> },
    { title: '用途', dataIndex: 'purpose', width: 130, render: (value) => <Tag color="geekblue">{value}</Tag> },
    { title: 'Schema', dataIndex: 'schema_id', width: 190, render: (value) => schemaMap.get(value)?.name ?? value.slice(0, 8) },
    { title: '数据量', dataIndex: 'item_count', width: 100, align: 'right' },
    { title: '边界序号', dataIndex: 'current_seq', width: 110, align: 'right' },
    { title: '操作', width: 180, render: (_, item) => <Space><Button size="small" onClick={() => setSelected(item)}>浏览</Button><Button size="small" danger icon={<InboxOutlined />} onClick={() => archive(item)}>归档</Button></Space> },
  ]
  const batchColumns: ColumnsType<V2DatasetBatch> = [
    { title: 'Batch', dataIndex: 'id', render: (value: string) => <Text code>{value.slice(0, 12)}</Text> }, { title: '状态', dataIndex: 'status', render: (value) => <Tag>{value}</Tag> },
    { title: '范围', render: (_, item) => `${item.from_seq} – ${item.to_seq}` }, { title: '条数', dataIndex: 'item_count' },
  ]
  return <>
    <Card title={<div><Title level={4} style={{ margin: 0 }}>V2 数据集</Title><Paragraph type="secondary" style={{ margin: 0 }}>不可变 Schema + 追加型 Batch；所有读取都可固定到 through_seq。</Paragraph></div>} extra={<Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>创建数据集</Button>}>
      <Table rowKey="id" loading={datasets.isLoading} dataSource={datasets.data?.datasets ?? []} columns={columns} pagination={false} locale={{ emptyText: <Empty description="暂无 V2 Dataset" /> }} />
    </Card>
    <Modal title="创建 V2 Dataset" open={createOpen} onCancel={() => setCreateOpen(false)} onOk={() => form.submit()} okText="创建">
      <Form form={form} layout="vertical" onFinish={create} initialValues={{ purpose: 'base' }}>
        <Form.Item name="name" label="名称" rules={[{ required: true }]}><Input /></Form.Item><Form.Item name="description" label="说明"><Input.TextArea /></Form.Item>
        <Form.Item name="purpose" label="用途" rules={[{ required: true }]}><Select options={['base', 'query', 'analysis', 'graph_node', 'graph_edge'].map((value) => ({ value, label: value }))} /></Form.Item>
        <Form.Item name="schema_id" label="不可变 Schema" rules={[{ required: true }]}><Select showSearch optionFilterProp="label" options={(schemas.data?.schemas ?? []).map((item) => ({ value: item.id, label: item.name }))} /></Form.Item>
        <Form.Item name="key_fields" label="业务主键字段（逗号分隔）" rules={[{ required: true }]}><Input placeholder="例如 id 或 source_id,type" /></Form.Item>
      </Form>
    </Modal>
    <Drawer title={<Space><DatabaseOutlined />{selected?.name}</Space>} width={900} open={Boolean(selected)} onClose={() => setSelected(undefined)}>
      {selected && <Space direction="vertical" size={16} style={{ width: '100%' }}>
        <Descriptions bordered size="small" column={2} items={[{ key: 'purpose', label: '用途', children: selected.purpose }, { key: 'boundary', label: '当前边界', children: selected.current_seq }, { key: 'count', label: '条目', children: selected.item_count }, { key: 'schema', label: 'Schema', children: schemaMap.get(selected.schema_id)?.name ?? selected.schema_id }]} />
        <Card size="small" title="提交批次"><Table rowKey="id" size="small" pagination={false} dataSource={batches.data?.batches ?? []} columns={batchColumns} /></Card>
        <Card size="small" title="数据条目（最多 200 条）"><Table rowKey="id" size="small" pagination={false} dataSource={items.data?.items ?? []} columns={[{ title: 'Seq', dataIndex: 'commit_seq', width: 80 }, { title: '字段', dataIndex: 'fields', render: (value) => <pre style={{ margin: 0, whiteSpace: 'pre-wrap' }}>{JSON.stringify(value, null, 2)}</pre> }]} /></Card>
      </Space>}
    </Drawer>
  </>
}
