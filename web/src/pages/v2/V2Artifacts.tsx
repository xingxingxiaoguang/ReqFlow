import { Card, Empty, Space, Table, Tag, Typography } from 'antd'
import { DownloadOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { v2CatalogApi } from '../../api/v2/catalog'
import type { V2Artifact } from '../../api/v2/types'

const { Link, Paragraph, Text, Title } = Typography

export default function V2Artifacts() {
  const query = useQuery({ queryKey: ['v2-artifacts'], queryFn: v2CatalogApi.listArtifacts })
  return <Card title={<div><Title level={4} style={{ margin: 0 }}>业务制品</Title><Paragraph type="secondary" style={{ margin: 0 }}>报告与图谱 Manifest 使用内容寻址存储，哈希与来源 Task/Step 可完整追溯。</Paragraph></div>}>
    <Table<V2Artifact> rowKey="id" loading={query.isLoading} pagination={false} dataSource={query.data?.artifacts ?? []} locale={{ emptyText: <Empty description="暂无 Artifact" /> }} columns={[
      { title: '制品', render: (_, item) => <Space direction="vertical" size={1}><Text strong>{item.name}</Text><Text type="secondary">{item.id}</Text></Space> },
      { title: '类型', dataIndex: 'kind', width: 140, render: (value) => <Tag color="purple">{value}</Tag> },
      { title: '内容哈希', dataIndex: 'content_hash', width: 190, render: (value: string) => <Text code>{value.slice(0, 16)}</Text> },
      { title: '来源任务', dataIndex: 'source_task_id', width: 190, render: (value: string) => <Link href={`/tasks/${value}`}>{value.slice(0, 12)}</Link> },
      { title: '创建时间', dataIndex: 'created_at', width: 180, render: (value: string) => new Date(value).toLocaleString('zh-CN') },
      { title: '操作', width: 100, render: (_, item) => <Link href={`/api/v2/artifacts/${item.id}/content`}><DownloadOutlined /> 下载</Link> },
    ]} />
  </Card>
}
