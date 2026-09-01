import { App, Button, Descriptions, Empty, Modal, Popconfirm, Space, Table, Tag, Typography } from 'antd'
import { DeleteOutlined } from '@ant-design/icons'
import type { ColumnsType } from 'antd/es/table'
import { useQueryClient } from '@tanstack/react-query'
import { v2CatalogApi } from '../../api/v2/catalog'
import type { JSONSchemaProperty, V2Schema } from '../../api/v2/types'

const { Text } = Typography

interface Props {
  schema?: V2Schema | null
  open: boolean
  onClose: () => void
  /** 删除成功后回调（例如清空表单里对已删结构的选中）。 */
  onDeleted?: (deletedID: string) => void
}

/** 数据结构详情：展示字段定义（名称/类型/必填/说明）并提供删除入口。
 * 删除保护在后端：仍被数据集、抽取规则或索引规则引用的结构会返回 409。 */
export default function SchemaDetailModal({ schema, open, onClose, onDeleted }: Props) {
  const { message } = App.useApp()
  const queryClient = useQueryClient()

  const remove = async () => {
    if (!schema) return
    try {
      await v2CatalogApi.deleteSchema(schema.id)
      message.success(`数据结构「${schema.name}」已删除`)
      onClose()
      await queryClient.invalidateQueries({ queryKey: ['v2-schemas'] })
      onDeleted?.(schema.id)
    } catch (error) {
      message.error((error as Error).message)
    }
  }

  const required = schema?.json_schema.required ?? []
  const fields = Object.entries(schema?.json_schema.properties ?? {})

  return <Modal
    width={720}
    title={`数据结构 · ${schema?.name ?? ''}`}
    open={open && Boolean(schema)}
    onCancel={onClose}
    footer={<Space>
      <Popconfirm
        title="删除该数据结构？"
        description="仍被数据集、抽取规则或索引规则引用的结构无法删除（系统会提示）；删除不影响已入库数据。"
        onConfirm={() => void remove()}
      >
        <Button danger icon={<DeleteOutlined />}>删除结构</Button>
      </Popconfirm>
      <Button type="primary" onClick={onClose}>关闭</Button>
    </Space>}
  >
    {schema ? <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Descriptions bordered size="small" column={2}>
        <Descriptions.Item label="结构名称">{schema.name}</Descriptions.Item>
        <Descriptions.Item label="创建时间">{new Date(schema.created_at).toLocaleString('zh-CN')}</Descriptions.Item>
        <Descriptions.Item label="结构说明" span={2}>{schema.description || '—'}</Descriptions.Item>
      </Descriptions>
      <Table
        rowKey="name" size="small" pagination={false}
        dataSource={fields.map(([name, property]) => ({ name, property }))}
        columns={fieldColumns(required)}
        locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无字段" /> }}
      />
      <Text type="secondary" style={{ fontSize: 12 }}>结构指纹：{schema.schema_hash}</Text>
    </Space> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="结构不存在或已被删除" />}
  </Modal>
}

function fieldColumns(required: string[]): ColumnsType<{ name: string; property: JSONSchemaProperty }> {
  return [
    { title: '字段', render: (_, item) => <Space direction="vertical" size={0}><Text strong>{item.property.title || item.name}</Text><Text code>{item.name}</Text></Space> },
    { title: '类型', width: 120, render: (_, item) => <Tag>{fieldType(item.property)}</Tag> },
    { title: '必填', width: 70, render: (_, item) => required.includes(item.name) ? <Tag color="red">是</Tag> : <Text type="secondary">否</Text> },
    { title: '说明', render: (_, item) => item.property.description || '—' },
  ]
}

function fieldType(property: JSONSchemaProperty) {
  if (property.type === 'array') return `数组<${property.items?.type ?? '对象'}>`
  return Array.isArray(property.type) ? property.type.join(' / ') : property.type || '对象'
}
