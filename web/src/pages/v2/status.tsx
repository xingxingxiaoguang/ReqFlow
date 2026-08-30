import { Tag } from 'antd'
import type { V2StepStatus, V2TaskStatus, ValidationRecordStatus } from '../../api/v2/types'

const taskStatus: Record<V2TaskStatus, { label: string; color: string }> = {
  pending: { label: '待启动', color: 'default' },
  running: { label: '运行中', color: 'processing' },
  pausing: { label: '暂停中', color: 'gold' },
  awaiting: { label: '待审核', color: 'warning' },
  paused: { label: '已暂停', color: 'gold' },
  succeeded: { label: '已完成', color: 'success' },
  failed: { label: '失败', color: 'error' },
}

const stepStatus: Record<V2StepStatus, { label: string; color: string }> = {
  pending: { label: '待执行', color: 'default' },
  queued: { label: '排队中', color: 'cyan' },
  running: { label: '执行中', color: 'processing' },
  awaiting: { label: '待审核', color: 'warning' },
  paused: { label: '已暂停', color: 'gold' },
  succeeded: { label: '成功', color: 'success' },
  failed: { label: '失败', color: 'error' },
  skipped: { label: '已跳过', color: 'default' },
}

const validationStatus: Record<ValidationRecordStatus, { label: string; color: string }> = {
  valid: { label: '有效', color: 'success' },
  warning: { label: '有警告', color: 'warning' },
  invalid: { label: '无效', color: 'error' },
  duplicate_in_batch: { label: '批内重复', color: 'magenta' },
  conflict_existing_key: { label: '已有主键冲突', color: 'volcano' },
}

export function TaskStatusTag({ status }: { status: V2TaskStatus }) {
  const value = taskStatus[status] ?? { label: status, color: 'default' }
  return <Tag color={value.color}>{value.label}</Tag>
}

export function StepStatusTag({ status }: { status: V2StepStatus }) {
  const value = stepStatus[status] ?? { label: status, color: 'default' }
  return <Tag color={value.color}>{value.label}</Tag>
}

export function ValidationStatusTag({ status }: { status: ValidationRecordStatus }) {
  const value = validationStatus[status] ?? { label: status, color: 'default' }
  return <Tag color={value.color}>{value.label}</Tag>
}

export const STEP_KIND_LABEL: Record<string, string> = {
  'source.parse': '文档解析',
  'llm.extract': '结构化抽取',
  'data.transform': '确定性转换',
  'data.validate': '数据校验',
  'human.review': '人工审核',
  'data.publish': '原子发布',
  'data.query_derive': 'Query Dataset 增量派生',
  'retrieval.build': '检索构建',
  'agent.analyze': '智能分析',
  'artifact.render': '产物渲染',
  'graph.build': '图谱构建',
}
