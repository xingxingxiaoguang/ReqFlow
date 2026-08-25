import { Card, Tag, Typography, Space } from 'antd'
import { LoadingOutlined, BulbOutlined, FileTextOutlined } from '@ant-design/icons'
import { useEffect, useRef } from 'react'
import { useImportWizard } from '../../stores/importWizard'

const { Text } = Typography

/** 阶段 3：AI 流式分析面板 —— 思考/正文双区实时滚动 + 实时计数（complete 后自动进入 result） */
export default function Analyzing() {
  const { analyzing, analyzeMessage, elapsedSec, phase, thinkingTail, answerTail, answerCount } = useImportWizard()
  const thinkRef = useRef<HTMLDivElement>(null)
  const answerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    thinkRef.current?.scrollTo({ top: thinkRef.current.scrollHeight })
  }, [thinkingTail])
  useEffect(() => {
    answerRef.current?.scrollTo({ top: answerRef.current.scrollHeight })
  }, [answerTail])

  if (!analyzing) return null

  return (
    <Card
      title={
        <Space>
          <LoadingOutlined spin style={{ color: '#4F46E5' }} />
          <Text strong>AI 分析中</Text>
          <Tag color="geekblue">{elapsedSec > 0 ? `${elapsedSec}s` : '启动中'}</Tag>
          {answerCount > 0 && <Tag color="green">已生成 {answerCount} 项</Tag>}
        </Space>
      }
      extra={<Text type="secondary">{analyzeMessage}</Text>}
    >
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 16 }}>
        <div>
          <Space style={{ marginBottom: 8 }}>
            <BulbOutlined style={{ color: phase === 'thinking' ? '#f59e0b' : '#9aa1b5' }} />
            <Text type="secondary" strong>推理过程</Text>
            {phase === 'thinking' && <Tag color="orange" style={{ marginInlineEnd: 0 }}>思考中</Tag>}
          </Space>
          <div
            ref={thinkRef}
            style={{
              height: 380, overflow: 'auto', padding: 14, borderRadius: 10,
              background: '#fffbeb', border: '1px solid #fde68a',
              fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
              fontSize: 12.5, lineHeight: 1.7, color: '#78350f', whiteSpace: 'pre-wrap',
            }}
          >
            {thinkingTail || '（等待模型输出…）'}
          </div>
        </div>
        <div>
          <Space style={{ marginBottom: 8 }}>
            <FileTextOutlined style={{ color: phase === 'answer' ? '#4F46E5' : '#9aa1b5' }} />
            <Text type="secondary" strong>结构化输出</Text>
            {phase === 'answer' && <Tag color="geekblue" style={{ marginInlineEnd: 0 }}>生成中</Tag>}
          </Space>
          <div
            ref={answerRef}
            style={{
              height: 380, overflow: 'auto', padding: 14, borderRadius: 10,
              background: '#0f172a', border: '1px solid #1e293b',
              fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
              fontSize: 12.5, lineHeight: 1.7, color: '#a5f3fc', whiteSpace: 'pre-wrap',
            }}
          >
            {answerTail || '（正文输出尚未开始…）'}
          </div>
        </div>
      </div>
    </Card>
  )
}
