import { Card, Button, Space, Typography, Segmented, Input, App } from 'antd'
import { ArrowLeftOutlined, RocketOutlined, EyeOutlined, EditOutlined } from '@ant-design/icons'
import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { useImportWizard } from '../../stores/importWizard'

const { Text, Paragraph } = Typography

/** 阶段 2：解析确认门 —— 进入 LLM 前修正解析噪音（花钱前的最后一道闸） */
export default function Review() {
  const navigate = useNavigate()
  const { message, modal } = App.useApp()
  const { fileName, parsedText, setField, startAnalyze, reset } = useImportWizard()
  const [mode, setMode] = useState<string | number>('preview')
  const [starting, setStarting] = useState(false)

  if (!parsedText) {
    return (
      <Card>
        <Paragraph>还没有解析中的文档，请先上传。</Paragraph>
        <Button type="primary" onClick={() => navigate('/import/upload')}>去上传</Button>
      </Card>
    )
  }

  const onStart = async () => {
    setStarting(true)
    try {
      await startAnalyze()
      navigate('/import/analyzing')
    } catch (e) {
      message.error((e as Error).message || 'AI 分析失败')
    } finally {
      setStarting(false)
    }
  }

  const onCancel = () => {
    modal.confirm({
      title: '放弃本次分析？',
      content: '已解析的全文与额外要求将被清空。',
      okText: '放弃',
      okButtonProps: { danger: true },
      cancelText: '继续编辑',
      onOk: () => {
        reset()
        navigate('/import/upload')
      },
    })
  }

  return (
    <Card
      title={<Space><Text strong>{fileName}</Text><Text type="secondary">· 解析结果确认</Text></Space>}
      extra={
        <Space>
          <Button icon={<ArrowLeftOutlined />} onClick={() => navigate('/import/upload')}>重新上传</Button>
          <Button danger onClick={onCancel}>取消</Button>
          <Button type="primary" icon={<RocketOutlined />} loading={starting} onClick={onStart}>
            开始 AI 分析
          </Button>
        </Space>
      }
    >
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
            onChange={(e) => setField({ parsedText: e.target.value })}
            style={{ fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace', fontSize: 13 }}
            placeholder="可直接修正解析结果（如删掉水印、目录、页眉页脚等噪音）"
          />
        )}
        <Text type="secondary">
          确认或修正全文后开始分析；AI 将识别项目分组、拆分工作项、评估优先级与工时，并给出解决方案建议。
        </Text>
      </Space>
    </Card>
  )
}
