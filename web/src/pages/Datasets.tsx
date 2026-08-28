import { useMemo, useState } from 'react'
import {
  Card, Typography, Space, Tag, Row, Col, Statistic, Table, Segmented, Button, Input, Select,
  App, Popconfirm, Modal, Drawer, Alert,
} from 'antd'
import {
  DatabaseOutlined, FileSearchOutlined, SearchOutlined, InboxOutlined,
  PlusOutlined, EditOutlined, TagsOutlined,
} from '@ant-design/icons'
import { useNavigate } from 'react-router-dom'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api } from '../api/client'
import { tasksApi } from '../api/tasks'
import { parseDatasetItemFields, parseDatasetSchema } from '../api/types'
import type {
  CompatReport, CreateDatasetInput, DatasetSchema, DatasetType, FieldSpec, Overview, QueryHit,
} from '../api/types'

const { Text } = Typography

/** 数据集页：字段定义归属数据集——浏览（数据集自身 schema 驱动明细表格 + 筛选 + 双模搜索）、
 * 新建（类型模板带出字段）、字段定义受控编辑（兼容守卫 + 动态索引随 schema 同步）。 */
export default function Datasets() {
  const navigate = useNavigate()
  const qc = useQueryClient()
  const { message } = App.useApp()
  const [type, setType] = useState<DatasetType>()
  const [openId, setOpenId] = useState<string>()
  const [q, setQ] = useState('')
  const [searchMode, setSearchMode] = useState<'semantic' | 'fts'>('semantic')
  const [filters, setFilters] = useState<Record<string, string>>({})
  const [createOpen, setCreateOpen] = useState(false)
  const [schemaEditorOpen, setSchemaEditorOpen] = useState(false)

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
  const templates = schemaData?.schemas ?? []
  // 类型标签从模板目录泛化（M5 起类型级定义 = 数据集类型模板）
  const TYPE_MAP = useMemo(() => {
    const m: Record<string, string> = {}
    for (const t of templates) m[t.type] = t.label
    return m
  }, [templates])
  // 字段定义真相源在数据集行：详情优先用其自带 schema（实例可独立演进），缺省回退模板
  const schema: DatasetSchema | undefined = useMemo(
    () => parseDatasetSchema(detail?.dataset.Schema) ?? templates.find((t) => t.type === detail?.dataset.Type),
    [detail, templates],
  )
  const ftsFields = (schema?.fields ?? []).filter((f) => f.fts)

  // 筛选 + 语义/全文双模查询
  const queryParams = useMemo(() => {
    const clean: Record<string, string> = {}
    for (const [k, v] of Object.entries(filters)) if (v) clean[k] = v
    return { q: q.trim() || undefined, filters: clean, limit: 200 }
  }, [q, filters])
  const { data: hitData, isFetching: querying } = useQuery({
    queryKey: ['dataset-items', openId, searchMode, queryParams],
    queryFn: () => searchMode === 'fts'
      ? tasksApi.searchDatasetFTS(openId!, q.trim()).then((r) => ({ items: r.items as QueryHit[] }))
      : tasksApi.queryDatasetItems(openId!, queryParams),
    enabled: !!openId && !!schema && (searchMode === 'fts' ? !!q.trim() : true),
  })
  const hits: QueryHit[] = hitData?.items ?? detail?.items ?? []
  const filterable = schema?.fields.filter((f) => f.filterable) ?? []

  const onArchive = async (id: string, name: string) => {
    try {
      await tasksApi.archiveDataset(id)
      if (openId === id) { setOpenId(undefined); setFilters({}); setQ('') }
      qc.invalidateQueries({ queryKey: ['datasets'] })
      qc.invalidateQueries({ queryKey: ['archives'] })
      qc.invalidateQueries({ queryKey: ['overview'] })
      message.success(`「${name}」已归档（含全部条目，可在归档页恢复）`)
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const onCreated = () => {
    qc.invalidateQueries({ queryKey: ['datasets'] })
    qc.invalidateQueries({ queryKey: ['overview'] })
  }

  return (
    <Row gutter={[16, 16]}>
      <Col span={24}>
        <Card
          title={<Space><DatabaseOutlined style={{ color: '#4F46E5' }} /><Text strong>数据集</Text></Space>}
          extra={<Space>
            <Segmented
              options={[{ label: '全部', value: '' }, ...templates.map((t) => ({ label: t.label, value: t.type }))]}
              value={type ?? ''}
              onChange={(v) => setType((v === '' ? undefined : v) as DatasetType | undefined)}
            />
            <Button type="primary" icon={<PlusOutlined />} onClick={() => setCreateOpen(true)}>新建数据集</Button>
          </Space>}
        >
          <Row gutter={24}>
            <Col><Statistic title="数据集" value={overview?.datasets ?? 0} /></Col>
            <Col><Statistic title="条目总数" value={overview?.datasetItems ?? 0} /></Col>
          </Row>
          <Text type="secondary" style={{ display: 'block', marginTop: 12 }}>
            数据集是任务产出的结果集，也是字段定义的载体：创建任务时绑定数据集，字段元数据随之自动带出
            （AI 提取、草稿校验、写入、检索全程按数据集自身的字段定义执行）——同名任务可多次写入（补充/更新/覆盖）。
          </Text>
          <Table
            rowKey="ID" size="small" style={{ marginTop: 12 }}
            dataSource={data?.datasets ?? []}
            locale={{ emptyText: '还没有数据集：点击右上角「新建数据集」，或从「开始任务」创建' }}
            pagination={false}
            columns={[
              {
                title: '名称', dataIndex: 'Name', render: (v, r) => (
                  <Space size={8}>
                    <Text strong>{v}</Text>
                    <Tag color="purple">{TYPE_MAP[r.Type] ?? r.Type}</Tag>
                    {r.Tags?.map((t) => <Tag key={t} style={{ margin: 0 }}>{t}</Tag>)}
                  </Space>
                ),
              },
              { title: '条目', dataIndex: 'ItemCount', width: 90, align: 'center' },
              { title: '字段', width: 90, align: 'center',
                render: (_, r) => parseDatasetSchema(r.Schema)?.fields.length ?? <Text type="secondary">—</Text> },
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
            extra={<Space>
              {schema && (
                <Button size="small" icon={<EditOutlined />} onClick={() => setSchemaEditorOpen(true)}>
                  字段定义（{schema.fields.length}）v{detail.dataset.SchemaVersion}
                </Button>
              )}
              <Text type="secondary">{querying ? '查询中…' : `共 ${hits.length} 条`}</Text>
            </Space>}
          >
            {/* 双模搜索（语义/全文）+ schema 筛选（filterable 字段驱动） */}
            <Space wrap style={{ marginBottom: 12 }}>
              <Segmented
                size="small"
                value={searchMode}
                onChange={(v) => { setSearchMode(v as 'semantic' | 'fts'); setQ('') }}
                options={[
                  { value: 'semantic', label: '语义搜索' },
                  { value: 'fts', label: ftsFields.length ? `全文（${ftsFields.map((f) => f.label).join('/')}）` : '全文' },
                ]}
              />
              <Input
                allowClear
                prefix={<SearchOutlined />}
                placeholder={searchMode === 'semantic' ? '描述相近的条目' : '关键词（标题/描述全文检索）'}
                style={{ width: 280 }}
                value={q}
                disabled={searchMode === 'fts' && ftsFields.length === 0}
                onChange={(e) => setQ(e.target.value)}
              />
              {searchMode === 'semantic' && filterable.map((f) => (
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

      <CreateDatasetModal
        open={createOpen}
        templates={templates}
        onClose={() => setCreateOpen(false)}
        onCreated={(ds) => { onCreated(); message.success(`数据集「${ds.Name}」已创建（字段定义从模板带出）`) }}
      />
      {detail && schema && (
        <DatasetSchemaEditorDrawer
          open={schemaEditorOpen}
          datasetId={detail.dataset.ID}
          datasetName={detail.dataset.Name}
          schema={schema}
          version={detail.dataset.SchemaVersion}
          onClose={() => setSchemaEditorOpen(false)}
          onSaved={() => {
            qc.invalidateQueries({ queryKey: ['dataset', detail.dataset.ID] })
            qc.invalidateQueries({ queryKey: ['datasets'] })
          }}
        />
      )}
    </Row>
  )
}

/** 新建数据集：命名 + 选类型模板（字段定义从模板带出，创建后可在明细页受控调整） */
function CreateDatasetModal({ open, templates, onClose, onCreated }: {
  open: boolean
  templates: DatasetSchema[]
  onClose: () => void
  onCreated: (ds: { Name: string }) => void
}) {
  const { message } = App.useApp()
  const [name, setName] = useState('')
  const [description, setDescription] = useState('')
  const [type, setType] = useState<string>()
  const [saving, setSaving] = useState(false)
  const effType = type ?? templates[0]?.type
  const template = templates.find((t) => t.type === effType)

  const submit = async () => {
    if (!name.trim() || !effType) {
      message.warning('名称与字段模板必填')
      return
    }
    setSaving(true)
    try {
      const input: CreateDatasetInput = { name: name.trim(), type: effType, description: description.trim() || undefined }
      const { dataset } = await tasksApi.createDataset(input)
      onCreated(dataset)
      setName(''); setDescription(''); setType(undefined)
      onClose()
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <Modal
      title="新建数据集" open={open} onCancel={onClose} onOk={submit} confirmLoading={saving}
      okText="创建" cancelText="取消"
    >
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        <Input placeholder="数据集命名（如：订单中心需求集 v1）" value={name} maxLength={60}
          onChange={(e) => setName(e.target.value)} />
        <Select
          style={{ width: '100%' }} placeholder="字段模板（决定本数据集的字段定义）"
          value={effType} onChange={(v) => setType(v)}
          options={templates.map((t) => ({ value: t.type, label: `${t.label}（${t.fields.length} 字段）` }))}
        />
        <Input.TextArea placeholder="描述（可选）" value={description} rows={2} maxLength={200}
          onChange={(e) => setDescription(e.target.value)} />
        {template && (
          <div>
            <Text type="secondary" style={{ fontSize: 12 }}>将带出的字段：</Text>
            <div style={{ marginTop: 4 }}>
              {template.fields.map((f) => (
                <Tag key={f.key} style={{ marginBottom: 4 }}>
                  {f.label}{f.required && <Text type="danger"> *</Text>}
                </Tag>
              ))}
            </div>
          </div>
        )}
        <Alert type="info" showIcon style={{ fontSize: 12 }}
          message="创建后即可在「新建任务」时绑定本数据集；字段定义可随时受控调整（兼容守卫保护存量条目）。" />
      </Space>
    </Modal>
  )
}

/** 数据集字段定义受控编辑抽屉：字段增删改 → check dry-run → ⚠️ 确认 / ❌ 拦截 → 保存。
 * 保存后服务端同步重建 FTS/筛选动态索引（InVector 变更的存量条目在下次更新时自动重嵌）。 */
function DatasetSchemaEditorDrawer({ open, datasetId, datasetName, schema, version, onClose, onSaved }: {
  open: boolean
  datasetId: string
  datasetName: string
  schema: DatasetSchema
  version: number
  onClose: () => void
  onSaved: () => void
}) {
  const { message, modal } = App.useApp()
  const [fields, setFields] = useState<FieldSpec[]>(() => schema.fields.map((f) => ({ ...f, enum: f.enum ? [...f.enum] : undefined })))
  const [label, setLabel] = useState(schema.label)
  const [report, setReport] = useState<CompatReport | null>(null)
  const [editing, setEditing] = useState<{ open: boolean; index: number; draft: FieldSpec }>()
  const [saving, setSaving] = useState(false)
  const [checking, setChecking] = useState(false)

  const buildSchema = (): DatasetSchema => ({ type: schema.type, label, version, fields })

  const doCheck = async () => {
    setChecking(true)
    try {
      const { report: r } = await tasksApi.checkDatasetSchema(datasetId, buildSchema())
      setReport(r)
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setChecking(false)
    }
  }

  const doSave = async (confirmRisky: boolean) => {
    setSaving(true)
    try {
      const { report: r } = await tasksApi.updateDatasetSchema(datasetId, buildSchema(), {
        confirm_risky: confirmRisky, summary: `[数据集页] 更新字段定义（v${version} → v${version + 1}）`,
      })
      setReport(r)
      message.success('字段定义已保存（动态索引已同步）')
      onSaved()
      onClose()
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const submitSave = () => {
    if (!report) { message.warning('请先「检查兼容性」'); return }
    if (report.blocked) { message.error('存在不兼容变更，无法保存'); return }
    if (report.needs_confirm) {
      modal.confirm({
        title: '存在风险变更（⚠️）',
        content: '新增必填字段仅对后续写入生效；InVector 变更的存量条目将在下次更新时自动重嵌。确认保存？',
        okText: '确认保存',
        onOk: () => doSave(true),
      })
      return
    }
    void doSave(false)
  }

  const submitField = (f: FieldSpec) => {
    if (!f.key.trim() || !f.label.trim()) { message.warning('字段 key 与名称必填'); return }
    const idx = editing?.index ?? -1
    setFields((prev) => {
      const next = [...prev]
      if (idx >= 0) next[idx] = f
      else next.push(f)
      return next
    })
    setEditing(undefined)
    setReport(null)
  }

  return (
    <Drawer
      title={<Space><TagsOutlined /><Text strong>字段定义 · {datasetName}</Text><Text type="secondary" style={{ fontSize: 12 }}>v{version}</Text></Space>}
      open={open} onClose={onClose} width={640}
      extra={<Space>
        <Button onClick={doCheck} loading={checking}>检查兼容性</Button>
        <Button type="primary" onClick={submitSave} loading={saving}>保存</Button>
      </Space>}
    >
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        <Input value={label} onChange={(e) => { setLabel(e.target.value); setReport(null) }} addonBefore="名称" maxLength={60} />
        <Table
          rowKey="key" size="small" dataSource={fields} pagination={false}
          columns={[
            { title: '字段', dataIndex: 'label', render: (v, f) => <Space size={4}>{v}{f.required && <Tag color="red" style={{ margin: 0, fontSize: 10 }}>必填</Tag>}</Space> },
            { title: 'key', dataIndex: 'key', render: (v) => <Text code style={{ fontSize: 12 }}>{v}</Text> },
            { title: '类型', dataIndex: 'type', width: 80 },
            { title: '标记', width: 150, render: (_, f: FieldSpec) => (
              <Space size={2} wrap>
                {f.fts && <Tag style={{ margin: 0, fontSize: 10 }}>全文</Tag>}
                {f.filterable && <Tag style={{ margin: 0, fontSize: 10 }}>筛选</Tag>}
                {f.in_vector === 'title' && <Tag style={{ margin: 0, fontSize: 10 }}>向量标题</Tag>}
                {f.in_vector === 'body' && <Tag style={{ margin: 0, fontSize: 10 }}>向量正文</Tag>}
                {f.in_key && <Tag style={{ margin: 0, fontSize: 10 }}>主键</Tag>}
              </Space>
            ) },
            { title: '', width: 70, render: (_, f) => (
              <Space size={4}>
                <Button size="small" type="text" icon={<EditOutlined />}
                  onClick={() => setEditing({ open: true, index: fields.indexOf(f), draft: { ...f, enum: f.enum ? [...f.enum] : undefined } })} />
                <Button size="small" type="text" danger
                  onClick={() => { setFields((prev) => prev.filter((x) => x.key !== f.key)); setReport(null) }}>删</Button>
              </Space>
            ) },
          ]}
        />
        <Button block type="dashed" icon={<PlusOutlined />}
          onClick={() => setEditing({ open: true, index: -1, draft: { key: '', label: '', type: 'string' } })}>
          新增字段
        </Button>
        {report && report.findings.length > 0 && (
          <Alert
            type={report.blocked ? 'error' : report.needs_confirm ? 'warning' : 'success'}
            showIcon
            message={report.blocked ? '存在不兼容变更（已拦截）' : report.needs_confirm ? '存在风险变更（需确认）' : '兼容'}
            description={
              <div style={{ fontSize: 12 }}>
                {report.findings.map((f, i) => (
                  <div key={i}>{f.level === 'block' ? '❌' : f.level === 'warn' ? '⚠️' : '✅'} {f.field ? `[${f.field}] ` : ''}{f.message}</div>
                ))}
              </div>
            }
          />
        )}
      </Space>

      <Modal
        title={(editing && editing.index >= 0) ? '编辑字段' : '新增字段'}
        open={!!editing}
        onCancel={() => setEditing(undefined)}
        onOk={() => editing && submitField(editing.draft)}
        okText="确定" cancelText="取消"
      >
        {editing && (
          <Space direction="vertical" size={10} style={{ width: '100%' }}>
            <Input addonBefore="key" placeholder="snake_case（如 risk_level）" value={editing.draft.key}
              disabled={!!editing && editing.index >= 0}
              onChange={(e) => setEditing({ ...editing, draft: { ...editing.draft, key: e.target.value } })} />
            <Input addonBefore="名称" value={editing.draft.label} maxLength={60}
              onChange={(e) => setEditing({ ...editing, draft: { ...editing.draft, label: e.target.value } })} />
            <Select style={{ width: '100%' }} value={editing.draft.type}
              onChange={(v) => setEditing({ ...editing, draft: { ...editing.draft, type: v } })}
              options={[
                { value: 'string', label: '短文本 string' },
                { value: 'text', label: '长文本 text' },
                { value: 'number', label: '数字 number' },
                { value: 'enum', label: '枚举 enum' },
                { value: 'date', label: '日期 date' },
              ]} />
            {editing.draft.type === 'enum' && (
              <Input addonBefore="枚举值" placeholder="逗号分隔（High,Medium,Low）"
                value={(editing.draft.enum ?? []).join(',')}
                onChange={(e) => setEditing({ ...editing, draft: { ...editing.draft, enum: e.target.value.split(',').map((s) => s.trim()).filter(Boolean) } })} />
            )}
            <Space size={16} wrap>
              {([['required', '必填'], ['fts', '全文检索'], ['filterable', '可筛选'], ['in_key', '条目主键']] as const).map(([k, lbl]) => (
                <label key={k} style={{ fontSize: 13 }}>
                  <input type="checkbox" checked={!!editing.draft[k]}
                    onChange={(e) => setEditing({ ...editing, draft: { ...editing.draft, [k]: e.target.checked } })} /> {lbl}
                </label>
              ))}
              <label style={{ fontSize: 13 }}>
                <input type="checkbox" checked={editing.draft.in_vector === 'title'}
                  onChange={(e) => setEditing({ ...editing, draft: { ...editing.draft, in_vector: e.target.checked ? 'title' : 'none' } })} /> 向量标题
              </label>
            </Space>
            <div>
              <Text type="secondary" style={{ fontSize: 12 }}>提取说明（渲染进分析提示词，可空）</Text>
              <Input.TextArea rows={2} maxLength={2000} showCount
                placeholder="告诉 AI 该字段怎么提取"
                value={editing.draft.prompt ?? ''}
                onChange={(e) => setEditing({ ...editing, draft: { ...editing.draft, prompt: e.target.value } })} />
            </div>
          </Space>
        )}
      </Modal>
    </Drawer>
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
