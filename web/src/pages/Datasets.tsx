import { useMemo, useState } from 'react'
import { Card, Typography, Space, Tag, Row, Col, Statistic, Table, Segmented, Button, Input, Select, App, Popconfirm } from 'antd'
import { DatabaseOutlined, FileSearchOutlined, SearchOutlined, InboxOutlined } from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../api/client'
import { tasksApi } from '../api/tasks'
import { parseDatasetItemFields } from '../api/types'
import type { DatasetSchema, DatasetType, Overview, QueryHit } from '../api/types'

const { Text } = Typography

const TYPE_LABEL: Record<DatasetType, string> = { requirement: '需求' }

/** 数据集页：结果集浏览（schema 驱动明细表格 + 属性筛选 + 语义搜索） */
export default function Datasets() {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { message } = App.useApp()
  const [type, setType] = useState<DatasetType>()
  const [openId, setOpenId] = useState<string>()
  const [q, setQ] = useState('')
  const [filters, setFilters] = useState<Record<string, string>>({})

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
  const { data: schemaData } = useQuery({
    queryKey: ['dataset-schemas'],
    queryFn: () => tasksApi.schemas(),
  })
  const schema: DatasetSchema | undefined = useMemo(
    () => schemaData?.schemas.find((s) => s.type === detail?.dataset.Type),
    [schemaData, detail?.dataset.Type],
  )

  // 筛选 + 语义查询（q 与字段筛选可叠加）
  const queryParams = useMemo(() => {
    const clean: Record<string, string> = {}
    for (const [k, v] of Object.entries(filters)) if (v) clean[k] = v
    return { q: q.trim() || undefined, filters: clean, limit: 200 }
  }, [q, filters])
  const { data: hitData, isFetching: querying } = useQuery({
    queryKey: ['dataset-items', openId, queryParams],
    queryFn: () => tasksApi.queryDatasetItems(openId!, queryParams),
    enabled: !!openId && !!schema,
  })
  const hits: QueryHit[] = hitData?.items ?? detail?.items ?? []
  const filterable = schema?.fields.filter((f) => f.filterable) ?? []

  const onArchive = async (id: string, name: string) => {
    try {
      await tasksApi.archiveDataset(id)
      if (openId === id) { setOpenId(undefined); setFilters({}); setQ('') }
      qc.invalidateQueries({ queryKey: ['datasets'] })
      qc.invalidateQueries({ queryKey: ['archives'] })
      message.success(`「${name}」已归档（含全部条目，可在归档页恢复）`)
    } catch (e) {
      message.error((e as Error).message)
    }
  }

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
            任务与任务通过数据集衔接，构成「任务 + 数据」的业务闭环。同名任务可多次写入（补充/更新/覆盖）。
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
                    {r.Tags?.map((t) => <Tag key={t} style={{ margin: 0 }}>{t}</Tag>)}
                  </Space>
                ),
              },
              { title: '条目', dataIndex: 'ItemCount', width: 90, align: 'center' },
              {
                title: '来源任务', dataIndex: 'SourceTaskID', width: 200,
                render: (v) => v ? <Button type="link" size="small" onClick={() => navigate(`/tasks/${v}`)}>查看任务 <FileSearchOutlined /></Button> : <Text type="secondary">—</Text>,
              },
              { title: '更新时间', dataIndex: 'UpdatedAt', width: 160,
                render: (v) => v && !v.startsWith('0001') ? new Date(v).toLocaleString('zh-CN') : <Text type="secondary">—</Text> },
              {
                title: '操作', width: 150,
                render: (_, r) => (
                  <Space>
                    <Button size="small" onClick={() => { setOpenId(openId === r.ID ? undefined : r.ID); setFilters({}); setQ('') }}>{openId === r.ID ? '收起' : '明细'}</Button>
                    <Popconfirm
                      title="归档该数据集？"
                      description="数据集与全部条目（含向量）将移出主列表，不再参与查重/检索/统计；可在归档页恢复。"
                      okText="归档"
                      okButtonProps={{ danger: true }}
                      onConfirm={() => onArchive(r.ID, r.Name)}
                    >
                      <Button size="small" danger type="text" icon={<InboxOutlined />}>归档</Button>
                    </Popconfirm>
                  </Space>
                ),
              },
            ]}
          />
        </Card>
      </Col>

      {openId && detail && (
        <Col span={24}>
          <Card
            size="small"
            title={<Text strong>「{detail.dataset.Name}」条目明细</Text>}
            extra={<Text type="secondary">{querying ? '查询中…' : `共 ${hits.length} 条`}</Text>}
          >
            {/* 语义搜索 + schema 筛选（filterable 字段驱动） */}
            <Space wrap style={{ marginBottom: 12 }}>
              <Input
                allowClear
                prefix={<SearchOutlined />}
                placeholder="语义搜索（描述相近的条目）"
                style={{ width: 260 }}
                value={q}
                onChange={(e) => setQ(e.target.value)}
              />
              {filterable.map((f) => (
                f.type === 'enum' ? (
                  <Select
                    key={f.key}
                    allowClear
                    placeholder={`按${f.label}筛选`}
                    style={{ width: 150 }}
                    mode="multiple"
                    maxTagCount="responsive"
                    options={(f.enum ?? []).map((v) => ({ value: v, label: v }))}
                    value={filters[f.key]?.split('|').filter(Boolean)}
                    onChange={(vals) => setFilters((prev) => ({ ...prev, [f.key]: vals.join('|') }))}
                  />
                ) : (
                  <Input
                    key={f.key}
                    allowClear
                    placeholder={`按${f.label}筛选`}
                    style={{ width: 150 }}
                    value={filters[f.key] ?? ''}
                    onChange={(e) => setFilters((prev) => ({ ...prev, [f.key]: e.target.value }))}
                  />
                )
              ))}
            </Space>
            <SchemaItemTable schema={schema} hits={hits} />
          </Card>
        </Col>
      )}
    </Row>
  )
}

/** schema 驱动的条目表格：列由 schema 生成（text 长文本进展开行），语义命中带分数标记 */
function SchemaItemTable({ schema, hits }: { schema?: DatasetSchema; hits: QueryHit[] }) {
  const columns = useMemo(() => {
    const cols: any[] = (schema?.fields ?? [])
      .filter((f) => f.type !== 'text')
      .map((f) => ({
        title: f.label,
        width: f.key === 'title' ? undefined : 120,
        ellipsis: f.key === 'title',
        render: (_: unknown, it: QueryHit) => {
          const v = parseDatasetItemFields(it.Fields)[f.key]
          if (f.key === 'title') {
            return (
              <Space>
                <Text strong>{v}</Text>
                {it.MatchType === 'semantic' && it.Score != null && (
                  <Tag style={{ margin: 0 }} color="geekblue">{(it.Score * 100).toFixed(0)}%</Tag>
                )}
              </Space>
            )
          }
          return v ?? <Text type="secondary">—</Text>
        },
      }))
    return cols
  }, [schema])

  return (
    <Table
      rowKey="ID" size="small" dataSource={hits} pagination={false} columns={columns}
      expandable={{
        rowExpandable: (it) => (schema?.fields ?? []).some((f) => f.type === 'text' && parseDatasetItemFields(it.Fields)[f.key]),
        expandedRowRender: (it) => {
          const values = parseDatasetItemFields(it.Fields)
          return (
            <div style={{ padding: '4px 8px' }}>
              {(schema?.fields ?? []).filter((f) => f.type === 'text').map((f) => values[f.key] ? (
                <div key={f.key} style={{ marginBottom: 8 }}>
                  <Text type="secondary" strong style={{ fontSize: 12 }}>{f.label}</Text>
                  <div style={{ whiteSpace: 'pre-wrap', fontSize: 13, color: '#374151', marginTop: 2 }}>{values[f.key]}</div>
                </div>
              ) : null)}
            </div>
          )
        },
      }}
    />
  )
}
