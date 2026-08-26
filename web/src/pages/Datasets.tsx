import { useState } from 'react'
import { Card, Typography, Space, Tag, Row, Col, Statistic, Table, Segmented, Button } from 'antd'
import { DatabaseOutlined, FileSearchOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../api/client'
import { tasksApi } from '../api/tasks'
import { parseDatasetItemFields } from '../api/types'
import type { Overview, DatasetType } from '../api/types'

const { Text } = Typography

const TYPE_LABEL: Record<DatasetType, string> = { requirement: '需求' }

/** 数据集页：任务产出的结果集浏览（需求集是后续任务如 Bug 分析的输入底料） */
export default function Datasets() {
  const navigate = useNavigate()
  const [type, setType] = useState<DatasetType>()
  const [openId, setOpenId] = useState<string>()

  const { data: overview } = useQuery({ queryKey: ['overview'], queryFn: () => api.get<Overview>('/api/overview') })
  const { data } = useQuery({
    queryKey: ['datasets', type],
    queryFn: () => tasksApi.listDatasets({ type, limit: 100 }),
  })
  const { data: detail } = useQuery({
    queryKey: ['dataset', openId],
    queryFn: () => tasksApi.getDataset(openId!),
    enabled: !!openId,
  })

  return (
    <Row gutter={[16, 16]}>
      <Col span={24}>
        <Card
          title={<Space><DatabaseOutlined style={{ color: '#4F46E5' }} /><Text strong>数据集</Text></Space>}
          extra={<Segmented
            options={[{ label: '全部', value: '' }, ...(Object.keys(TYPE_LABEL) as DatasetType[]).map((t) => ({ label: TYPE_LABEL[t], value: t }))]}
            value={type ?? ''}
            onChange={(v) => setType((v === '' ? undefined : v) as DatasetType | undefined)}
          />}
        >
          <Row gutter={24}>
            <Col><Statistic title="数据集" value={overview?.datasets ?? 0} /></Col>
            <Col><Statistic title="条目总数" value={overview?.datasetItems ?? 0} /></Col>
          </Row>
          <Text type="secondary" style={{ display: 'block', marginTop: 12 }}>
            数据集是任务产出的结果集：需求导入任务生成需求数据集，后续任务（如 Bug 分析）以它为输入——
            任务与任务通过数据集衔接，构成「任务 + 数据」的业务闭环。
          </Text>
          <Table
            rowKey="ID" size="small" style={{ marginTop: 12 }}
            dataSource={data?.datasets ?? []}
            locale={{ emptyText: '还没有数据集：从「开始任务 → 需求导入」生成第一个需求数据集' }}
            pagination={false}
            columns={[
              {
                title: '名称', dataIndex: 'Name', render: (v, r) => (
                  <Space size={8}>
                    <Text strong>{v}</Text>
                    <Tag color="purple">{TYPE_LABEL[r.Type] ?? r.Type}</Tag>
                  </Space>
                ),
              },
              { title: '条目', dataIndex: 'ItemCount', width: 90, align: 'center' },
              {
                title: '来源任务', dataIndex: 'SourceTaskID', width: 220,
                render: (v) => v ? <Button type="link" size="small" onClick={() => navigate(`/tasks/${v}`)}>查看任务 <FileSearchOutlined /></Button> : <Text type="secondary">—</Text>,
              },
              { title: '创建时间', dataIndex: 'CreatedAt', width: 160, render: (v) => new Date(v).toLocaleString('zh-CN') },
              {
                title: '操作', width: 100,
                render: (_, r) => <Button size="small" onClick={() => setOpenId(openId === r.ID ? undefined : r.ID)}>{openId === r.ID ? '收起' : '明细'}</Button>,
              },
            ]}
          />
        </Card>
      </Col>

      {detail && (
        <Col span={24}>
          <Card
            size="small"
            title={<Text strong>「{detail.dataset.Name}」条目明细</Text>}
            extra={<Text type="secondary">共 {detail.items.length} 条</Text>}
          >
            <Table
              rowKey="ID" size="small" dataSource={detail.items} pagination={false}
              columns={[
                { title: '标题', render: (_, it) => <Text strong>{parseDatasetItemFields(it.Fields).title}</Text> },
                { title: '分组', render: (_, it) => parseDatasetItemFields(it.Fields).project_name || '—' },
                { title: '类型', width: 90, render: (_, it) => parseDatasetItemFields(it.Fields).type_id },
                { title: '优先级', width: 100, render: (_, it) => parseDatasetItemFields(it.Fields).priority },
                { title: '工时(h)', width: 90, render: (_, it) => parseDatasetItemFields(it.Fields).estimated_hours || '—' },
                { title: '负责人', width: 110, render: (_, it) => parseDatasetItemFields(it.Fields).assignee_name || '—' },
              ]}
              expandable={{
                rowExpandable: (it) => !!parseDatasetItemFields(it.Fields).description,
                expandedRowRender: (it) => (
                  <div style={{ padding: '4px 8px', whiteSpace: 'pre-wrap', fontSize: 13, color: '#374151' }}>
                    {parseDatasetItemFields(it.Fields).description}
                  </div>
                ),
              }}
            />
          </Card>
        </Col>
      )}
    </Row>
  )
}
