import { useState } from 'react'
import { Card, Button, Space, Typography, Segmented, Input, App } from 'antd'
import { RocketOutlined, SaveOutlined, EyeOutlined, EditOutlined } from '@ant-design/icons'
import { useQueryClient } from '@tanstack/react-query'
import { tasksApi } from '../../../api/tasks'
import type { Task, TaskInput } from '../../../api/types'

const { Text } = Typography

/** 确认解析门：预览/编辑解析全文 + 附加要求 → 保存 → 开始 AI 分析（失败可重试） */
export default function ConfirmParsePanel({ task, input }: { task: Task; input: TaskInput }) {
  const qc = useQueryClient()
  const { message } = App.useApp()
  const [mode, setMode] = useState<string | number>('preview')
  const [parsedText, setParsedText] = useState(input.parsed_text ?? '')
  const [special, setSpecial] = useState(input.special_requirements ?? '')
  const [starting, setStarting] = useState(false)

  const dirty = parsedText !== (input.parsed_text ?? '') || special !== (input.special_requirements ?? '')

  const onSave = async () => {
    try {
      await tasksApi.patch(task.ID, { parsed_text: parsedText, special_requirements: special })
      qc.invalidateQueries({ queryKey: ['task', task.ID] })
      message.success('已保存')
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const onStart = async () => {
    setStarting(true)
    try {
      if (dirty) await onSave()
      await tasksApi.triggerAnalyze(task.ID)
      qc.invalidateQueries({ queryKey: ['task', task.ID] })
    } catch (e) {
      message.error((e as Error).message || 'AI 分析启动失败')
    } finally {
      setStarting(false)
    }
  }

  return (
    <Card
      title={<Space><Text strong>{task.Title}</Text><Text type="secondary">· 解析结果确认</Text></Space>}
      extra={
        <Space>
          <Button icon={<SaveOutlined />} disabled={!dirty} onClick={onSave}>保存修改</Button>
          <Button type="primary" icon={<RocketOutlined />} loading={starting} onClick={onStart}>
            {task.ErrorMessage ? '重新开始 AI 分析' : '开始 AI 分析'}
          </Button>
        </Space>
      }
    >
      {task.ErrorMessage && (
        <div style={{ marginBottom: 12, padding: '8px 12px', borderRadius: 8, background: '#fef2f2', border: '1px solid #fecaca', color: '#b91c1c', fontSize: 13 }}>
          上次分析失败：{task.ErrorMessage}（可修正后重试）
        </div>
      )}
      <Space direction="vertical" style={{ width: '100%' }} size={12}>
        <Segmented
          value={mode}
          onChange={setMode}
          options={[
            { label: '预览', value: 'preview', icon: <EyeOutlined /> },
            { label: '编辑', value: 'edit', icon: <EditOutlined /> },
          ]}
        />
        {mode === 'preview' ? (
          <pre
            style={{
              margin: 0, padding: 16, background: '#0f172a', color: '#dbe3f4',
              borderRadius: 10, minHeight: 340, maxHeight: 560, overflow: 'auto',
              fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', fontSize: 13, lineHeight: 1.7,
            }}
          >
            {parsedText}
          </pre>
        ) : (
          <Input.TextArea
            rows={16}
            value={parsedText}
            onChange={(e) => setParsedText(e.target.value)}
            style={{ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', fontSize: 13 }}
            placeholder="可直接修正解析结果（如删掉水印、目录、页眉页脚等噪音）"
          />
        )}
        <div>
          <Text type="secondary">额外要求（可选，将注入分析提示词，优先级高于默认约定）</Text>
          <Input.TextArea
            rows={3}
            maxLength={2000}
            showCount
            value={special}
            onChange={(e) => setSpecial(e.target.value)}
            placeholder="例：每个需求必须给出验收标准；工时按 2 人日上限评估；忽略附录里的术语表…"
            style={{ marginTop: 8 }}
          />
        </div>
        <Text type="secondary">
          确认或修正全文后开始分析；AI 将识别项目分组、拆分工作项、评估优先级与工时，并给出解决方案建议。
        </Text>
      </Space>
    </Card>
  )
}
