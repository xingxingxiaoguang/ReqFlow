import { App, Button, Card, Empty, Space, Table, Tag, Typography } from 'antd'
import { UndoOutlined } from '@ant-design/icons'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { v2CatalogApi } from '../../api/v2/catalog'
import { datasetPurposeLabel } from './datasetPurpose'

const { Paragraph, Text, Title } = Typography

// v1：流程已固定为内置流程，流程与任务的归档/恢复不再开放，归档页仅保留数据集。
export default function V2Archives() {
  const { message } = App.useApp(); const client = useQueryClient()
  const query = useQuery({ queryKey: ['v2-archives'], queryFn: v2CatalogApi.listArchives })
  const restoreDataset = async (id: string) => { try { await v2CatalogApi.restoreDataset(id); message.success('数据集已恢复'); client.invalidateQueries({ queryKey: ['v2-archives'] }); client.invalidateQueries({ queryKey: ['v2-datasets'] }) } catch (error) { message.error((error as Error).message) } }
  return <Card title={<div><Title level={4} style={{ margin: 0 }}>归档管理</Title><Paragraph type="secondary" style={{ margin: 0 }}>归档只退出活跃目录，不搬移、改写或丢失执行审计和追加历史。</Paragraph></div>}>
    <Table
      rowKey="id"
      loading={query.isLoading}
      pagination={false}
      dataSource={query.data?.datasets ?? []}
      locale={{ emptyText: <Empty description="没有已归档数据集" /> }}
      columns={[
        { title: '数据集', render: (_: unknown, item: any) => <Space direction="vertical" size={0}><Text strong>{item.name}</Text>{item.description && <Text type="secondary">{item.description}</Text>}</Space> },
        { title: '用途', width: 170, render: (_: unknown, item: any) => <Tag color="geekblue">{datasetPurposeLabel(item.purpose)}</Tag> },
        { title: '条目', dataIndex: 'item_count', width: 90, align: 'right' as const },
        { title: '操作', width: 100, render: (_: unknown, item: any) => <Button icon={<UndoOutlined />} onClick={() => void restoreDataset(item.id)}>恢复</Button> },
      ]}
    />
  </Card>
}
