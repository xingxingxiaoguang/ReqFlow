import { useEffect, useMemo, useState } from 'react'
import {
  Alert, App, Button, Card, Descriptions, Form, Input, InputNumber, Modal,
  Popconfirm, Select, Space, Switch, Table, Tabs, Tag, Typography,
} from 'antd'
import { CheckCircleOutlined, DeleteOutlined, EditOutlined, LockOutlined, PlusOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import {
  platformConfigsApi, type PlatformConfigGroup, type PlatformConfigInput,
  type PlatformConfigItem, type PlatformConfigKind, type PlatformConfigValue,
} from '../api/platformConfigs'

const { Text, Paragraph, Title } = Typography

const kindMeta: Record<PlatformConfigKind, { label: string; description: string; secretLabel: string }> = {
  llm: { label: 'LLM 模型', description: 'Agent、文档抽取和知识分析使用的对话模型。', secretLabel: 'API Key' },
  embedding: { label: '向量化', description: '语义检索和向量索引使用的 Embedding 模型。', secretLabel: 'API Key' },
  rerank: { label: '重排序', description: '混合检索候选结果的精排模型，凭据与向量化配置相互独立。', secretLabel: 'API Key' },
  mineru: { label: 'MinerU', description: 'PDF 云端解析服务；docx、md、txt 仍在本地解析。', secretLabel: 'API Token' },
}

const defaultValues: Record<PlatformConfigKind, Record<string, unknown>> = {
  llm: { provider: 'openai', temperature: 0.2, max_tokens: 8192, timeout_ms: 300000, activate: true },
  embedding: { dimensions: 1024, batch_size: 32, timeout_ms: 30000, activate: true },
  rerank: { timeout_ms: 60000, activate: true },
  mineru: { model_version: 'vlm', timeout_ms: 600000, poll_interval_ms: 5000, activate: true },
}

export default function Settings() {
  const { message } = App.useApp()
  const client = useQueryClient()
  const catalog = useQuery({ queryKey: ['platform-configs'], queryFn: platformConfigsApi.catalog })
  const [modal, setModal] = useState<{ kind: PlatformConfigKind; item?: PlatformConfigItem }>()
  const [form] = Form.useForm()

  useEffect(() => {
    if (!modal) return
    form.resetFields()
    form.setFieldsValue(modal.item
      ? { name: modal.item.name, ...modal.item.config, secret: '' }
      : defaultValues[modal.kind])
  }, [form, modal])

  const refresh = async () => {
    await client.invalidateQueries({ queryKey: ['platform-configs'] })
  }
  const save = useMutation({
    mutationFn: async (values: Record<string, unknown>) => {
      if (!modal) throw new Error('配置类型不存在')
      const input = toInput(modal.kind, values)
      return modal.item
        ? platformConfigsApi.update(modal.kind, modal.item.id, input)
        : platformConfigsApi.create(modal.kind, input)
    },
    onSuccess: async () => {
      message.success(modal?.item ? '配置已更新' : '配置已创建')
      form.resetFields()
      setModal(undefined)
      await refresh()
    },
    onError: (error) => message.error((error as Error).message),
  })

  const groups = useMemo(
    () => new Map(catalog.data?.groups.map((group) => [group.kind, group])),
    [catalog.data],
  )

  const openCreate = (kind: PlatformConfigKind) => {
    setModal({ kind })
  }
  const openEdit = (item: PlatformConfigItem) => {
    setModal({ kind: item.kind, item })
  }
  const activate = async (item: PlatformConfigItem) => {
    try {
      await platformConfigsApi.activate(item.kind, item.id)
      message.success(`已激活「${item.name}」`)
      await refresh()
    } catch (error) {
      message.error((error as Error).message)
    }
  }
  const remove = async (item: PlatformConfigItem) => {
    try {
      await platformConfigsApi.remove(item.kind, item.id)
      message.success('配置已删除')
      await refresh()
    } catch (error) {
      message.error((error as Error).message)
    }
  }

  if (catalog.isLoading || !catalog.data) return <Card loading />

  return (
    <div style={{ padding: 24 }}>
      <div style={{ maxWidth: 1180, margin: '0 auto' }}>
        <Title level={3} style={{ marginBottom: 4 }}>平台配置</Title>
        <Paragraph type="secondary">
          每类能力可以保存多份配置，但任一时刻只会有一份生效。切换后下一次模型或工具调用立即使用新配置，无需重启服务。
        </Paragraph>
        <Alert
          type="info"
          showIcon
          message="config.yaml 仅作为只读兜底"
          description="配置文件项始终可见，但不能编辑或删除；当该类别没有数据库配置被激活时，它会自动生效。所有数据库密钥均加密保存，页面和接口不会返回明文。"
          style={{ marginBottom: 20 }}
        />
        <Tabs
          items={(Object.keys(kindMeta) as PlatformConfigKind[]).map((kind) => ({
            key: kind,
            label: <Space>{kindMeta[kind].label}<StatusTag ok={catalog.data.summary[kind]} /></Space>,
            children: <ConfigGroupPanel
              group={groups.get(kind)}
              onCreate={() => openCreate(kind)}
              onEdit={openEdit}
              onActivate={activate}
              onDelete={remove}
            />,
          }))}
        />
      </div>

      <Modal
        title={`${modal?.item ? '编辑' : '新增'}${modal ? kindMeta[modal.kind].label : ''}配置`}
        open={Boolean(modal)}
        onCancel={() => { form.resetFields(); setModal(undefined) }}
        onOk={() => form.submit()}
        confirmLoading={save.isPending}
        width={680}
        destroyOnHidden
      >
        {modal && (
          <Form form={form} layout="vertical" onFinish={(values) => save.mutate(values)} preserve={false}>
            <Form.Item name="name" label="配置名称" rules={[{ required: true, message: '请输入配置名称' }, { max: 80 }]}>
              <Input placeholder="例如：生产主模型" />
            </Form.Item>
            <ConfigFields kind={modal.kind} />
            <Form.Item
              name="secret"
              label={kindMeta[modal.kind].secretLabel}
              rules={modal.item ? [] : [{ required: true, message: `请输入 ${kindMeta[modal.kind].secretLabel}` }]}
              extra={modal.item ? '留空表示保留当前密钥；输入新值才会替换。' : '密钥提交后只保存密文，之后不会再次展示。'}
            >
              <Input.Password autoComplete="new-password" placeholder={modal.item ? '••••••••（保持不变）' : '输入密钥'} />
            </Form.Item>
            {!modal.item && (
              <Form.Item name="activate" label="创建后立即激活" valuePropName="checked">
                <Switch />
              </Form.Item>
            )}
          </Form>
        )}
      </Modal>
    </div>
  )
}

function ConfigGroupPanel({ group, onCreate, onEdit, onActivate, onDelete }: {
  group?: PlatformConfigGroup
  onCreate: () => void
  onEdit: (item: PlatformConfigItem) => void
  onActivate: (item: PlatformConfigItem) => void
  onDelete: (item: PlatformConfigItem) => void
}) {
  if (!group) return <Card loading />
  const meta = kindMeta[group.kind]
  return (
    <Card
      title={<div><Text strong>{meta.label}</Text><div><Text type="secondary" style={{ fontSize: 13 }}>{meta.description}</Text></div></div>}
      extra={<Button type="primary" icon={<PlusOutlined />} onClick={onCreate}>新增配置</Button>}
    >
      <Table<PlatformConfigItem>
        rowKey="id"
        pagination={false}
        dataSource={group.items}
        columns={[
          {
            title: '名称', dataIndex: 'name', width: 220,
            render: (name: string, item: PlatformConfigItem) => (
              <Space direction="vertical" size={2}>
                <Space><Text strong={item.active}>{name}</Text>{item.active && <Tag color="green" icon={<CheckCircleOutlined />}>当前生效</Tag>}</Space>
                {item.source === 'file' ? <Tag icon={<LockOutlined />}>配置文件 · 只读</Tag> : <Tag color="blue">平台配置</Tag>}
              </Space>
            ),
          },
          { title: '服务地址', render: (_: unknown, item: PlatformConfigItem) => <Text code>{endpointOf(item)}</Text> },
          { title: '模型/版本', width: 210, render: (_: unknown, item: PlatformConfigItem) => modelOf(item) || '—' },
          { title: '密钥', width: 110, render: (_: unknown, item: PlatformConfigItem) => <StatusTag ok={item.secret_configured} /> },
          {
            title: '操作', width: 250,
            render: (_: unknown, item: PlatformConfigItem) => (
              <Space>
                <Button size="small" type={item.active ? 'default' : 'primary'} disabled={item.active} onClick={() => onActivate(item)}>
                  {item.active ? '已激活' : item.source === 'file' ? '使用兜底' : '激活'}
                </Button>
                {!item.read_only && <Button size="small" icon={<EditOutlined />} onClick={() => onEdit(item)}>编辑</Button>}
                {!item.read_only && (
                  <Popconfirm title="确认删除这份配置？" description={item.active ? '删除后将自动回退到 config.yaml。' : undefined} onConfirm={() => onDelete(item)}>
                    <Button size="small" danger icon={<DeleteOutlined />} />
                  </Popconfirm>
                )}
              </Space>
            ),
          },
        ]}
        expandable={{ expandedRowRender: (item) => <ConfigDescriptions item={item} /> }}
      />
    </Card>
  )
}

function ConfigFields({ kind }: { kind: PlatformConfigKind }) {
  if (kind === 'llm') return <>
    <Form.Item name="provider" label="协议" rules={[{ required: true }]}><Select options={[{ value: 'openai', label: 'OpenAI 兼容' }, { value: 'anthropic', label: 'Anthropic Messages' }]} /></Form.Item>
    <Form.Item name="base_url" label="Base URL" rules={urlRules}><Input placeholder="https://api.example.com/v1" /></Form.Item>
    <Form.Item name="model" label="模型" rules={[{ required: true }]}><Input placeholder="模型标识" /></Form.Item>
    <Space size="large" wrap>
      <Form.Item name="temperature" label="Temperature" rules={[{ required: true }]}><InputNumber min={0} max={2} step={0.1} /></Form.Item>
      <Form.Item name="max_tokens" label="最大输出 Token" rules={[{ required: true }]}><InputNumber min={0} /></Form.Item>
      <Form.Item name="timeout_ms" label="超时（毫秒）" rules={[{ required: true }]}><InputNumber min={1} /></Form.Item>
    </Space>
  </>
  if (kind === 'embedding') return <>
    <Form.Item name="base_url" label="Base URL" rules={urlRules}><Input placeholder="https://api.example.com/v1" /></Form.Item>
    <Form.Item name="model" label="模型" rules={[{ required: true }]}><Input placeholder="BAAI/bge-m3" /></Form.Item>
    <Space size="large" wrap>
      <Form.Item name="dimensions" label="向量维度" rules={[{ required: true }]} extra="当前固定为 1024"><InputNumber min={1024} max={1024} disabled /></Form.Item>
      <Form.Item name="batch_size" label="批大小" rules={[{ required: true }]}><InputNumber min={1} max={2048} /></Form.Item>
      <Form.Item name="timeout_ms" label="超时（毫秒）" rules={[{ required: true }]}><InputNumber min={1} /></Form.Item>
    </Space>
  </>
  if (kind === 'rerank') return <>
    <Form.Item name="base_url" label="Base URL" rules={urlRules}><Input placeholder="https://api.example.com/v1" /></Form.Item>
    <Form.Item name="model" label="模型" rules={[{ required: true }]}><Input placeholder="BAAI/bge-reranker-v2-m3" /></Form.Item>
    <Form.Item name="timeout_ms" label="超时（毫秒）" rules={[{ required: true }]}><InputNumber min={1} /></Form.Item>
  </>
  return <>
    <Form.Item name="api_url" label="API URL" rules={urlRules}><Input placeholder="https://mineru.net" /></Form.Item>
    <Form.Item name="model_version" label="模型版本" rules={[{ required: true }]}><Select options={[{ value: 'vlm', label: 'VLM' }, { value: 'pipeline', label: 'Pipeline' }]} /></Form.Item>
    <Space size="large" wrap>
      <Form.Item name="timeout_ms" label="总超时（毫秒）" rules={[{ required: true }]}><InputNumber min={1} /></Form.Item>
      <Form.Item name="poll_interval_ms" label="轮询间隔（毫秒）" rules={[{ required: true }]}><InputNumber min={1} /></Form.Item>
    </Space>
  </>
}

const urlRules = [
  { required: true, message: '请输入服务地址' },
  { type: 'url' as const, message: '请输入有效的 http/https URL' },
]

function toInput(kind: PlatformConfigKind, values: Record<string, unknown>): PlatformConfigInput {
  const shared = { name: String(values.name ?? ''), secret: String(values.secret ?? ''), activate: Boolean(values.activate) }
  if (kind === 'llm') return { ...shared, config: pick(values, ['provider', 'base_url', 'model', 'temperature', 'max_tokens', 'timeout_ms']) as unknown as PlatformConfigValue }
  if (kind === 'embedding') return { ...shared, config: pick(values, ['base_url', 'model', 'dimensions', 'batch_size', 'timeout_ms']) as unknown as PlatformConfigValue }
  if (kind === 'rerank') return { ...shared, config: pick(values, ['base_url', 'model', 'timeout_ms']) as unknown as PlatformConfigValue }
  return { ...shared, config: { ...pick(values, ['api_url', 'model_version', 'timeout_ms', 'poll_interval_ms']), enabled: true } as unknown as PlatformConfigValue }
}

function pick(values: Record<string, unknown>, keys: string[]) {
  return Object.fromEntries(keys.map((key) => [key, values[key]]))
}

function endpointOf(item: PlatformConfigItem) {
  const value = item.config as unknown as Record<string, unknown>
  return String(value.base_url ?? value.api_url ?? '—')
}

function modelOf(item: PlatformConfigItem) {
  const value = item.config as unknown as Record<string, unknown>
  return String(value.model ?? value.model_version ?? '')
}

function StatusTag({ ok }: { ok: boolean }) {
  return ok ? <Tag color="green">已配置</Tag> : <Tag color="orange">未配置</Tag>
}

function ConfigDescriptions({ item }: { item: PlatformConfigItem }) {
  const entries = Object.entries(item.config as unknown as Record<string, unknown>)
  return (
    <Descriptions size="small" column={3} bordered>
      {entries.map(([key, value]) => <Descriptions.Item key={key} label={key}>{String(value)}</Descriptions.Item>)}
      <Descriptions.Item label="密钥">{item.secret_configured ? '已安全保存（不可查看）' : '未配置'}</Descriptions.Item>
      {item.updated_at && <Descriptions.Item label="更新时间">{new Date(item.updated_at).toLocaleString()}</Descriptions.Item>}
    </Descriptions>
  )
}
