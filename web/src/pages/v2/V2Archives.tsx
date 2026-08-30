import { App, Button, Card, Empty, Space, Table, Tabs, Tag, Typography } from 'antd'
import { UndoOutlined } from '@ant-design/icons'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { v2CatalogApi } from '../../api/v2/catalog'

const { Paragraph, Text, Title } = Typography

export default function V2Archives() {
  const { message } = App.useApp(); const client = useQueryClient()
  const query = useQuery({ queryKey: ['v2-archives'], queryFn: v2CatalogApi.listArchives })
  const restoreTask = async (id: string) => { try { await v2CatalogApi.restoreTask(id); message.success('任务已恢复'); client.invalidateQueries({ queryKey: ['v2-archives'] }) } catch (error) { message.error((error as Error).message) } }
  const restoreDataset = async (id: string) => { try { await v2CatalogApi.restoreDataset(id); message.success('数据集已恢复'); client.invalidateQueries({ queryKey: ['v2-archives'] }); client.invalidateQueries({ queryKey: ['v2-datasets'] }) } catch (error) { message.error((error as Error).message) } }
  return <Card title={<div><Title level={4} style={{ margin: 0 }}>V2 归档</Title><Paragraph type="secondary" style={{ margin: 0 }}>归档只退出活跃目录，不搬移、改写或丢失执行审计和追加历史。</Paragraph></div>}>
    <Tabs items={[
      { key: 'tasks', label: `任务 (${query.data?.tasks?.length ?? 0})`, children: <Table rowKey="id" pagination={false} dataSource={query.data?.tasks ?? []} locale={{ emptyText: <Empty description="没有已归档任务" /> }} columns={[{ title: '任务', render: (_: unknown, item: any) => <Space direction="vertical"><Text strong>{item.title}</Text><Text type="secondary">{item.id}</Text></Space> }, { title: '终态', dataIndex: 'status', render: (value) => <Tag>{value}</Tag> }, { title: '操作', render: (_: unknown, item: any) => <Button icon={<UndoOutlined />} onClick={() => restoreTask(item.id)}>恢复</Button> }]} /> },
      { key: 'datasets', label: `数据集 (${query.data?.datasets?.length ?? 0})`, children: <Table rowKey="id" pagination={false} dataSource={query.data?.datasets ?? []} locale={{ emptyText: <Empty description="没有已归档数据集" /> }} columns={[{ title: '数据集', dataIndex: 'name' }, { title: '用途', dataIndex: 'purpose' }, { title: '条目', dataIndex: 'item_count' }, { title: '操作', render: (_: unknown, item: any) => <Button icon={<UndoOutlined />} onClick={() => restoreDataset(item.id)}>恢复</Button> }]} /> },
    ]} />
  </Card>
}
