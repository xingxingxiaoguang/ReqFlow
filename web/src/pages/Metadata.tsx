import { useEffect, useState } from 'react'
import {
  App, Button, Card, Col, Descriptions, Empty, Input, List, Row, Space, Spin,
  Table, Tabs, Tag, Timeline, Typography,
} from 'antd'
import type { CSSProperties } from 'react'
import { DownloadOutlined, PlayCircleOutlined, ReloadOutlined } from '@ant-design/icons'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { metadataApi } from '../api/metadata'
import type { PromptPreview, StepKind, TaskTypeSummary, TaskTypeView } from '../api/types'
import { ProfileEditor, SchemaEditor } from './MetadataEditors'

const { Text, Paragraph } = Typography

/** 步骤种类展示（与执行器语义对齐：parse/human/analyze/dataset） */
const kindMeta: Record<StepKind, { label: string; color: string }> = {
  parse: { label: '解析', color: 'blue' },
  human: { label: '人工门', color: 'orange' },
  analyze: { label: 'AI 分析', color: 'geekblue' },
  dataset: { label: '数据集', color: 'green' },
}

const preStyle: CSSProperties = {
  margin: 0, padding: 12, background: '#f9fafb', borderRadius: 8,
  maxHeight: 520, overflow: 'auto', fontSize: 12, whiteSpace: 'pre-wrap', wordBreak: 'break-word',
}

/** 提示词文本块（可复制；title 供预览三段区分） */
function PromptBlock({ title, text }: { title: string; text: string }) {
  return (
    <div>
      <Paragraph copyable={{ text }} style={{ marginBottom: 4 }}>
        <Text strong>{title}</Text>
      </Paragraph>
      <pre style={preStyle}>{text}</pre>
    </div>
  )
}

/** 元数据页：任务类型聚合定义的统一目录——看懂一个任务类型从「翻四个文件」变「开一个页面」；
 *  M3 起字段合同/装配描述可受控编辑（保存前自动兼容检查，❌ 拦截 / ⚠️ 需确认） */
export default function Metadata() {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const { data: catalog, isLoading } = useQuery({
    queryKey: ['metadata-catalog'],
    queryFn: metadataApi.catalog,
  })

  const [selected, setSelected] = useState<string>()
  useEffect(() => {
    if (!selected && catalog?.task_types.length) setSelected(catalog.task_types[0].type)
  }, [catalog, selected])

  const { data: view, isFetching: viewLoading } = useQuery({
    queryKey: ['metadata-task-type', selected],
    queryFn: () => metadataApi.taskType(selected!),
    enabled: !!selected,
  })

  /* 提示词预览：按需渲染（special 变化后手动刷新；切换任务类型自动重渲） */
  const [special, setSpecial] = useState('')
  const [preview, setPreview] = useState<PromptPreview | null>(null)
  const [previewLoading, setPreviewLoading] = useState(false)
  const renderPreview = async (taskType: string) => {
    setPreviewLoading(true)
    try {
      setPreview(await metadataApi.promptPreview({ task_type: taskType, special_requirements: special }))
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setPreviewLoading(false)
    }
  }
  useEffect(() => {
    if (selected) void renderPreview(selected)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selected])

  /* 受控编辑保存/回退后：刷新聚合视图与目录（effective 即时生效），预览重渲 */
  const handleSaved = () => {
    void queryClient.invalidateQueries({ queryKey: ['metadata-task-type', selected] })
    void queryClient.invalidateQueries({ queryKey: ['metadata-catalog'] })
    if (selected) void renderPreview(selected)
  }

  /* 导出 effective 视图（JSON 文件留档 / 跨环境分发人工导入） */
  const doExport = async () => {
    try {
      const doc = await metadataApi.export()
      const blob = new Blob([JSON.stringify(doc, null, 2)], { type: 'application/json' })
      const url = URL.createObjectURL(blob)
      const a = document.createElement('a')
      a.href = url
      a.download = `reqflow-metadata-${new Date().toISOString().slice(0, 10)}.json`
      a.click()
      URL.revokeObjectURL(url)
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  if (isLoading) return <Card loading />
  if (!catalog?.task_types.length) {
    return <Card><Empty description="注册表为空（无已注册任务类型）" /></Card>
  }

  const renderSummary = (s: TaskTypeSummary) => (
    <List.Item
      style={{ cursor: 'pointer', padding: '10px 12px', borderRadius: 8,
        background: s.type === selected ? '#eef2ff' : undefined }}
      onClick={() => setSelected(s.type)}
    >
      <List.Item.Meta
        title={<Space>
          <Text strong={s.type === selected}>{s.name}</Text>
          <Tag style={{ marginInlineEnd: 0 }}>{s.source === 'builtin' ? '内置' : '覆盖'}</Tag>
        </Space>}
        description={<Text type="secondary" style={{ fontSize: 12 }}>
          {s.step_count} 步 · 产出 {s.schema_label || s.dataset_type || '—'}
        </Text>}
      />
    </List.Item>
  )

  return (
    <Row gutter={16}>
      <Col span={6}>
        <Card
          title={<Text strong>任务类型</Text>}
          styles={{ body: { padding: 8 } }}
          extra={<Button size="small" icon={<DownloadOutlined />} onClick={doExport}>导出</Button>}
        >
          <List dataSource={catalog.task_types} renderItem={renderSummary} split={false} />
        </Card>
      </Col>
      <Col span={18}>
        <Card loading={viewLoading && !view}>
          {!view ? <Spin /> : <TaskTypeDetail view={view} preview={preview} previewLoading={previewLoading}
            special={special} onSpecialChange={setSpecial} onRender={() => selected && void renderPreview(selected)}
            onSaved={handleSaved} />}
        </Card>
      </Col>
    </Row>
  )
}

/** 聚合详情：概览（步骤链）/ 字段合同（受控编辑）/ 装配描述（受控编辑）/ 提示词预览 */
function TaskTypeDetail({
  view, preview, previewLoading, special, onSpecialChange, onRender, onSaved,
}: {
  view: TaskTypeView
  preview: PromptPreview | null
  previewLoading: boolean
  special: string
  onSpecialChange: (v: string) => void
  onRender: () => void
  onSaved: () => void
}) {
  return (
    <>
      <Paragraph type="secondary" style={{ marginBottom: 16 }}>
        <Text strong>{view.name}</Text>（<Text code>{view.type}</Text>）· {view.desc}
      </Paragraph>
      <Tabs
        defaultActiveKey="overview"
        items={[
          {
            key: 'overview',
            label: '概览',
            children: (
              <>
                <Descriptions bordered size="small" column={2}>
                  <Descriptions.Item label="来源">
                    <Tag color={view.source === 'builtin' ? 'default' : 'purple'}>
                      {view.source === 'builtin' ? '内置（随版本发布）' : '数据库覆盖'}
                    </Tag>
                  </Descriptions.Item>
                  <Descriptions.Item label="产出数据集类型"><Text code>{view.dataset_type}</Text></Descriptions.Item>
                  <Descriptions.Item label="字段合同">
                    {view.schema.label}（v{view.schema.version} · {view.schema.fields.length} 字段）
                  </Descriptions.Item>
                  <Descriptions.Item label="步骤数">{view.workflow.steps.length}</Descriptions.Item>
                </Descriptions>
                <Typography.Title level={5} style={{ marginTop: 24 }}>步骤链</Typography.Title>
                <Timeline
                  items={view.workflow.steps.map((s) => ({
                    children: (
                      <div>
                        <Text strong>{s.seq}. {s.name}</Text>{' '}
                        <Tag color={kindMeta[s.kind]?.color}>{kindMeta[s.kind]?.label ?? s.kind}</Tag>
                        {(s.deps ?? []).map((d, i) => (
                          <div key={i}>
                            <Text type="secondary" style={{ fontSize: 12 }}>
                              依赖 {d.data} · 工具 {d.tool}
                            </Text>
                          </div>
                        ))}
                      </div>
                    ),
                  }))}
                />
              </>
            ),
          },
          {
            key: 'schema',
            label: '字段合同',
            children: <SchemaEditor view={view} onSaved={onSaved} />,
          },
          {
            key: 'profile',
            label: '装配描述',
            children: (
              <>
                <ProfileEditor view={view} onSaved={onSaved} />
                <Typography.Title level={5} style={{ marginTop: 24 }}>
                  工具清单（写入绑定：<Text code>{view.profile.write.tool_name}</Text>）
                </Typography.Title>
                <Table
                  rowKey="name"
                  size="small"
                  pagination={false}
                  dataSource={view.tools}
                  columns={[
                    { title: '工具', dataIndex: 'name', width: 170, render: (n: string) => <Text code>{n}</Text> },
                    { title: '说明', dataIndex: 'description' },
                  ]}
                />
              </>
            ),
          },
          {
            key: 'preview',
            label: '提示词预览',
            children: (
              <>
                <Paragraph type="secondary">
                  按当前注册定义实时渲染，与运行时装配同一函数——所见即新任务所得。
                  首轮消息为模板示意（规模数字随实际文档）。
                </Paragraph>
                <Input.TextArea
                  rows={2}
                  value={special}
                  onChange={(e) => onSpecialChange(e.target.value)}
                  placeholder="额外要求（可选）——模拟用户在确认解析门填写的补充要求"
                  style={{ marginBottom: 12, maxWidth: 560 }}
                />
                <div style={{ marginBottom: 16 }}>
                  <Button type="primary" icon={previewLoading ? <ReloadOutlined spin /> : <PlayCircleOutlined />}
                    loading={previewLoading} onClick={onRender}>
                    渲染预览
                  </Button>
                </div>
                {preview && (
                  <Tabs
                    items={[
                      { key: 'system', label: 'agent 系统提示词', children: <PromptBlock title="SystemPrompt（agent 模式）" text={preview.agent_system_prompt} /> },
                      { key: 'first', label: '首轮消息', children: <PromptBlock title="首轮 user 消息（文档清单 + 首步指引）" text={preview.agent_first_message} /> },
                      { key: 'classic', label: '单发 prompt', children: <PromptBlock title="单发直调完整 prompt（默认模式 / agent 降级目标）" text={preview.classic_prompt} /> },
                    ]}
                  />
                )}
              </>
            ),
          },
        ]}
      />
    </>
  )
}
