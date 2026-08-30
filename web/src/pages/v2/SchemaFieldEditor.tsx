import { useMemo } from 'react'
import { Button, Card, Col, Empty, Input, Row, Select, Space, Switch, Typography } from 'antd'
import { DeleteOutlined, PlusOutlined } from '@ant-design/icons'
import type { JSONSchemaProperty, V2JSONSchema } from '../../api/v2/types'

const { Text } = Typography

export type FieldKind =
  | 'string'
  | 'integer'
  | 'number'
  | 'boolean'
  | 'date'
  | 'datetime'
  | 'enum'
  | 'string_array'
  | 'object'
  | 'object_array'

export interface SchemaFieldDraft {
  id: string
  name: string
  title: string
  description: string
  kind: FieldKind
  required: boolean
  enumValues: string[]
  children: SchemaFieldDraft[]
}

let draftSequence = 0

export function createSchemaField(
  values: Partial<Omit<SchemaFieldDraft, 'id'>> = {},
): SchemaFieldDraft {
  draftSequence += 1
  return {
    id: `schema-field-${draftSequence}`,
    name: values.name ?? `field_${draftSequence}`,
    title: values.title ?? '',
    description: values.description ?? '',
    kind: values.kind ?? 'string',
    required: values.required ?? false,
    enumValues: values.enumValues ?? [],
    children: values.children ?? [],
  }
}

const kindOptions: Array<{ value: FieldKind; label: string }> = [
  { value: 'string', label: '文本' },
  { value: 'integer', label: '整数' },
  { value: 'number', label: '小数' },
  { value: 'boolean', label: '是 / 否' },
  { value: 'date', label: '日期' },
  { value: 'datetime', label: '日期时间' },
  { value: 'enum', label: '单选项' },
  { value: 'string_array', label: '多个文本' },
  { value: 'object', label: '字段分组' },
  { value: 'object_array', label: '多条分组记录' },
]

function nextFieldName(fields: SchemaFieldDraft[]) {
  const names = new Set(fields.map((field) => field.name))
  let index = 1
  while (names.has(`field_${index}`)) index += 1
  return `field_${index}`
}

function isNested(kind: FieldKind) {
  return kind === 'object' || kind === 'object_array'
}

export function SchemaFieldEditor({
  fields,
  onChange,
  depth = 0,
}: {
  fields: SchemaFieldDraft[]
  onChange: (fields: SchemaFieldDraft[]) => void
  depth?: number
}) {
  const compact = depth > 0
  const update = (index: number, patch: Partial<SchemaFieldDraft>) => {
    onChange(fields.map((field, current) => current === index ? { ...field, ...patch } : field))
  }
  const remove = (index: number) => onChange(fields.filter((_, current) => current !== index))
  const add = () => onChange([...fields, createSchemaField({ name: nextFieldName(fields) })])

  if (fields.length === 0) {
    return <Card size="small" styles={{ body: { padding: 16 } }}>
      <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="还没有字段">
        <Button type="dashed" icon={<PlusOutlined />} onClick={add}>添加第一个字段</Button>
      </Empty>
    </Card>
  }

  return <Space direction="vertical" size={12} style={{ width: '100%' }}>
    {fields.map((field, index) => <Card
      key={field.id}
      size="small"
      title={<Space><Text strong>{field.title || `字段 ${index + 1}`}</Text><Text type="secondary">{field.name}</Text></Space>}
      extra={<Button type="text" danger icon={<DeleteOutlined />} onClick={() => remove(index)}>删除</Button>}
      styles={{ body: { padding: compact ? 12 : 16 } }}
    >
      <Row gutter={12}>
        <Col xs={24} md={8}>
          <Text type="secondary">字段名称</Text>
          <Input
            value={field.title}
            placeholder="例如：产品名称"
            onChange={(event) => update(index, { title: event.target.value })}
          />
        </Col>
        <Col xs={24} md={7}>
          <Text type="secondary">字段编码</Text>
          <Input
            value={field.name}
            placeholder="英文 snake_case"
            onChange={(event) => update(index, { name: event.target.value })}
          />
        </Col>
        <Col xs={16} md={6}>
          <Text type="secondary">数据类型</Text>
          <Select
            value={field.kind}
            options={kindOptions}
            style={{ width: '100%' }}
            onChange={(kind: FieldKind) => update(index, {
              kind,
              enumValues: kind === 'enum' ? field.enumValues : [],
              children: isNested(kind) ? (field.children.length ? field.children : [createSchemaField({ name: 'field_1' })]) : [],
            })}
          />
        </Col>
        <Col xs={8} md={3}>
          <Text type="secondary">必须填写</Text>
          <div style={{ paddingTop: 5 }}><Switch checked={field.required} onChange={(required) => update(index, { required })} /></div>
        </Col>
      </Row>
      <div style={{ marginTop: 10 }}>
        <Text type="secondary">业务说明</Text>
        <Input
          value={field.description}
          placeholder="告诉 AI 和使用者这个字段表示什么"
          onChange={(event) => update(index, { description: event.target.value })}
        />
      </div>
      {field.kind === 'enum' && <div style={{ marginTop: 10 }}>
        <Text type="secondary">可选值</Text>
        <Select
          mode="tags"
          value={field.enumValues}
          tokenSeparators={[',', '，']}
          placeholder="输入一个选项后按回车，例如：高、中、低"
          style={{ width: '100%' }}
          onChange={(enumValues) => update(index, { enumValues })}
        />
      </div>}
      {isNested(field.kind) && <div style={{ marginTop: 14, paddingLeft: 12, borderLeft: '3px solid #e5e7eb' }}>
        <Text strong>{field.kind === 'object_array' ? '每条记录包含的字段' : '分组内字段'}</Text>
        <div style={{ marginTop: 8 }}>
          <SchemaFieldEditor fields={field.children} depth={depth + 1} onChange={(children) => update(index, { children })} />
        </div>
      </div>}
    </Card>)}
    <Button type="dashed" block icon={<PlusOutlined />} onClick={add}>添加字段</Button>
  </Space>
}

export function validateSchemaFields(fields: SchemaFieldDraft[], path = '字段'): string | undefined {
  if (fields.length === 0) return `${path}至少需要一项`
  const names = new Set<string>()
  for (const field of fields) {
    if (!field.title.trim()) return `${path}“${field.name || '未命名'}”缺少中文名称`
    if (!/^[a-z][a-z0-9_]*$/.test(field.name.trim())) return `${path}“${field.title}”的编码必须是小写英文、数字或下划线，并以英文字母开头`
    if (names.has(field.name.trim())) return `${path}编码“${field.name}”重复`
    names.add(field.name.trim())
    if (field.kind === 'enum' && field.enumValues.filter((value) => value.trim()).length === 0) return `${path}“${field.title}”至少需要一个可选值`
    if (isNested(field.kind)) {
      const childError = validateSchemaFields(field.children, `${path}“${field.title}”内的字段`)
      if (childError) return childError
    }
  }
  return undefined
}

function objectSchema(fields: SchemaFieldDraft[], title?: string): V2JSONSchema {
  const properties: Record<string, JSONSchemaProperty> = {}
  const required: string[] = []
  for (const field of fields) {
    properties[field.name.trim()] = fieldSchema(field)
    if (field.required) required.push(field.name.trim())
  }
  return {
    type: 'object',
    ...(title ? { title } : {}),
    additionalProperties: false,
    properties,
    ...(required.length ? { required } : {}),
  }
}

function fieldSchema(field: SchemaFieldDraft): JSONSchemaProperty {
  const common = {
    title: field.title.trim(),
    ...(field.description.trim() ? { description: field.description.trim() } : {}),
  }
  switch (field.kind) {
    case 'integer': return { ...common, type: 'integer' }
    case 'number': return { ...common, type: 'number' }
    case 'boolean': return { ...common, type: 'boolean' }
    case 'date': return { ...common, type: 'string', format: 'date' }
    case 'datetime': return { ...common, type: 'string', format: 'date-time' }
    case 'enum': return { ...common, type: 'string', enum: field.enumValues.map((value) => value.trim()).filter(Boolean) }
    case 'string_array': return { ...common, type: 'array', items: { type: 'string' } }
    case 'object': return { ...common, ...objectSchema(field.children) }
    case 'object_array': return { ...common, type: 'array', items: objectSchema(field.children) as JSONSchemaProperty }
    default: return { ...common, type: 'string' }
  }
}

export function buildObjectSchema(fields: SchemaFieldDraft[], title?: string) {
  return objectSchema(fields, title)
}

export function schemaFieldOptions(schema?: V2JSONSchema, textOnly = false) {
  return Object.entries(schema?.properties ?? {})
    .filter(([, property]) => !textOnly || property.type === 'string' || (property.type === 'array' && property.items?.type === 'string'))
    .map(([name, property]) => ({ value: name, label: property.title ? `${property.title}（${name}）` : name }))
}

export function schemaSummary(schema?: V2JSONSchema) {
  return Object.entries(schema?.properties ?? {}).map(([name, property]) => property.title || name)
}

export function SchemaFieldSummary({ schema }: { schema?: V2JSONSchema }) {
  const fields = useMemo(() => schemaSummary(schema), [schema])
  return fields.length ? <Text>{fields.join('、')}</Text> : <Text type="secondary">暂无字段</Text>
}
