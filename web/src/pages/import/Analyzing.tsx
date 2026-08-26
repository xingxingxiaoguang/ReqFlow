import { Card, Tag, Typography, Space, Tooltip } from 'antd'
import {
  LoadingOutlined, BulbOutlined, FileTextOutlined, ToolOutlined,
  CheckCircleOutlined, CloseCircleOutlined,
} from '@ant-design/icons'
import { useEffect, useRef } from 'react'
import { useImportWizard } from '../../stores/importWizard'

const { Text } = Typography;

/** 工具名 → 人读标签（与后端 tools.Build 清单对齐） */
const TOOL_LABELS: Record<string, string> = {
  search_projects: '搜索项目',
  search_work_items: '查重搜索',
  get_work_item_types: '核实类型',
  get_project_members: '核实成员',
  list_recent_work_items: '查看近期工作项',
}

/** 阶段 3：AI 流式分析面板 —— 思考/正文双区实时滚动 + agent 工具轨迹 + 实时计数 */
export default function Analyzing() {
  const { analyzing, analyzeMessage, elapsedSec, phase, thinkingTail, answerTail, answerCount, toolTrace } = useImportWizard()
  const thinkRef = useRef<HTMLDivElement>(null)
  const answerRef = useRef<HTMLDivElement>(null)
  const traceRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    thinkRef.current?.scrollTo({ top: thinkRef.current.scrollHeight })
  }, [thinkingTail])
  useEffect(() => {
    answerRef.current?.scrollTo({ top: answerRef.current.scrollHeight })
  }, [answerTail])
  useEffect(() => {
    traceRef.current?.scrollTo({ top: traceRef.current.scrollHeight })
  }, [toolTrace])

  if (!analyzing) return null

  const running = toolTrace.filter((t) => t.status === 'running').length

  return (
    <Card
      title={
        <Space>
          <LoadingOutlined spin style={{ color: '#4F46E5' }} />
          <Text strong>AI 分析中</Text>
          <Tag color="geekblue">{elapsedSec > 0 ? `${elapsedSec}s` : '启动中'}</Tag>
          {answerCount > 0 && <Tag color="green">已生成 {answerCount} 项</Tag>}
          {toolTrace.length > 0 && (
            <Tag color="purple">
              查证 {toolTrace.length} 次{running > 0 ? ` · ${running} 进行中` : ''}
            </Tag>
          )}
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

      {toolTrace.length > 0 && (
        <div style={{ marginTop: 16 }}>
          <Space style={{ marginBottom: 8 }}>
            <ToolOutlined style={{ color: '#7c3aed' }} />
            <Text type="secondary" strong>工具查证轨迹</Text>
            <Text type="secondary" style={{ fontSize: 12 }}>AI 在出稿前自主核实的信息（只读查询）</Text>
          </Space>
          <div
            ref={traceRef}
            style={{
              maxHeight: 160, overflow: 'auto', padding: '10px 14px', borderRadius: 10,
              background: '#f5f3ff', border: '1px solid #ddd6fe',
            }}
          >
            {toolTrace.map((t, i) => (
              <div key={`${t.callId}-${i}`} style={{ display: 'flex', alignItems: 'baseline', gap: 8, padding: '3px 0', fontSize: 12.5 }}>
                {t.status === 'running' ? (
                  <LoadingOutlined spin style={{ color: '#7c3aed' }} />
                ) : t.status === 'error' ? (
                  <CloseCircleOutlined style={{ color: '#dc2626' }} />
                ) : (
                  <CheckCircleOutlined style={{ color: '#16a34a' }} />
                )}
                <Text strong style={{ flexShrink: 0 }}>{TOOL_LABELS[t.name] ?? t.name}</Text>
                {t.args && (
                  <Tooltip title={t.args}>
                    <Text code style={{ fontSize: 11, maxWidth: 260, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', display: 'inline-block', verticalAlign: 'bottom' }}>
                      {t.args}
                    </Text>
                  </Tooltip>
                )}
                {t.details && <Text type="secondary">{t.details}</Text>}
                {t.status === 'running' && <Text type="secondary" style={{ fontSize: 11 }}>执行中…</Text>}
              </div>
            ))}
          </div>
        </div>
      )}
    </Card>
  )
}
