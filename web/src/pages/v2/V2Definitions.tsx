import { useState } from 'react'
import { Button, Card, Drawer, Empty, Space, Table, Tag, Typography } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { v2CatalogApi } from '../../api/v2/catalog'
import type { V2TaskDefinition } from '../../api/v2/types'
import { STEP_KIND_LABEL } from './status'

const { Paragraph, Text, Title } = Typography

export default function V2Definitions() {
  const navigate = useNavigate()
  const [selected, setSelected] = useState<V2TaskDefinition>()
  const query = useQuery({ queryKey: ['v2-definitions'], queryFn: () => v2CatalogApi.listDefinitions({ limit: 200 }) })
  const columns: ColumnsType<V2TaskDefinition> = [
    { title: '流程定义', render: (_, item) => <Space direction="vertical" size={1}><Text strong>{item.name}</Text><Text type="secondary">{item.key}</Text></Space> },
    { title: '状态', dataIndex: 'status', width: 100, render: (value) => <Tag color={value === 'active' ? 'success' : value === 'draft' ? 'gold' : 'default'}>{value}</Tag> },
    { title: '步骤', width: 90, align: 'center', render: (_, item) => item.steps.length },
    { title: '版本哈希', dataIndex: 'definition_hash', width: 180, render: (value: string) => <Text code>{value.slice(0, 12)}</Text> },
    { title: '创建时间', dataIndex: 'created_at', width: 180, render: (value: string) => new Date(value).toLocaleString('zh-CN') },
    { title: '操作', width: 90, render: (_, item) => <Button size="small" onClick={() => setSelected(item)}>查看</Button> },
  ]
  return <>
    <Card title={<div><Title level={4} style={{ margin: 0 }}>无代码流程定义</Title><Paragraph type="secondary" style={{ margin: 0 }}>每次任务创建都引用不可变定义；运行中的拓扑不受后续配置影响。</Paragraph></div>} extra={<Button type="primary" icon={<PlusOutlined />} onClick={() => navigate('/tasks/new')}>从模板创建任务</Button>}>
      <Table rowKey="id" loading={query.isLoading} dataSource={query.data?.task_definitions ?? []} columns={columns} pagination={false} locale={{ emptyText: <Empty description="暂无 V2 流程定义" /> }} />
    </Card>
    <Drawer title={selected?.name} width={720} open={Boolean(selected)} onClose={() => setSelected(undefined)}>
      {selected && <Space direction="vertical" size={16} style={{ width: '100%' }}>
        <Space><Tag color="geekblue">{selected.key}</Tag><Tag>{selected.definition_hash}</Tag></Space>
        {selected.steps.map((step, index) => <Card key={step.id} size="small" title={`${index + 1}. ${step.name}`} extra={<Tag color="blue">{STEP_KIND_LABEL[step.kind] ?? step.kind}</Tag>}><Text type="secondary">输入：{Object.entries(step.inputs ?? {}).map(([name, ref]) => `${name} ← ${ref}`).join('，') || '无'}</Text></Card>)}
        <pre style={{ whiteSpace: 'pre-wrap', background: '#0f172a', color: '#cbd5e1', padding: 16, borderRadius: 8 }}>{JSON.stringify(selected, null, 2)}</pre>
      </Space>}
    </Drawer>
  </>
}
