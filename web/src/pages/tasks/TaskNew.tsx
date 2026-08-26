import { useState } from 'react'
import { Card, Typography, Alert, App, Timeline, Tag, Space } from 'antd'
import { InboxOutlined, LinkOutlined, ToolOutlined } from '@ant-design/icons'
import { Upload as AntUpload, Input } from 'antd'
import { useNavigate } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { api } from '../../api/client'
import { tasksApi } from '../../api/tasks'
import type { SettingsView, StepKind, Workflow } from '../../api/types'

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

/** 新建需求导入任务：工作流预览（步骤链 + 依赖）→ 上传文档 → 创建任务 → 触发解析 → 跳转详情页 */
export default function TaskNew() {
  const navigate = useNavigate()
  const { message } = App.useApp()
  const [parsing, setParsing] = useState(false)
  const [specialRequirements, setSpecialRequirements] = useState('')
  const { data: settings } = useQuery({ queryKey: ['settings'], queryFn: () => api.get<SettingsView>('/api/settings') })
  const { data: workflows } = useQuery({ queryKey: ['workflows'], queryFn: () => tasksApi.workflows() })
  const workflow: Workflow | undefined = workflows?.workflows.find((w) => w.type === 'requirement_import')

  const handleFile = async (file: File) => {
    setParsing(true)
    try {
      const { task } = await tasksApi.create('requirement_import', file.name)
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
      <Dragger
        name="file"
        accept=".docx,.pdf,.md,.txt"
        showUploadList={false}
        disabled={parsing}
        customRequest={() => {}}
        beforeUpload={handleFile}
      >
        <p className="ant-upload-drag-icon"><InboxOutlined /></p>
        <p className="ant-upload-text">{parsing ? '正在创建任务并解析…' : '拖拽或点击上传需求文档'}</p>
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
