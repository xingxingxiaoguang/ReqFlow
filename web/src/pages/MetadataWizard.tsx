import { useEffect, useState } from 'react'
import {
  Alert, App, Button, Card, Col, Descriptions, Empty, Input, Row, Space, Tabs, Tag, Typography,
} from 'antd'
import type { CSSProperties } from 'react'
import { CaretRightOutlined, PlusOutlined, RollbackOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { metadataApi } from '../api/metadata'
import type {
  CompatFinding, FieldSpec, TaskTypeView, WorkflowStep,
} from '../api/types'
import { StepsEditor, levelMeta } from './MetadataEditors'

const { Text, Paragraph } = Typography

const preStyle: CSSProperties = {
  margin: 0, padding: 12, background: '#f9fafb', borderRadius: 8,
  maxHeight: 420, overflow: 'auto', fontSize: 12, whiteSpace: 'pre-wrap', wordBreak: 'break-word',
}

/** 字段行内小表（向导用的轻量字段模板编辑；完整编辑器在元数据页） */
function WizardFields({
  fields, onChange,
}: {
  fields: FieldSpec[]
  onChange: (next: FieldSpec[]) => void
}) {
  return (
    <Space direction="vertical" style={{ width: '100%' }} size={6}>
      {fields.map((f, i) => (
        <Card key={i} size="small" styles={{ body: { padding: '8px 12px' } }}>
          <Space wrap>
            <Input style={{ width: 150 }} prefix={<Text code>key</Text>} value={f.key}
              placeholder="snake_case"
              onChange={(e) => { const n = [...fields]; n[i] = { ...f, key: e.target.value }; onChange(n) }} />
            <Input style={{ width: 110 }} placeholder="名称" value={f.label}
              onChange={(e) => { const n = [...fields]; n[i] = { ...f, label: e.target.value }; onChange(n) }} />
            <Tag color={f.in_vector === 'title' ? 'geekblue' : f.in_vector === 'body' ? 'blue' : 'default'}>
              {f.type}{f.in_vector && f.in_vector !== 'none' ? ` · 向量${f.in_vector === 'title' ? '标题位' : '正文'}` : ''}
            </Tag>
            {f.required && <Tag color="red">必填</Tag>}
            {f.in_key && <Tag color="gold">主键</Tag>}
            {f.type === 'enum' && (f.enum ?? []).length > 0 && <Tag>{(f.enum ?? []).join(' / ')}</Tag>}
            <Button size="small" type="text" danger
              onClick={() => onChange(fields.filter((_, k) => k !== i))}>删除</Button>
          </Space>
          <Input size="small" style={{ marginTop: 6 }} placeholder="提取说明（渲染进分析提示词的字段规范段）"
            value={f.prompt ?? ''} onChange={(e) => { const n = [...fields]; n[i] = { ...f, prompt: e.target.value }; onChange(n) }} />
        </Card>
      ))}
      <Button icon={<PlusOutlined />} type="dashed"
        onClick={() => onChange([...fields, { key: '', label: '', type: 'string' }])}>
        添加字段（建议 2~5 个：一个标题位、一至两个分类/正文位）
      </Button>
    </Space>
  )
}

/** 新任务类型向导（M4）：步骤链编排 + schema 字段定义 + 指令头填写 + 绑定声明。
 *  产物先以 disabled 入库（草稿），人工验证后启用——红线见交接文档「元数据系统不变量」。 */
export default function MetadataWizard() {
  const { message } = App.useApp()
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const editType = searchParams.get('edit') // 传入时=编辑既有草稿

  /* 表单状态 */
  const [type, setType] = useState('')
  const [datasetType, setDatasetType] = useState('')
  const [wfName, setWfName] = useState('')
  const [wfDesc, setWfDesc] = useState('')
  const [steps, setSteps] = useState<WorkflowStep[]>([
    { seq: 1, name: '上传解析', kind: 'parse', deps: [] },
    { seq: 2, name: 'AI 分析', kind: 'analyze', deps: [] },
    { seq: 3, name: '生成数据集', kind: 'dataset', deps: [] },
  ])
  const [schemaLabel, setSchemaLabel] = useState('')
  const [fields, setFields] = useState<FieldSpec[]>([])
  const [role, setRole] = useState('你是专业的…助手。\n\n{field_spec}\n\n## 分析要点\n1. …')
  const [example, setExample] = useState('')
  const [submitting, setSubmitting] = useState(false)

  /** 提交结果（草稿 + 判定 + 即时预览） */
  const [result, setResult] = useState<{
    draft_view_hint: string
    report_findings: CompatFinding[]
    preview?: { agent_system_prompt: string; agent_first_message: string; classic_prompt: string }
    versions: Record<string, number | undefined>
  } | null>(null)

  // 编辑既有草稿：拉取草稿视图回填表单
  const { data: draftView } = useQuery({
    queryKey: ['metadata-draft-view', editType],
    queryFn: () => metadataApi.taskTypeWithDraft(editType!),
    enabled: !!editType,
  })
  useEffect(() => {
    if (!draftView || !draftView.draft) return
    const v: TaskTypeView = draftView
    setType(v.type)
    setDatasetType(v.dataset_type)
    setWfName(v.workflow.name)
    setWfDesc(v.workflow.desc)
    setSteps(v.workflow.steps.map((s) => ({ ...s, deps: s.deps?.map((d) => ({ ...d })) })))
    setSchemaLabel(v.schema.label)
    setFields(v.schema.fields.map((f) => ({ ...f, enum: f.enum ? [...f.enum] : undefined })))
    setRole(v.profile.role)
    setExample(v.profile.example)
  }, [draftView])

  const localValidate = (): string | null => {
    if (!/^[a-z][a-z0-9_]{0,62}$/.test(type)) return '任务类型标识须为小写字母开头的 snake_case'
    if (!/^[a-z][a-z0-9_]{0,62}$/.test(datasetType)) return '数据集类型须为小写字母开头的 snake_case'
    if (!schemaLabel.trim()) return '请填写字段模板名称'
    if (!fields.length) return '至少添加一个字段'
    if (!steps.some((s) => s.kind === 'analyze')) return '步骤链至少需要一个 AI 分析步骤'
    return null
  }

  const doSubmit = async () => {
    const err = localValidate()
    if (err) {
      message.warning(err)
      return
    }
    setSubmitting(true)
    try {
      const res = await metadataApi.registerTaskType({
        type,
        dataset_type: datasetType,
        workflow: { type, name: wfName || type, desc: wfDesc, steps },
        schema: { type: datasetType, label: schemaLabel, version: 0, fields },
        role,
        example,
      })
      setResult({
        draft_view_hint: `${res.type} v${res.versions.workflow ?? '-'}（工作流锚行草稿待启用）`,
        report_findings: res.report.findings ?? [],
        preview: res.preview,
        versions: res.versions ?? {},
      })
      message.success(`已提交为草稿：「${type}」。请在下方核对判定与提示词预览，然后到元数据页启用。`)
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setSubmitting(false)
    }
  }

  const findings = result?.report_findings ?? []
  return (
    <Row gutter={16}>
      <Col span={14}>
        <Card title={<Text strong>新任务类型向导</Text>}
          extra={<Button onClick={() => navigate('/metadata')}>返回目录</Button>}>
          {editType && (
            <Alert type="info" showIcon style={{ marginBottom: 12 }}
              message={`正在编辑草稿「${editType}」（重新提交即替换，版本续链；仍保持待启用状态）`} />
          )}
          <Descriptions bordered size="small" column={2} style={{ marginBottom: 16 }}>
            <Descriptions.Item label="类型标识">
              <Input placeholder="如 bug_import" value={type} disabled={!!editType}
                onChange={(e) => { setType(e.target.value); if (!editType) setDatasetType(e.target.value.replace(/_import$/, '') + '_items') }} />
            </Descriptions.Item>
            <Descriptions.Item label="产出数据集类型">
              <Input placeholder="如 review" value={datasetType} disabled={!!editType}
                onChange={(e) => setDatasetType(e.target.value)} />
            </Descriptions.Item>
          </Descriptions>
          <Paragraph type="secondary">
            数据集类型是任务间衔接的身份键（随本类型新建，不与既有类型共用）；两者确定后不可改。
          </Paragraph>

          <Typography.Title level={5}>① 步骤链编排</Typography.Title>
          <Paragraph type="secondary" style={{ marginBottom: 8 }}>
            只能编排既有执行器类型（封闭集合）：parse / human / analyze / dataset。
            需要新的执行能力仍是代码开发（新增 StepKind）。名称与描述进创建入口展示。
          </Paragraph>
          <Space direction="vertical" style={{ width: '100%', marginBottom: 12 }}>
            <Space.Compact style={{ width: '100%' }}>
              <Input style={{ width: '30%' }} placeholder="工作流名称（如：评审导入）" value={wfName}
                onChange={(e) => setWfName(e.target.value)} />
              <Input style={{ width: '70%' }} placeholder="一句话描述（进任务创建入口）" value={wfDesc}
                onChange={(e) => setWfDesc(e.target.value)} />
            </Space.Compact>
          </Space>
          <StepsEditor steps={steps} onChange={setSteps} />

          <Typography.Title level={5} style={{ marginTop: 24 }}>② 数据集字段模板</Typography.Title>
          <Space direction="vertical" style={{ width: '100%', marginBottom: 12 }}>
            <Input style={{ width: 320 }} placeholder="合同名称（如：评审记录）" value={schemaLabel}
              onChange={(e) => setSchemaLabel(e.target.value)} />
          </Space>
          <WizardFields fields={fields} onChange={setFields} />

          <Typography.Title level={5} style={{ marginTop: 24 }}>③ 指令头装配描述</Typography.Title>
          <Paragraph type="secondary" style={{ marginBottom: 8 }}>
            Role 中保留 <Text code>{'{field_spec}'}</Text> 占位符以注入字段规范段（删掉会告警）；
            单发示例可留空（运行时按 schema 自动生成骨架）。
          </Paragraph>
          <Input.TextArea rows={7} value={role} onChange={(e) => setRole(e.target.value)}
            style={{ marginBottom: 8 }} />
          <Input.TextArea rows={4} value={example} style={{ fontFamily: 'monospace', fontSize: 12 }}
            placeholder='单发示例（可选，JSON 数组片段，支持 {current_time} 占位）'
            onChange={(e) => setExample(e.target.value)} />

          <div style={{ marginTop: 20 }}>
            <Space>
              <Button type="primary" icon={<CaretRightOutlined />} loading={submitting} onClick={doSubmit}>
                提交（注册为草稿）
              </Button>
              <PopconfirmReset onReset={() => navigate('/metadata')} />
            </Space>
          </div>
        </Card>
      </Col>
      <Col span={10}>
        <Card title={<Text strong>提交结果 · 草稿验证</Text>}>
          {!result ? (
            <Empty description="提交后在右侧查看编排判定与即时提示词预览" />
          ) : (
            <>
              <Alert type="success" showIcon style={{ marginBottom: 12 }} message={`已入库：${result.draft_view_hint}`} />
              <Typography.Title level={5}>编排判定</Typography.Title>
              {findings.length === 0 ? (
                <Text type="secondary">无告警项</Text>
              ) : (
                findings.map((f, i) => (
                  <div key={i} style={{ marginBottom: 6 }}>
                    <Tag color={levelMeta[f.level]?.color}>{levelMeta[f.level]?.label}</Tag>
                    {f.field && <Text code>{f.field}</Text>} {f.message}
                  </div>
                ))
              )}
              <Typography.Title level={5} style={{ marginTop: 12 }}>提示词即时预览（人工验证依据）</Typography.Title>
              <Tabs
                items={[
                  { key: 'sys', label: 'agent 系统提示词', children: <pre style={preStyle}>{result.preview?.agent_system_prompt ?? '—'}</pre> },
                  { key: 'first', label: '首轮消息', children: <pre style={preStyle}>{result.preview?.agent_first_message ?? '—'}</pre> },
                  { key: 'classic', label: '单发 prompt', children: <pre style={preStyle}>{result.preview?.classic_prompt ?? '—'}</pre> },
                ]}
              />
              <Paragraph type="secondary" style={{ marginTop: 12 }}>
                核对无误后：返回「元数据」页 → 左栏「待启用草稿」→ 详情页点击「启用此任务类型」。
              </Paragraph>
            </>
          )}
        </Card>
      </Col>
    </Row>
  )
}

/** 返回目录（放弃当前填写） */
function PopconfirmReset({ onReset }: { onReset: () => void }) {
  return (
    <Button icon={<RollbackOutlined />} onClick={() => {
      if (confirm('返回目录？（未提交的填写将丢失）')) onReset()
    }}>返回</Button>
  )
}
