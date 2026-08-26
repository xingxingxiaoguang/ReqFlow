import { useEffect, useRef, useState } from 'react'
import { Card, Tag, Typography, Space, Tooltip, Button, App, Modal, Input, Radio, Alert } from 'antd'
import {
  LoadingOutlined, BulbOutlined, FileTextOutlined, ToolOutlined,
  CheckCircleOutlined, CloseCircleOutlined, PlayCircleOutlined, QuestionCircleOutlined,
} from '@ant-design/icons'
import { useQueryClient } from '@tanstack/react-query'
import { tasksApi } from '../../../api/tasks'
import type { Task } from '../../../api/types'
import type { TaskStream } from '../../../hooks/useTaskEvents'

const { Text } = Typography

/** 工具名 → 人读标签（与后端 tools.BuildForRun 清单对齐） */
const TOOL_LABELS: Record<string, string> = {
  read_document: '读取文档',
  search_document: '检索文档',
  write_work_items: '写入草稿',
  ask_human: '人工确认',
}

/** AI 分析工作区：思考/正文双区实时滚动 + agent 工具轨迹 + 人工交互弹窗（暂停时遮罩可继续） */
export default function AnalysisPane({ task, stream }: { task: Task; stream: TaskStream }) {
  const qc = useQueryClient()
  const { message } = App.useApp()
  const { thinkingTail, answerTail, answerCount, phase, elapsedSec, analyzeMessage, toolTrace, dialog } = stream
  const thinkRef = useRef<HTMLDivElement>(null)
  const answerRef = useRef<HTMLDivElement>(null)
  const traceRef = useRef<HTMLDivElement>(null)

  // 人工交互弹窗：受控可见（可关闭，重开按钮防丢），提交后等服务端 close 事件清 stream.dialog
  const [modalOpen, setModalOpen] = useState(false)
  const [answer, setAnswer] = useState('')
  const [optionIdx, setOptionIdx] = useState<number>(0)
  const [submitting, setSubmitting] = useState(false)

  useEffect(() => {
    if (dialog) {
      setModalOpen(true)
      setAnswer('')
      setOptionIdx(0)
    }
  }, [dialog?.callId])

  useEffect(() => {
    thinkRef.current?.scrollTo({ top: thinkRef.current.scrollHeight })
  }, [thinkingTail])
  useEffect(() => {
    answerRef.current?.scrollTo({ top: answerRef.current.scrollHeight })
  }, [answerTail])
  useEffect(() => {
    traceRef.current?.scrollTo({ top: traceRef.current.scrollHeight })
  }, [toolTrace])

  const paused = task.Status === 'paused'
  const running = toolTrace.filter((t) => t.status === 'running').length

  const onResume = async () => {
    try {
      await tasksApi.resume(task.ID)
      qc.invalidateQueries({ queryKey: ['task', task.ID] })
    } catch (e) {
      message.error((e as Error).message)
    }
  }

  const onAnswerDialog = async () => {
    if (!dialog) return
    const final = dialog.options?.length ? dialog.options[optionIdx] ?? '' : answer
    if (!final.trim()) {
      message.warning('请先填写回答')
      return
    }
    setSubmitting(true)
    try {
      await tasksApi.answerDialog(task.ID, dialog.callId, final.trim())
      setModalOpen(false) // stream.dialog 由服务端 close 事件清除
    } catch (e) {
      message.error((e as Error).message)
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Card
      title={
        <Space>
          {paused ? <PlayCircleOutlined style={{ color: '#f59e0b' }} /> : <LoadingOutlined spin style={{ color: '#4F46E5' }} />}
          <Text strong>{paused ? '分析已暂停' : 'AI 分析中'}</Text>
          <Tag color="geekblue">{elapsedSec > 0 ? `${elapsedSec}s` : '启动中'}</Tag>
          {answerCount > 0 && <Tag color="green">已生成 {answerCount} 项</Tag>}
          {toolTrace.length > 0 && (
            <Tag color="purple">
              工具调用 {toolTrace.length} 次{running > 0 ? ` · ${running} 进行中` : ''}
            </Tag>
          )}
        </Space>
      }
      extra={
        paused ? (
          <Space>
            <Text type="secondary">{analyzeMessage}</Text>
            <Button type="primary" size="small" icon={<PlayCircleOutlined />} onClick={onResume}>继续分析</Button>
          </Space>
        ) : (
          <Text type="secondary">{analyzeMessage}</Text>
        )
      }
    >
      {/* 人工交互：弹窗可关但保留入口，防止误关后无法作答（任务在等待回答期间阻塞） */}
      {dialog && !modalOpen && (
        <Alert
          style={{ marginBottom: 16 }}
          type="warning"
          showIcon
          icon={<QuestionCircleOutlined />}
          message="AI 正在等待你的回答"
          description={dialog.question}
          action={<Button size="small" type="primary" onClick={() => setModalOpen(true)}>回答问题</Button>}
        />
      )}
      <Modal
        open={modalOpen && !!dialog}
        title={<Space><QuestionCircleOutlined style={{ color: '#f59e0b' }} /><span>AI 需要你的输入</span></Space>}
        okText="提交回答"
        cancelText="稍后回答"
        okButtonProps={{ loading: submitting }}
        onOk={onAnswerDialog}
        onCancel={() => setModalOpen(false)}
        maskClosable={false}
      >
        <Typography.Paragraph style={{ whiteSpace: 'pre-wrap', marginTop: 4 }}>{dialog?.question}</Typography.Paragraph>
        {dialog?.options?.length ? (
          <Radio.Group
            value={optionIdx}
            onChange={(e) => setOptionIdx(e.target.value as number)}
            style={{ display: 'flex', flexDirection: 'column', gap: 8 }}
          >
            {dialog.options.map((opt, i) => (
              <Radio key={i} value={i}>{opt}</Radio>
            ))}
          </Radio.Group>
        ) : (
          <Input.TextArea
            rows={4}
            value={answer}
            onChange={(e) => setAnswer(e.target.value)}
            placeholder="输入你的回答（AI 将基于它继续分析）"
            autoFocus
          />
        )}
      </Modal>

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
            <Text type="secondary" strong>分析陈述</Text>
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
            {answerTail || '（正文输出尚未开始；草稿经「写入草稿」工具分批产出，见下方轨迹）'}
          </div>
        </div>
      </div>

      {toolTrace.length > 0 && (
        <div style={{ marginTop: 16 }}>
          <Space style={{ marginBottom: 8 }}>
            <ToolOutlined style={{ color: '#7c3aed' }} />
            <Text type="secondary" strong>工具调用轨迹</Text>
            <Text type="secondary" style={{ fontSize: 12 }}>AI 分析过程中的读取/检索/写入/交互记录</Text>
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
