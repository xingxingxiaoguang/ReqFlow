import { useEffect, useMemo, useState } from 'react'
import {
  Alert, App, Button, Checkbox, Drawer, Empty, Input, List, Modal, Popconfirm,
  Select, Space, Spin, Table, Tag, Typography,
} from 'antd'
import { HistoryOutlined, PlusOutlined, RollbackOutlined, SaveOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { metadataApi } from '../api/metadata'
import type {
  AffectedDataset, CompatFinding, CompatLevel, CompatReport, DatasetSchema, FieldSpec,
  MetadataVersionView, TaskTypeView, StepDependency, Workflow, WorkflowStep,
} from '../api/types'

const { Text, Paragraph } = Typography

/** 元数据受控编辑器（M3）：字段合同/装配描述编辑 + 兼容守卫 + 版本历史 + 回退内置。
 *  写路径是显式管理动作——保存前自动 check（❌ 拦截 / ⚠️ 需勾选确认影响面）。 */

/* ---- 通用小件 ---- */

const levelMeta: Record<CompatLevel, { label: string; color: string }> = {
  ok: { label: '✅ 兼容', color: 'green' },
  warn: { label: '⚠️ 需确认', color: 'orange' },
  block: { label: '❌ 不兼容', color: 'red' },
}

export { levelMeta }
export function SourceBadge({ source }: { source: string }) {
  return (
    <Tag color={source === 'builtin' ? 'default' : 'purple'}>
      {source === 'builtin' ? '内置' : '已覆盖'}
    </Tag>
  )
}

/* ---- 兼容判定弹窗（check dry-run 与保存拦截共用） ---- */

function CheckResultModal({
  open, result, saving, onClose, onConfirmSave,
}: {
  open: boolean
  result: CompatReport & { datasets?: AffectedDataset[] } | null
  saving: boolean
  onClose: () => void
  onConfirmSave: (confirmed: boolean) => void
}) {
  const [confirmed, setConfirmed] = useState(false)
  useEffect(() => { if (open) setConfirmed(false) }, [open, result])
  if (!result) return null
  const canSave = !result.blocked && (!result.needs_confirm || confirmed)

  const cols = [
    { title: '数据集', dataIndex: 'name', render: (v: string) => <Text strong>{v}</Text> },
    { title: '条目数', dataIndex: 'item_count', width: 90 },
    { title: '钉定版本', dataIndex: 'schema_version', width: 90, render: (v: number) => `v${v}` },
    {
      title: '影响', dataIndex: 'needs_reembed', width: 140,
      render: (v: boolean) => v ? <Tag color="orange">语料需重嵌</Tag> : <Tag color="green">读侧兼容</Tag>,
    },
  ]
  return (
    <Modal
      title="兼容性检查"
      open={open}
      onCancel={onClose}
      width={720}
      footer={[
        <Button key="close" onClick={onClose}>关闭</Button>,
        <Button key="save" type="primary" danger={!result.blocked && result.needs_confirm}
          disabled={!canSave} loading={saving}
          onClick={() => onConfirmSave(confirmed)}>
          {result.blocked ? '不兼容项需修正' : result.needs_confirm ? '确认影响面并保存' : '保存'}
        </Button>,
      ]}
    >
      {result.blocked && (
        <Alert type="error" showIcon style={{ marginBottom: 12 }}
          message="存在不兼容变更（❌），保存被拦截"
          description="删除字段 / 改字段类型 / 改主键 / 枚举收窄会与存量数据断裂。请修正后重试；确需破坏性变更请走版本重建（新建数据集类型）。" />
      )}
      {!result.blocked && result.needs_confirm && (
        <Alert type="warning" showIcon style={{ marginBottom: 12 }}
          message="存在需确认的影响项（⚠️）——仅对新写入生效，存量条目不受保护" />
      )}
      {result.needs_reembed && !result.blocked && (
        <Alert type="info" showIcon style={{ marginBottom: 12 }}
          message="向量组装口径变化：受影响数据集重新生成（重跑任务写入）即可触发重嵌" />
      )}
      <List
        size="small"
        dataSource={result.findings}
        renderItem={(f: CompatFinding) => (
          <List.Item style={{ padding: '6px 0' }}>
            <Space align="start">
              <Tag color={levelMeta[f.level]?.color}>{levelMeta[f.level]?.label}</Tag>
              <div>
                {f.field && <Text code>{f.field}</Text>} {f.message}
              </div>
            </Space>
          </List.Item>
        )}
      />
      {!!result.datasets?.length && (
        <>
          <Typography.Title level={5} style={{ marginTop: 16 }}>影响面 · 存量数据集</Typography.Title>
          <Table<AffectedDataset> rowKey="id" size="small" pagination={false}
            dataSource={result.datasets} columns={cols} />
        </>
      )}
      {!result.blocked && result.needs_confirm && (
        <Checkbox style={{ marginTop: 16 }} checked={confirmed} onChange={(e) => setConfirmed(e.target.checked)}>
          我已了解上述影响面（仅新任务生效；存量数据集按各自钉定版本继续工作）
        </Checkbox>
      )}
    </Modal>
  )
}

/* ---- 字段编辑弹窗 ---- */

const emptyField = (): FieldSpec => ({ key: '', label: '', type: 'string', prompt: '' })

function FieldModal({
  open, field, isNew, onClose, onSubmit,
}: {
  open: boolean
  field: FieldSpec | null
  isNew: boolean
  onClose: () => void
  onSubmit: (f: FieldSpec) => void
}) {
  const [draft, setDraft] = useState<FieldSpec>(field ?? emptyField())
  useEffect(() => { if (open) setDraft(field ? { ...field } : emptyField()) }, [open, field])
  const set = <K extends keyof FieldSpec>(k: K, v: FieldSpec[K]) => setDraft((d) => ({ ...d, [k]: v }))

  return (
    <Modal
      title={isNew ? '添加字段' : `编辑字段 · ${field?.key}`}
      open={open} onCancel={onClose} onOk={() => onSubmit(draft)} okText="确定" destroyOnClose
    >
      <Space direction="vertical" style={{ width: '100%' }} size={12}>
        <Space.Compact style={{ width: '100%' }}>
          <Input style={{ width: '40%' }} placeholder="字段 key（snake_case）" disabled={!isNew}
            value={draft.key} onChange={(e) => set('key', e.target.value)} />
          <Input style={{ width: '30%' }} placeholder="名称" value={draft.label}
            onChange={(e) => set('label', e.target.value)} />
          <Select style={{ width: '30%' }} value={draft.type}
            onChange={(v) => set('type', v)}
            options={[
              { value: 'string', label: 'string · 短文本' },
              { value: 'text', label: 'text · 长文本' },
              { value: 'number', label: 'number · 数字' },
              { value: 'enum', label: 'enum · 枚举' },
              { value: 'date', label: 'date · 日期' },
            ]} />
        </Space.Compact>
        {draft.type === 'enum' && (
          <Select mode="tags" placeholder="枚举值域（回车添加；收窄值域会被守卫拦截）"
            value={draft.enum ?? []} onChange={(v) => set('enum', v)} open={false} />
        )}
        <Input placeholder='默认值（支持 {current_time} 占位 = 运行时刻）' value={draft.default ?? ''}
          onChange={(e) => set('default', e.target.value || undefined)} />
        <Input.TextArea rows={4} placeholder="提取说明（渲染进分析提示词的字段规范段；支持 {current_time}）"
          value={draft.prompt ?? ''} onChange={(e) => set('prompt', e.target.value)} />
        <Space wrap>
          <Checkbox checked={!!draft.required} onChange={(e) => set('required', e.target.checked)}>必填</Checkbox>
          <Checkbox checked={!!draft.filterable} onChange={(e) => set('filterable', e.target.checked)}>可筛</Checkbox>
          <Checkbox checked={!!draft.in_key} onChange={(e) => set('in_key', e.target.checked)}>主键 InKey</Checkbox>
          <Select size="small" style={{ width: 150 }} value={draft.in_vector ?? 'none'}
            onChange={(v) => set('in_vector', v)} placeholder="向量角色"
            options={[
              { value: 'none', label: '不进向量' },
              { value: 'title', label: '向量 · 标题位' },
              { value: 'body', label: '向量 · 正文' },
            ]} />
        </Space>
        {draft.clean && <Text type="secondary">清洗声明：{draft.clean}（代码资产，本页不可编辑）</Text>}
      </Space>
    </Modal>
  )
}

/* ---- 字段合同编辑器 ---- */

export function SchemaEditor({ view, onSaved }: { view: TaskTypeView; onSaved: () => void }) {
  const { message } = App.useApp()
  const [fields, setFields] = useState<FieldSpec[]>([])
  const [label, setLabel] = useState('')
  const [dirty, setDirty] = useState(false)
  const [editing, setEditing] = useState<{ open: boolean; isNew: boolean; index: number }>({ open: false, isNew: false, index: -1 })
  const [check, setCheck] = useState<(CompatReport & { datasets?: AffectedDataset[] }) | null>(null)
  const [saving, setSaving] = useState(false)
  const [historyOpen, setHistoryOpen] = useState(false)

  // 视图刷新（保存/回退后）同步编辑态
  useEffect(() => {
    setFields(view.schema.fields.map((f) => ({ ...f, enum: f.enum ? [...f.enum] : undefined })))
    setLabel(view.schema.label)
    setDirty(false)
  }, [view.schema])

  const buildSchema = (): DatasetSchema => ({
    type: view.dataset_type, label, version: view.schema.version, fields,
  })

  const submitField = (f: FieldSpec) => {
    if (!f.key.trim() || !f.label.trim()) {
      message.warning('字段 key 与名称必填')
      return
    }
    setFields((prev) => {
      const next = [...prev]
      if (editing.isNew) next.push(f)
      else next[editing.index] = f
      return next
    })
    setDirty(true)
    setEditing({ open: false, isNew: false, index: -1 })
  }

  const runCheck = async () => {
    try {
      const res = await metadataApi.checkSchema(view.dataset_type, { schema: buildSchema() })
      setCheck(res.report ? { ...res.report, datasets: res.datasets } : null)
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const doSave = async (confirmed: boolean) => {
    setSaving(true)
    try {
      const res = await metadataApi.updateSchema(view.dataset_type, {
        schema: buildSchema(), confirm_risky: confirmed,
      })
      if (res.saved) {
        message.success(`已保存：effective v${res.version}（新任务即生效；存量任务按各自快照执行）`)
        setCheck(null)
        onSaved()
      } else {
        // 守卫拦截：刷新判定明细（409 载荷）
        setCheck(res.report ? { ...res.report, datasets: res.datasets } : null)
        if (res.block_reason) message.warning(res.block_reason)
      }
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const doReset = async () => {
    try {
      const res = await metadataApi.resetSchema(view.dataset_type)
      if (res.saved) message.success('已回退到内置定义')
      else message.info('当前已是内置定义')
      onSaved()
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  return (
    <>
      <Space style={{ marginBottom: 12 }} wrap>
        <Input style={{ width: 200 }} prefix="名称" value={label}
          onChange={(e) => { setLabel(e.target.value); setDirty(true) }} />
        <Button icon={<PlusOutlined />}
          onClick={() => setEditing({ open: true, isNew: true, index: -1 })}>添加字段</Button>
        <Button type="primary" icon={<SaveOutlined />} loading={saving} onClick={runCheck}
          disabled={!dirty}>保存（自动检查）</Button>
        <Button icon={<HistoryOutlined />} onClick={() => setHistoryOpen(true)}>版本历史</Button>
        {!view.custom && view.schema_source === 'overridden' && (
          <Popconfirm title="回退到内置定义？（版本历史保留，可再查看）" onConfirm={doReset}>
            <Button icon={<RollbackOutlined />}>回退到内置</Button>
          </Popconfirm>
        )}
        <SourceBadge source={view.schema_source} />
        {dirty && <Text type="warning">有未保存修改</Text>}
      </Space>
      <Table<FieldSpec>
        rowKey="key" size="small" pagination={false} dataSource={fields}
        onRow={(_, i) => ({ onClick: () => setEditing({ open: true, isNew: false, index: i ?? -1 }), style: { cursor: 'pointer' } })}
        columns={[
          { title: '字段', dataIndex: 'key', width: 150, render: (k: string) => <Text code>{k}</Text> },
          { title: '名称', dataIndex: 'label', width: 110 },
          { title: '类型', dataIndex: 'type', width: 100 },
          {
            title: '属性', key: 'flags', width: 240, render: (_, f) => (
              <Space size={4} wrap>
                {f.required && <Tag color="red">必填</Tag>}
                {f.filterable && <Tag color="cyan">可筛</Tag>}
                {f.in_key && <Tag color="gold">主键</Tag>}
                {f.in_vector && f.in_vector !== 'none' && <Tag color="geekblue">向量·{f.in_vector}</Tag>}
                {f.default !== undefined && f.default !== '' && <Tag>默认 {String(f.default)}</Tag>}
              </Space>
            ),
          },
          {
            title: '提取说明（进提示词）', dataIndex: 'prompt', ellipsis: true,
            render: (v: string) => <Text type="secondary">{v || '—'}</Text>,
          },
        ]}
      />
      <FieldModal
        open={editing.open} isNew={editing.isNew}
        field={editing.index >= 0 ? fields[editing.index] : null}
        onClose={() => setEditing({ open: false, isNew: false, index: -1 })}
        onSubmit={submitField}
      />
      <CheckResultModal
        open={!!check} result={check} saving={saving}
        onClose={() => setCheck(null)}
        onConfirmSave={(confirmed) => void doSave(confirmed)}
      />
      <HistoryDrawer open={historyOpen} kind="dataset_schema" target={view.dataset_type}
        onClose={() => setHistoryOpen(false)} />
    </>
  )
}

/* ---- 装配描述编辑器 ---- */

export function ProfileEditor({ view, onSaved }: { view: TaskTypeView; onSaved: () => void }) {
  const { message, modal } = App.useApp()
  const [role, setRole] = useState('')
  const [example, setExample] = useState('')
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [historyOpen, setHistoryOpen] = useState(false)

  useEffect(() => {
    setRole(view.profile.role)
    setExample(view.profile.example)
    setDirty(false)
  }, [view.profile])

  const doSave = async () => {
    setSaving(true)
    try {
      const res = await metadataApi.updateProfile(view.type, { role, example })
      const warns = (res.findings ?? []).filter((f) => f.level === 'warn')
      if (warns.length) {
        modal.warning({
          title: '已保存，但有告警',
          content: (
            <ul style={{ paddingLeft: 18 }}>
              {warns.map((w, i) => <li key={i}>{w.message}</li>)}
            </ul>
          ),
        })
      } else {
        message.success('装配描述已保存（新任务的提示词即生效）')
      }
      setDirty(false)
      onSaved()
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const doReset = async () => {
    try {
      const res = await metadataApi.resetProfile(view.type)
      if (res.saved) message.success('已回退到内置定义')
      else message.info('当前已是内置定义')
      onSaved()
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  return (
    <>
      <Space style={{ marginBottom: 12 }} wrap>
        <Button type="primary" icon={<SaveOutlined />} loading={saving} onClick={doSave} disabled={!dirty}>
          保存
        </Button>
        <Button icon={<HistoryOutlined />} onClick={() => setHistoryOpen(true)}>版本历史</Button>
        {!view.custom && view.profile_source === 'overridden' && (
          <Popconfirm title="回退到内置定义？（版本历史保留）" onConfirm={doReset}>
            <Button icon={<RollbackOutlined />}>回退到内置</Button>
          </Popconfirm>
        )}
        <SourceBadge source={view.profile_source} />
        {dirty && <Text type="warning">有未保存修改</Text>}
      </Space>
      <Paragraph type="secondary" style={{ marginBottom: 8 }}>
        指令头 Role（<Text code>{'{field_spec}'}</Text> 占位由 schema 渲染替换；删掉该占位会使字段规范段不再注入——保存时会告警）
      </Paragraph>
      <Input.TextArea rows={12} value={role}
        onChange={(e) => { setRole(e.target.value); setDirty(true) }} />
      <Typography.Title level={5} style={{ marginTop: 16 }}>单发降级示例 Example</Typography.Title>
      <Input.TextArea rows={8} value={example} style={{ fontFamily: 'monospace', fontSize: 12 }}
        onChange={(e) => { setExample(e.target.value); setDirty(true) }} />
      <HistoryDrawer open={historyOpen} kind="analyze_profile" target={view.type}
        onClose={() => setHistoryOpen(false)} />
    </>
  )
}

/* ---- 版本历史 + diff ---- */

type DiffRow = { left?: string; right?: string; status: 'same' | 'del' | 'add' }

/** 朴素 LCS 行 diff（载荷为百行级 JSON，O(n·m) 足够） */
function diffLines(a: string[], b: string[]): DiffRow[] {
  const n = a.length, m = b.length
  const dp: number[][] = Array.from({ length: n + 1 }, () => new Array(m + 1).fill(0))
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      dp[i][j] = a[i] === b[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1])
    }
  }
  const rows: DiffRow[] = []
  let i = 0, j = 0
  while (i < n && j < m) {
    if (a[i] === b[j]) { rows.push({ left: a[i], right: b[j], status: 'same' }); i++; j++ }
    else if (dp[i + 1][j] >= dp[i][j + 1]) { rows.push({ left: a[i], status: 'del' }); i++ }
    else { rows.push({ right: b[j], status: 'add' }); j++ }
  }
  while (i < n) rows.push({ left: a[i++], status: 'del' })
  while (j < m) rows.push({ right: b[j++], status: 'add' })
  return rows
}

const diffLineBg: Record<DiffRow['status'], string> = { same: 'transparent', del: '#fde8e8', add: '#e8f5e8' }

function JsonDiff({ left, right }: { left: object; right: object }) {
  const rows = useMemo(
    () => diffLines(JSON.stringify(left, null, 2).split('\n'), JSON.stringify(right, null, 2).split('\n')),
    [left, right],
  )
  return (
    <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 8, fontSize: 12, fontFamily: 'monospace' }}>
      {[0, 1].map((col) => (
        <pre key={col} style={{ margin: 0, padding: 8, background: '#f9fafb', borderRadius: 6, overflow: 'auto', maxHeight: 480 }}>
          {rows.map((r, i) => (
            <div key={i} style={{
              background: col === 0 ? diffLineBg[r.status === 'add' ? 'same' : r.status] : diffLineBg[r.status === 'del' ? 'same' : r.status],
              color: r.status !== 'same' ? (r.status === 'del' ? '#c0392b' : '#1e8449') : undefined,
              whiteSpace: 'pre-wrap', wordBreak: 'break-all',
            }}>
              {(col === 0 ? r.left : r.right) ?? ''}
            </div>
          ))}
        </pre>
      ))}
    </div>
  )
}

export function HistoryDrawer({
  open, kind, target, onClose,
}: {
  open: boolean
  kind: 'dataset_schema' | 'analyze_profile' | 'workflow'
  target: string
  onClose: () => void
}) {
  const { data, isLoading } = useQuery({
    queryKey: ['metadata-history', kind, target],
    queryFn: () => metadataApi.history(kind, target),
    enabled: open,
  })
  const [picked, setPicked] = useState<number[]>([])
  useEffect(() => { setPicked([]) }, [kind, target, data])

  const kindLabel = kind === 'dataset_schema' ? '字段合同' : kind === 'workflow' ? '工作流' : '装配描述'
  const pickedVersions = (data ?? []).filter((v) => picked.includes(v.version))
  const twoPicked = pickedVersions.length === 2

  return (
    <Drawer title={`版本历史 · ${kindLabel} · ${target}`}
      open={open} onClose={onClose} width={760}>
      {isLoading ? <Spin /> : !data?.length ? <Empty description="无历史版本（尚未编辑过，当前为内置定义）" /> : (
        <>
          <Paragraph type="secondary">点选两个版本对比 diff；版本历史不删（数据集按钉定版本回溯定义）。</Paragraph>
          <List
            size="small"
            dataSource={data}
            renderItem={(v: MetadataVersionView) => (
              <List.Item
                style={{ cursor: 'pointer', padding: '8px 10px', borderRadius: 8,
                  background: picked.includes(v.version) ? '#eef2ff' : undefined }}
                onClick={() => setPicked((p) =>
                  p.includes(v.version) ? p.filter((x) => x !== v.version)
                    : [...p, v.version].slice(-2))}
              >
                <Space>
                  <Text strong>v{v.effective_version}</Text>
                  <Tag color={v.enabled ? 'green' : 'default'}>{v.enabled ? '生效' : '已禁用（回退内置）'}</Tag>
                  <Text type="secondary">{v.summary || '—'}</Text>
                </Space>
                <Text type="secondary" style={{ fontSize: 12 }}>
                  {new Date(v.created_at).toLocaleString()}
                </Text>
              </List.Item>
            )}
          />
          {twoPicked && (
            <>
              <Typography.Title level={5} style={{ marginTop: 16 }}>
                v{pickedVersions[0].effective_version} → v{pickedVersions[1].effective_version}
              </Typography.Title>
              <JsonDiff left={pickedVersions[0].payload} right={pickedVersions[1].payload} />
            </>
          )}
        </>
      )}
    </Drawer>
  )
}

/* ---- 工作流编辑器（M4：定义外置——步骤链编排，封闭集 kind） ---- */

const stepKindOptions = [
  { value: 'parse', label: 'parse · 文档解析' },
  { value: 'human', label: 'human · 人工确认门' },
  { value: 'analyze', label: 'analyze · AI 分析' },
  { value: 'dataset', label: 'dataset · 生成数据集' },
]

function StepModal({
  open, step, isNew, onClose, onSubmit,
}: {
  open: boolean
  step: WorkflowStep | null
  isNew: boolean
  onClose: () => void
  onSubmit: (s: WorkflowStep) => void
}) {
  const [draft, setDraft] = useState<WorkflowStep>(step ?? { seq: 0, name: '', kind: 'parse', deps: [] })
  useEffect(() => { if (open) setDraft(step ? { ...step, deps: step.deps?.map((d) => ({ ...d })) } : { seq: 0, name: '', kind: 'parse', deps: [] }) }, [open, step])
  const depText = (deps?: StepDependency[]) => (deps ?? []).map((d) => `${d.data || ''} ⇒ ${d.tool || ''}`)

  return (
    <Modal
      title={isNew ? '添加步骤' : `编辑步骤 · ${step?.name}`}
      open={open} onCancel={onClose}
      onOk={() => {
        if (!draft.name.trim()) return
        onSubmit({ ...draft, deps: (draft.deps ?? []).filter((d) => d.data || d.tool) })
      }} okText="确定" destroyOnClose
    >
      <Space direction="vertical" style={{ width: '100%' }} size={12}>
        <Space.Compact style={{ width: '100%' }}>
          <Input style={{ width: '45%' }} placeholder="步骤名（唯一）" value={draft.name}
            onChange={(e) => setDraft((d) => ({ ...d, name: e.target.value }))} />
          <Select style={{ width: '55%' }} value={draft.kind} options={stepKindOptions}
            onChange={(v) => setDraft((d) => ({ ...d, kind: v }))} />
        </Space.Compact>
        <div>
          <Paragraph type="secondary" style={{ marginBottom: 4 }}>
            依赖声明（展示用元数据；一行一条「数据依赖 ⇒ 工具依赖」）
          </Paragraph>
          <Select
            mode="tags" open={false} style={{ width: '100%' }}
            placeholder="例：parsed_text（上一步产物） ⇒ agent_loop(read_document / write_work_items)"
            value={depText(draft.deps)}
            onChange={(arr) => setDraft((d) => ({
              ...d,
              deps: arr.map((s) => {
                const [data, tool] = s.split('⇒')
                return { data: (data ?? '').trim(), tool: (tool ?? '').trim() }
              }),
            }))}
          />
        </div>
      </Space>
    </Modal>
  )
}

/** 步骤链编排器：向导只能编排既有 kind（封闭集合），新增 kind 执行器仍是代码开发 */
export function StepsEditor({
  steps, onChange,
}: {
  steps: WorkflowStep[]
  onChange: (next: WorkflowStep[]) => void
}) {
  const [editing, setEditing] = useState<{ open: boolean; index: number }>({ open: false, index: -1 })

  const renumber = (list: WorkflowStep[]) => list.map((s, i) => ({ ...s, seq: i + 1 }))
  const move = (i: number, dir: -1 | 1) => {
    const j = i + dir
    if (j < 0 || j >= steps.length) return
    const next = [...steps]
    ;[next[i], next[j]] = [next[j], next[i]]
    onChange(renumber(next))
  }
  const remove = (i: number) => onChange(renumber(steps.filter((_, k) => k !== i)))

  const kindColor: Record<string, string> = { parse: 'blue', human: 'orange', analyze: 'purple', dataset: 'green' }
  return (
    <>
      <Table<WorkflowStep>
        rowKey="seq" size="small" pagination={false} dataSource={steps}
        onRow={(_, i) => ({ onClick: () => setEditing({ open: true, index: i ?? -1 }), style: { cursor: 'pointer' } })}
        columns={[
          { title: '#', dataIndex: 'seq', width: 46 },
          { title: '步骤', dataIndex: 'name', width: 160, render: (v: string) => <Text strong>{v}</Text> },
          { title: '类型', dataIndex: 'kind', width: 120, render: (k: string) => <Tag color={kindColor[k] ?? 'default'}>{k}</Tag> },
          {
            title: '依赖声明', key: 'deps', ellipsis: true, render: (_, s) =>
              (s.deps?.length
                ? <Text type="secondary">{s.deps.map((d) => `${d.data || ''}${d.tool ? ` ⇒ ${d.tool}` : ''}`).join('；')}</Text>
                : <Text type="secondary">—</Text>),
          },
          {
            title: '', key: 'ops', width: 130,
            render: (_, _s) => null,
          },
        ]}
      />
      <Space style={{ marginTop: 8 }}>
        <Button icon={<PlusOutlined />} onClick={() => setEditing({ open: true, index: -1 })}>添加步骤</Button>
        {steps.map((s, i) => (
          <Space key={s.seq} size={0}>
            <Button size="small" type="text" disabled={i === 0}
              onClick={() => move(i, -1)}>↑</Button>
            <Button size="small" type="text" disabled={i === steps.length - 1}
              onClick={() => move(i, 1)}>↓</Button>
            <Button size="small" type="text" danger onClick={() => remove(i)}>删「{s.name}」</Button>
          </Space>
        ))}
      </Space>
      <StepModal
        open={editing.open} isNew={editing.index < 0}
        step={editing.index >= 0 && editing.index < steps.length ? steps[editing.index] : null}
        onClose={() => setEditing({ open: false, index: -1 })}
        onSubmit={(s) => {
          const next = [...steps]
          if (editing.index >= 0) next[editing.index] = { ...s, seq: next[editing.index].seq }
          else next.push(s)
          onChange(renumber(next))
          setEditing({ open: false, index: -1 })
        }}
      />
    </>
  )
}

/* ---- 工作流受控编辑器（元数据页「工作流」tab） ---- */

export function WorkflowEditor({ view, onSaved }: { view: TaskTypeView; onSaved: () => void }) {
  const { message } = App.useApp()
  const [steps, setSteps] = useState<WorkflowStep[]>([])
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [check, setCheck] = useState<(CompatReport & { datasets?: AffectedDataset[] }) | null>(null)
  const [historyOpen, setHistoryOpen] = useState(false)

  useEffect(() => {
    setSteps(view.workflow.steps.map((s) => ({ ...s, deps: s.deps?.map((d) => ({ ...d })) })))
    setDirty(false)
  }, [view.workflow])

  const buildWorkflow = (): Workflow => ({
    ...view.workflow, type: view.type, steps,
  })

  const doSave = async (confirmed: boolean) => {
    setSaving(true)
    try {
      const res = await metadataApi.updateWorkflow(view.type, { workflow: buildWorkflow(), confirm_risky: confirmed })
      if (res.saved) {
        message.success(`已保存 v${res.version}（新任务的步骤链即生效；存量任务按创建时快照执行展示）`)
        setCheck(null)
        setDirty(false)
        onSaved()
      } else {
        setCheck(res.report ?? null)
        if (res.block_reason) message.warning(res.block_reason)
      }
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const customHint = !!view.custom
  return (
    <>
      {customHint && (
        <Alert type="info" showIcon style={{ marginBottom: 12 }}
          message="向导注册类型：工作流为其运行时定义本体，无「内置基线」（停用类型走状态切换）。" />
      )}
      <Space style={{ marginBottom: 12 }} wrap>
        <Input style={{ width: 220 }} prefix="名称" value={view.workflow.name} disabled />
        <Button type="primary" icon={<SaveOutlined />} loading={saving}
          disabled={!dirty}
          onClick={async () => {
            try {
              const res = await metadataApi.checkWorkflow(view.type, { workflow: buildWorkflow() })
              setCheck(res.report ?? null)
            } catch (e) {
              message.error((e as Error).message)
            }
          }}>
          保存（自动检查）
        </Button>
        <Button icon={<HistoryOutlined />} onClick={() => setHistoryOpen(true)}>版本历史</Button>
        {!view.custom && view.workflow_source === 'overridden' && (
          <Popconfirm title="回退到内置工作流？（版本历史保留；存量任务不受影响）"
            onConfirm={async () => {
              try {
                const res = await metadataApi.resetWorkflow(view.type)
                if (res.saved) message.success('已回退到内置工作流')
                else message.info('当前已是内置定义')
                onSaved()
              } catch (e) {
                message.error((e as Error).message)
              }
            }}>
            <Button icon={<RollbackOutlined />}>回退到内置</Button>
          </Popconfirm>
        )}
        <SourceBadge source={view.workflow_source ?? view.source} />
        {dirty && <Text type="warning">有未保存修改（仅影响新任务）</Text>}
      </Space>
      <StepsEditor steps={steps} onChange={(n) => { setSteps(n); setDirty(true) }} />
      <CheckResultModal
        open={!!check} result={check} saving={saving}
        onClose={() => setCheck(null)}
        onConfirmSave={(confirmed) => void doSave(confirmed)}
      />
      <HistoryDrawer open={historyOpen} kind="workflow" target={view.type}
        onClose={() => setHistoryOpen(false)} />
    </>
  )
}
