import { useMemo, useState } from 'react'
import {
  Alert, App, Button, Card, Col, Descriptions, Drawer, Input, InputNumber, Row,
  Select, Space, Statistic, Table, Tag, Typography,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { CheckOutlined, EditOutlined, SafetyCertificateOutlined } from '@ant-design/icons'
import type {
  JSONSchemaProperty, ReviewAction, ReviewDecisionInput, V2Dataset, V2Schema,
  ValidationResult, ValidationResultSet,
} from '../../api/v2/types'
import { ValidationStatusTag } from './status'

const { Text, Paragraph, Title } = Typography

interface LocalDecision {
  action: ReviewAction
  fields?: Record<string, unknown>
  note: string
}

interface ReviewWorkspaceProps {
  validation: ValidationResultSet
  schema?: V2Schema
  dataset?: V2Dataset
  allowEdit: boolean
  submitting: boolean
  onSubmit: (input: { reviewer: string; rationale: string; decisions: ReviewDecisionInput[] }) => Promise<void>
}

function initialDecisions(results: ValidationResult[]): Record<string, LocalDecision> {
  const initial: Record<string, LocalDecision> = {}
  for (const result of results) {
    initial[result.id] = {
      action: result.status === 'valid' || result.status === 'warning' ? 'approve' : 'exclude',
      note: '',
    }
  }
  return initial
}

export default function ReviewWorkspace({
  validation, schema, dataset, allowEdit, submitting, onSubmit,
}: ReviewWorkspaceProps) {
  const { message } = App.useApp()
  const [reviewer, setReviewer] = useState(() => localStorage.getItem('reqflow.v2.reviewer') ?? '')
  const [rationale, setRationale] = useState('')
  const [decisions, setDecisions] = useState<Record<string, LocalDecision>>(() => initialDecisions(validation.results))
  const [editing, setEditing] = useState<ValidationResult>()
  const [editFields, setEditFields] = useState<Record<string, unknown>>({})
  const [complexFields, setComplexFields] = useState<Record<string, string>>({})

  const properties = schema?.json_schema.properties ?? inferProperties(validation.results)
  const required = new Set(schema?.json_schema.required ?? [])
  const counts = useMemo(() => Object.values(decisions).reduce((out, item) => {
    out[item.action]++
    return out
  }, { approve: 0, edit: 0, exclude: 0 } as Record<ReviewAction, number>), [decisions])

  const patchDecision = (id: string, patch: Partial<LocalDecision>) => {
    setDecisions((current) => ({ ...current, [id]: { ...current[id], ...patch } }))
  }

  const openEditor = (result: ValidationResult) => {
    const fields = { ...(decisions[result.id]?.fields ?? result.fields) }
    const complex: Record<string, string> = {}
    for (const [key, property] of Object.entries(properties)) {
      const type = propertyType(property)
      if (type === 'object' || type === 'array') complex[key] = JSON.stringify(fields[key] ?? (type === 'array' ? [] : {}), null, 2)
    }
    setEditing(result)
    setEditFields(fields)
    setComplexFields(complex)
  }

  const saveEdit = () => {
    if (!editing) return
    const next = { ...editFields }
    try {
      for (const [key, value] of Object.entries(complexFields)) next[key] = JSON.parse(value)
    } catch (error) {
      message.error(`JSON 字段格式错误：${(error as Error).message}`)
      return
    }
    patchDecision(editing.id, { action: 'edit', fields: next })
    setEditing(undefined)
  }

  const submit = async () => {
    if (!reviewer.trim() || !rationale.trim()) {
      message.warning('请填写审核人和审核理由')
      return
    }
    if (counts.approve + counts.edit === 0) {
      message.warning('不能排除全部记录')
      return
    }
    localStorage.setItem('reqflow.v2.reviewer', reviewer.trim())
    await onSubmit({
      reviewer: reviewer.trim(), rationale: rationale.trim(),
      decisions: validation.results.map((result) => {
        const decision = decisions[result.id]
        return {
          validation_result_id: result.id,
          action: decision.action,
          ...(decision.action === 'edit' ? { fields: decision.fields ?? result.fields } : {}),
          ...(decision.note.trim() ? { note: decision.note.trim() } : {}),
        }
      }),
    })
  }

  const columns: ColumnsType<ValidationResult> = [
    { title: '#', dataIndex: 'ordinal', width: 58, render: (value: number) => value + 1 },
    { title: '校验', dataIndex: 'status', width: 125, render: (value) => <ValidationStatusTag status={value} /> },
    {
      title: '记录',
      render: (_, result) => <FieldPreview fields={result.fields} properties={properties} />,
    },
    {
      title: '质量', width: 190,
      render: (_, result) => (
        <Space direction="vertical" size={3}>
          <Text type={result.issues.some((issue) => issue.severity === 'error') ? 'danger' : 'secondary'}>
            {result.issues.length ? `${result.issues.length} 个问题` : '无问题'}
          </Text>
          <Text type="secondary">{result.changes.length} 处确定性转换</Text>
          <Text type="secondary">{result.provenance.source_refs?.length ?? 0} 个来源锚点</Text>
        </Space>
      ),
    },
    {
      title: '决定', width: 125,
      render: (_, result) => (
        <Select<ReviewAction>
          value={decisions[result.id]?.action}
          style={{ width: 112 }}
          options={[
            { value: 'approve', label: '批准', disabled: result.status !== 'valid' && result.status !== 'warning' },
            { value: 'edit', label: '编辑后批准', disabled: !allowEdit },
            { value: 'exclude', label: '排除' },
          ]}
          onChange={(action) => {
            if (action === 'edit') openEditor(result)
            else patchDecision(result.id, { action, fields: undefined })
          }}
        />
      ),
    },
    {
      title: '备注', width: 190,
      render: (_, result) => (
        <Space.Compact style={{ width: '100%' }}>
          <Input
            maxLength={1000}
            placeholder="可选"
            value={decisions[result.id]?.note}
            onChange={(event) => patchDecision(result.id, { note: event.target.value })}
          />
          {decisions[result.id]?.action === 'edit' && (
            <Button icon={<EditOutlined />} onClick={() => openEditor(result)} />
          )}
        </Space.Compact>
      ),
    },
  ]

  return (
    <Card
      title={<Space><SafetyCertificateOutlined /><span>结构化审核工作台</span></Space>}
      extra={<Tag color="blue">校验位点 seq {validation.validated_through_seq}</Tag>}
    >
      <Alert
        type="info"
        showIcon
        message="审核提交会生成不可变 ApprovedRecordSet"
        description="必须逐条覆盖全部校验结果；只有“编辑后批准”会向服务端提交字段，服务端会重新执行 Schema、业务规则和主键冲突校验。"
        style={{ marginBottom: 16 }}
      />
      <Row gutter={12} style={{ marginBottom: 16 }}>
        <Col span={4}><Statistic title="记录" value={validation.record_count} /></Col>
        <Col span={4}><Statistic title="有效" value={validation.valid_count} valueStyle={{ color: '#16a34a' }} /></Col>
        <Col span={4}><Statistic title="警告" value={validation.warning_count} valueStyle={{ color: '#d97706' }} /></Col>
        <Col span={4}><Statistic title="无效/冲突" value={validation.invalid_count + validation.duplicate_count + validation.conflict_count} valueStyle={{ color: '#dc2626' }} /></Col>
        <Col span={4}><Statistic title="将编辑" value={counts.edit} valueStyle={{ color: '#4f46e5' }} /></Col>
        <Col span={4}><Statistic title="将排除" value={counts.exclude} /></Col>
      </Row>
      <Space direction="vertical" size={12} style={{ width: '100%', marginBottom: 16 }}>
        <Space.Compact style={{ width: '100%' }}>
          <Input addonBefore="审核人" maxLength={200} value={reviewer} onChange={(event) => setReviewer(event.target.value)} />
          <Input addonBefore="目标 Dataset" value={dataset ? `${dataset.name} (${dataset.id})` : validation.target_dataset_id} readOnly />
        </Space.Compact>
        <Input.TextArea
          rows={2}
          maxLength={2000}
          showCount
          placeholder="审核理由（必填）：说明本次批准、修复和排除的判断依据"
          value={rationale}
          onChange={(event) => setRationale(event.target.value)}
        />
      </Space>
      <Table<ValidationResult>
        rowKey="id"
        size="small"
        scroll={{ x: 1050 }}
        pagination={false}
        dataSource={validation.results}
        columns={columns}
        expandable={{ expandedRowRender: (result) => <ValidationEvidence result={result} properties={properties} /> }}
      />
      <div style={{ display: 'flex', justifyContent: 'flex-end', marginTop: 16 }}>
        <Button type="primary" size="large" icon={<CheckOutlined />} loading={submitting} onClick={submit}>
          提交完整审核（批准 {counts.approve} / 编辑 {counts.edit} / 排除 {counts.exclude}）
        </Button>
      </div>

      <Drawer
        width={640}
        title={editing ? `编辑第 ${editing.ordinal + 1} 条记录` : '编辑记录'}
        open={Boolean(editing)}
        onClose={() => setEditing(undefined)}
        extra={<Button type="primary" onClick={saveEdit}>保存编辑决定</Button>}
      >
        <Alert type="warning" showIcon message="保存后仍需提交整批审核；服务端会重新校验本条记录。" style={{ marginBottom: 16 }} />
        <Space direction="vertical" size={16} style={{ width: '100%' }}>
          {Object.entries(properties).map(([key, property]) => (
            <div key={key}>
              <Text strong>{property.title ?? key}{required.has(key) ? <Text type="danger"> *</Text> : null}</Text>
              {property.description && <Paragraph type="secondary" style={{ margin: '2px 0 6px' }}>{property.description}</Paragraph>}
              <SchemaInput
                property={property}
                value={editFields[key]}
                complexValue={complexFields[key]}
                onChange={(value) => setEditFields((current) => ({ ...current, [key]: value }))}
                onComplexChange={(value) => setComplexFields((current) => ({ ...current, [key]: value }))}
              />
            </div>
          ))}
        </Space>
      </Drawer>
    </Card>
  )
}

function inferProperties(results: ValidationResult[]): Record<string, JSONSchemaProperty> {
  const properties: Record<string, JSONSchemaProperty> = {}
  for (const result of results) {
    for (const [key, value] of Object.entries(result.fields)) {
      if (!properties[key]) properties[key] = { title: key, type: Array.isArray(value) ? 'array' : typeof value }
    }
  }
  return properties
}

function propertyType(property: JSONSchemaProperty): string {
  const value = Array.isArray(property.type) ? property.type.find((item) => item !== 'null') : property.type
  return value ?? 'string'
}

function SchemaInput({
  property, value, complexValue, onChange, onComplexChange,
}: {
  property: JSONSchemaProperty
  value: unknown
  complexValue?: string
  onChange: (value: unknown) => void
  onComplexChange: (value: string) => void
}) {
  const type = propertyType(property)
  if (property.enum) {
    return <Select style={{ width: '100%' }} value={value} options={property.enum.map((item) => ({ value: item as string | number, label: String(item) }))} onChange={onChange} />
  }
  if (type === 'number' || type === 'integer') {
    return <InputNumber style={{ width: '100%' }} precision={type === 'integer' ? 0 : undefined} value={typeof value === 'number' ? value : undefined} onChange={onChange} />
  }
  if (type === 'boolean') {
    return <Select style={{ width: '100%' }} value={value as boolean} options={[{ value: true, label: 'true' }, { value: false, label: 'false' }]} onChange={onChange} />
  }
  if (type === 'object' || type === 'array') {
    return <Input.TextArea rows={5} value={complexValue} onChange={(event) => onComplexChange(event.target.value)} />
  }
  return <Input value={value == null ? '' : String(value)} onChange={(event) => onChange(event.target.value)} />
}

function FieldPreview({ fields, properties }: { fields: Record<string, unknown>; properties: Record<string, JSONSchemaProperty> }) {
  const entries = Object.keys(properties).map((key) => [key, fields[key]] as const).filter(([, value]) => value !== undefined).slice(0, 3)
  return (
    <Space direction="vertical" size={2}>
      {entries.map(([key, value]) => <Text key={key}><Text type="secondary">{properties[key]?.title ?? key}：</Text>{displayValue(value)}</Text>)}
      {Object.keys(fields).length > entries.length && <Text type="secondary">展开查看全部字段</Text>}
    </Space>
  )
}

function ValidationEvidence({ result, properties }: { result: ValidationResult; properties: Record<string, JSONSchemaProperty> }) {
  return (
    <Space direction="vertical" size={16} style={{ width: '100%', padding: '8px 12px' }}>
      <div>
        <Title level={5}>校验后字段与置信度</Title>
        <Descriptions size="small" bordered column={2} items={Object.entries(result.fields).map(([key, value]) => ({
          key, label: properties[key]?.title ?? key,
          children: <Space>{displayValue(value)}{typeof result.field_confidence?.[key] === 'number' && <Tag color={result.field_confidence[key] >= 0.8 ? 'green' : 'orange'}>{Math.round(result.field_confidence[key] * 100)}%</Tag>}</Space>,
        }))} />
      </div>
      {result.issues.length > 0 && (
        <div><Title level={5}>校验问题</Title>{result.issues.map((issue, index) => <Alert key={`${issue.code}-${index}`} type={issue.severity === 'error' ? 'error' : 'warning'} showIcon message={`${issue.field ? `${issue.field} · ` : ''}${issue.message}`} description={issue.code} style={{ marginBottom: 6 }} />)}</div>
      )}
      {result.changes.length > 0 && (
        <div><Title level={5}>确定性转换差异</Title><Table size="small" pagination={false} rowKey={(change) => `${change.field}-${change.operation}`} dataSource={result.changes} columns={[
          { title: '字段', dataIndex: 'field' }, { title: '操作', dataIndex: 'operation' },
          { title: '原值', dataIndex: 'before', render: displayValue }, { title: '转换后', dataIndex: 'after', render: displayValue },
        ]} /></div>
      )}
      <div>
        <Title level={5}>来源锚点</Title>
        {result.provenance.source_refs?.length
          ? result.provenance.source_refs.map((source, index) => <Paragraph key={`${source.block_id}-${index}`} style={{ marginBottom: 6 }}><Tag>页 {source.page_no || '—'}</Tag><Text code>{source.block_id || source.asset_id}</Text>{source.quote && <Text> · “{source.quote}”</Text>}</Paragraph>)
          : <Text type="secondary">无来源锚点</Text>}
      </div>
    </Space>
  )
}

function displayValue(value: unknown): string {
  if (value == null) return '—'
  if (typeof value === 'string') return value
  return JSON.stringify(value)
}
