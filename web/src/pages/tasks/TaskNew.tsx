import { useMemo, useState } from 'react'
import { Card, Typography, Alert, App, Timeline, Tag, Space, Radio, Select, Input, Table } from 'antd'
import { InboxOutlined, LinkOutlined, ToolOutlined, DatabaseOutlined } from '@ant-design/icons'
import { Upload as AntUpload } from 'antd'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../../api/client'
import { tasksApi } from '../../api/tasks'
import { parseDatasetSchema } from '../../api/types'
import type { Dataset, DatasetSchema, FieldSpec, SettingsView, StepKind, Workflow } from '../../api/types'

const { Dragger } = AntUpload
const { Text } = Typography

/** 步骤种类 → 人读标签（analyze 按实际模式显示，见 kindLabelOf） */
const KIND_LABEL: Record<Exclude<StepKind, 'analyze'>, string> = {
  parse: '机器执行 · 解析',
  human: '人工确认门',
  dataset: '机器执行 · 生成数据集',
}

/** analyze 步骤标签如实反映执行模式（agent_mode 开关决定，不再无条件标 AI agent） */
const kindLabelOf = (kind: StepKind, agentMode?: boolean): string =>
  kind === 'analyze' ? (agentMode ? '机器执行 · AI agent（工具驱动）' : '机器执行 · AI 分析（单轮直调）') : KIND_LABEL[kind]

/** 新建任务：先选定目标数据集（字段元数据自动带出）→ 上传文档 → 创建任务 → 触发解析 → 跳转详情页。
 * 字段定义归属数据集：分析提示词、草稿校验、写入全程按所选数据集自身的 schema 执行。 */
export default function TaskNew() {
  const navigate = useNavigate()
  const { message } = App.useApp()
  const [parsing, setParsing] = useState(false)
  const [specialRequirements, setSpecialRequirements] = useState('')
  const [mode, setMode] = useState<'existing' | 'create'>('existing')
  const [datasetId, setDatasetId] = useState<string>()
  const [newName, setNewName] = useState('')
  const [templateType, setTemplateType] = useState<string>()
  const { data: settings } = useQuery({ queryKey: ['settings'], queryFn: () => api.get<SettingsView>('/api/settings') })
  const { data: workflows } = useQuery({ queryKey: ['workflows'], queryFn: () => tasksApi.workflows() })
  const { data: dsData } = useQuery({ queryKey: ['datasets', 'all'], queryFn: () => tasksApi.listDatasets({ limit: 100 }) })
  const { data: tplData } = useQuery({ queryKey: ['schemas'], queryFn: () => tasksApi.schemas() })
  const workflow: Workflow | undefined = workflows?.workflows.find((w) => w.type === 'requirement_import')
  const datasets = (dsData?.datasets ?? []).filter((d) => d.Status === 'ready')
  const templates = tplData?.schemas ?? []

  // 选定即带出：已有数据集 → 解析行内 schema；新建 → 取类型模板（缺省第一个）
  const selected = datasets.find((d) => d.ID === datasetId)
  const effTemplateType = templateType ?? templates[0]?.type
  const schema: DatasetSchema | undefined = useMemo(() => {
    if (mode === 'existing') return parseDatasetSchema(selected?.Schema) ?? undefined
    return templates.find((t) => t.type === effTemplateType)
  }, [mode, selected, templates, effTemplateType])

  const newDatasetValid = mode === 'create' && !!newName.trim() && !!effTemplateType
  const datasetReady = mode === 'existing' ? !!datasetId : newDatasetValid

  const handleFile = async (file: File) => {
    setParsing(true)
    try {
      // 新建模式：先建数据集（字段定义从模板固化到数据集行），再绑定创建任务
      let target: Dataset
      if (mode === 'existing') {
        if (!selected) { message.warning('请选择目标数据集'); setParsing(false); return false }
        target = selected
      } else {
        const created = await tasksApi.createDataset({ name: newName.trim(), type: effTemplateType! })
        target = created.dataset
      }
      const { task } = await tasksApi.create('requirement_import', file.name, target.ID)
      await tasksApi.triggerParse(task.ID, file)
      if (specialRequirements.trim()) {
        await tasksApi.patch(task.ID, { special_requirements: specialRequirements.trim() })
      }
      navigate(`/tasks/${task.ID}`)
    } catch (e) {
      message.error((e as Error).message || '任务创建失败')
      setParsing(false)
    }
    return false // 阻止 antd 自动上传
  }

  return (
    <Card style={{ maxWidth: 860 }} title={<Text strong>新建需求导入任务</Text>}>
      {settings && !settings.llm.configured && (
        <Alert
          type="warning" showIcon style={{ marginBottom: 16 }}
          message="LLM 未配置"
          description="尚未配置 llm.api_key，文档可以解析，但 AI 分析不可用。请编辑 config.yaml 后重启（见设置页）。"
        />
      )}
      {workflow && (
        <Card size="small" title={<Text strong>任务流程 · {workflow.name}</Text>} style={{ marginBottom: 16 }}
          extra={<Text type="secondary" style={{ fontSize: 12 }}>{workflow.desc}</Text>}>
          <Timeline
            style={{ marginTop: 8 }}
            items={workflow.steps.map((s) => ({
              children: (
                <div>
                  <Space size={8} wrap>
                    <Text strong style={{ fontSize: 13 }}>{s.seq}. {s.name}</Text>
                    <Tag style={{ margin: 0, fontSize: 11 }} color={s.kind === 'human' ? 'orange' : 'geekblue'}>
                      {kindLabelOf(s.kind, settings?.llm.agentMode)}
                    </Tag>
                  </Space>
                  {s.deps.map((d, i) => (
                    <div key={i} style={{ fontSize: 12, color: '#6b7280', marginTop: 2 }}>
                      <LinkOutlined style={{ marginRight: 4 }} />{d.data}
                      <ToolOutlined style={{ margin: '0 4px 0 12px' }} />{d.tool}
                    </div>
                  ))}
                </div>
              ),
            }))}
          />
        </Card>
      )}

      {/* 目标数据集：字段定义归属数据集，选定后自动带出（驱动 AI 提取与写入校验） */}
      <Card size="small" title={<Space><DatabaseOutlined style={{ color: '#4F46E5' }} /><Text strong>目标数据集</Text></Space>}
        style={{ marginBottom: 16 }}>
        <Space direction="vertical" size={10} style={{ width: '100%' }}>
          <Radio.Group
            value={mode}
            onChange={(e) => setMode(e.target.value)}
            optionType="button"
            options={[
              { value: 'existing', label: '写入已有数据集', disabled: datasets.length === 0 },
              { value: 'create', label: '新建数据集' },
            ]}
          />
          {mode === 'existing' ? (
            <Select
              style={{ width: 360 }}
              placeholder="选择目标数据集（字段定义将自动带出）"
              value={datasetId}
              onChange={(v) => setDatasetId(v)}
              options={datasets.map((d) => ({ value: d.ID, label: `${d.Name}（${d.ItemCount} 条）` }))}
            />
          ) : (
            <Space wrap>
              <Input
                style={{ width: 260 }}
                placeholder="新数据集命名（如：订单中心需求集 v1）"
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
                maxLength={60}
              />
              <Select
                style={{ width: 220 }}
                placeholder="字段模板"
                value={effTemplateType}
                onChange={(v) => setTemplateType(v)}
                options={templates.map((t) => ({ value: t.type, label: `${t.label}（${t.fields.length} 字段）` }))}
              />
            </Space>
          )}
          {schema && (
            <div>
              <Text type="secondary" style={{ fontSize: 12 }}>
                字段定义（{schema.fields.length} 个，创建任务即按此口径提取与写入；可在数据集详情页受控调整）：
              </Text>
              <Table
                size="small" style={{ marginTop: 6 }}
                pagination={false}
                dataSource={schema.fields}
                rowKey="key"
                columns={[
                  { title: '字段', dataIndex: 'label', width: 140,
                    render: (v: string, f: FieldSpec) => (
                      <Space size={4}>{v}{f.required && <Tag color="red" style={{ margin: 0, fontSize: 10 }}>必填</Tag>}</Space>
                  ) },
                  { title: 'key', dataIndex: 'key', width: 180,
                    render: (v: string) => <Text code style={{ fontSize: 12 }}>{v}</Text> },
                  { title: '类型', dataIndex: 'type', width: 90 },
                  { title: '说明', dataIndex: 'prompt', ellipsis: true,
                    render: (v?: string) => <Text type="secondary" style={{ fontSize: 12 }}>{v}</Text> },
                ]}
              />
            </div>
          )}
          {mode === 'existing' && datasetId && !schema && (
            <Alert type="warning" showIcon message="该数据集缺少字段定义，无法绑定（请在数据集详情页补齐后再创建任务）" />
          )}
        </Space>
      </Card>

      <Dragger
        name="file"
        accept=".docx,.pdf,.md,.txt"
        showUploadList={false}
        disabled={parsing || !datasetReady}
        customRequest={() => {}}
        beforeUpload={handleFile}
      >
        <p className="ant-upload-drag-icon"><InboxOutlined /></p>
        <p className="ant-upload-text">{parsing ? '正在创建任务并解析…' : datasetReady ? '拖拽或点击上传需求文档' : '请先选定上方目标数据集'}</p>
        <p className="ant-upload-hint">
          支持 docx / pdf（MinerU 云端解析）/ markdown / 纯文本 · 上传即创建任务，解析与分析进度全程可跟踪、可暂停继续
        </p>
      </Dragger>

      <div style={{ marginTop: 24 }}>
        <Text type="secondary">额外要求（可选，将注入分析提示词，优先级高于默认约定）</Text>
        <Input.TextArea
          rows={3}
          maxLength={2000}
          showCount
          value={specialRequirements}
          onChange={(e) => setSpecialRequirements(e.target.value)}
          placeholder="例：每个需求必须给出验收标准；工时按 2 人日上限评估；忽略附录里的术语表…"
          style={{ marginTop: 8 }}
        />
      </div>
    </Card>
  )
}
