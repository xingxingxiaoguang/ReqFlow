import { useMemo, useState } from 'react'
import {
  Alert, App, Badge, Button, Card, Col, Descriptions, Divider, Empty, Result, Row,
  Space, Spin, Steps, Table, Tag, Typography,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import {
  ArrowLeftOutlined, PauseCircleOutlined, PlayCircleOutlined, ReloadOutlined,
  RocketOutlined, InboxOutlined, DownloadOutlined,
} from '@ant-design/icons'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from 'react-router-dom'
import { v2TasksApi } from '../../api/v2/tasks'
import { v2CatalogApi } from '../../api/v2/catalog'
import type {
  ApprovedRecordSet, RecordReviewDecision, V2Resource, V2StepRun, V2TaskSnapshot,
} from '../../api/v2/types'
import { useV2TaskEvents } from '../../hooks/useV2TaskEvents'
import ReviewWorkspace from './ReviewWorkspace'
import { STEP_KIND_LABEL, StepStatusTag, TaskStatusTag } from './status'

const { Text, Title, Paragraph } = Typography

export default function V2TaskDetail() {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { message } = App.useApp()
  const [acting, setActing] = useState<string>()
  const streamStatus = useV2TaskEvents(id)
  const snapshotQuery = useQuery({
    queryKey: ['v2-task', id],
    queryFn: () => v2TasksApi.get(id!),
    enabled: Boolean(id),
  })
  const snapshot = snapshotQuery.data
  const validationRef = useMemo(() => findResource(snapshot, 'validation_results'), [snapshot])
  const approvedRef = useMemo(() => findResource(snapshot, 'approved_records'), [snapshot])
  const batchRef = useMemo(() => findResource(snapshot, 'dataset_batch'), [snapshot])
  const analysisRef = useMemo(() => findResource(snapshot, 'analysis_result'), [snapshot])
  const artifactRef = useMemo(() => findResource(snapshot, 'artifact'), [snapshot])
  const reviewStep = snapshot?.steps?.find((step) => step.kind === 'human.review' && step.status === 'awaiting') ?? snapshot?.steps?.find((step) => step.kind === 'human.review')

  const validationQuery = useQuery({
    queryKey: ['v2-validation-set', validationRef?.resource_id],
    queryFn: () => v2TasksApi.getValidationSet(validationRef!.resource_id),
    enabled: Boolean(validationRef?.resource_id),
  })
  const validation = validationQuery.data?.validation_result_set
  const schemaQuery = useQuery({
    queryKey: ['v2-schema', validation?.target_schema_id],
    queryFn: () => v2TasksApi.getSchema(validation!.target_schema_id),
    enabled: Boolean(validation?.target_schema_id),
  })
  const datasetQuery = useQuery({
    queryKey: ['v2-dataset', validation?.target_dataset_id],
    queryFn: () => v2TasksApi.getDataset(validation!.target_dataset_id),
    enabled: Boolean(validation?.target_dataset_id),
  })
  const approvedQuery = useQuery({
    queryKey: ['v2-approved-set', approvedRef?.resource_id],
    queryFn: () => v2TasksApi.getApprovedSet(approvedRef!.resource_id),
    enabled: Boolean(approvedRef?.resource_id),
  })
  const analysisQuery = useQuery({
    queryKey: ['v2-analysis-result', analysisRef?.resource_id],
    queryFn: () => v2CatalogApi.getAnalysisResult(analysisRef!.resource_id),
    enabled: Boolean(analysisRef?.resource_id),
  })

  const applySnapshot = (next: V2TaskSnapshot) => {
    queryClient.setQueryData(['v2-task', id], next)
    queryClient.invalidateQueries({ queryKey: ['v2-tasks'] })
  }

  const transition = async (name: string, action: () => Promise<V2TaskSnapshot>) => {
    setActing(name)
    try {
      applySnapshot(await action())
      message.success(name === 'pause' ? '已请求暂停' : name === 'retry' ? '步骤已进入重试调度' : '任务状态已更新')
    } catch (error) {
      message.error((error as Error).message)
    } finally {
      setActing(undefined)
    }
  }

  const submitReview = async (input: Parameters<typeof v2TasksApi.approve>[2]) => {
    if (!id || !reviewStep) return
    setActing('review')
    try {
      applySnapshot(await v2TasksApi.approve(id, reviewStep.step_id, input))
      await Promise.all([
        queryClient.invalidateQueries({ queryKey: ['v2-approved-set'] }),
        queryClient.invalidateQueries({ queryKey: ['v2-tasks'] }),
      ])
      message.success('审核已固化，后续发布已进入调度')
    } catch (error) {
      message.error((error as Error).message)
      throw error
    } finally {
      setActing(undefined)
    }
  }

  const approveAnalysis = async () => {
    if (!id || !reviewStep) return
    setActing('review')
    try {
      applySnapshot(await v2TasksApi.approveResource(id, reviewStep.step_id))
      message.success('分析结果已放行，后续发布与制品生成已进入调度')
    } catch (error) { message.error((error as Error).message) } finally { setActing(undefined) }
  }

  const archiveTask = async () => {
    setActing('archive')
    try { await v2TasksApi.archive(task.id); message.success('任务已归档'); navigate('/tasks') } catch (error) { message.error((error as Error).message) } finally { setActing(undefined) }
  }

  if (snapshotQuery.isLoading) return <Card><Spin tip="读取 V2 Task 快照…" /></Card>
  if (!snapshot || snapshotQuery.error) {
    return <Result status="404" title="V2 任务不存在" subTitle={(snapshotQuery.error as Error | undefined)?.message} extra={<Button onClick={() => navigate('/tasks')}>返回任务列表</Button>} />
  }

  const task = snapshot.task
  const steps = snapshot.steps ?? []
  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Card>
        <Row justify="space-between" align="middle" gutter={[16, 12]}>
          <Col>
            <Space direction="vertical" size={3}>
              <Space>
                <Button type="text" icon={<ArrowLeftOutlined />} onClick={() => navigate('/tasks')} />
                <Title level={3} style={{ margin: 0 }}>{task.title}</Title>
                <TaskStatusTag status={task.status} />
                <Tag color="purple">{task.type}</Tag>
              </Space>
              <Space split={<Divider type="vertical" />}>
                <Text type="secondary">Task {task.id}</Text>
                <Text type="secondary">Definition {task.definition_id}</Text>
                <Badge status={streamStatus === 'connected' ? 'success' : 'processing'} text={streamStatus === 'connected' ? '实时快照已连接' : '事件流重连中'} />
              </Space>
            </Space>
          </Col>
          <Col>
            <Space>
              <Button icon={<ReloadOutlined />} onClick={() => snapshotQuery.refetch()}>刷新</Button>
              {task.status === 'pending' && <Button type="primary" loading={acting === 'start'} icon={<RocketOutlined />} onClick={() => transition('start', () => v2TasksApi.start(task.id))}>启动</Button>}
              {task.status === 'running' && <Button loading={acting === 'pause'} icon={<PauseCircleOutlined />} onClick={() => transition('pause', () => v2TasksApi.pause(task.id))}>暂停</Button>}
              {task.status === 'paused' && <Button type="primary" loading={acting === 'resume'} icon={<PlayCircleOutlined />} onClick={() => transition('resume', () => v2TasksApi.resume(task.id))}>继续</Button>}
              {['succeeded', 'failed'].includes(task.status) && <Button danger loading={acting === 'archive'} icon={<InboxOutlined />} onClick={archiveTask}>归档</Button>}
            </Space>
          </Col>
        </Row>
        {task.error_message && <Alert type="error" showIcon message="任务错误" description={task.error_message} style={{ marginTop: 16 }} />}
      </Card>

      <Card title="执行拓扑与状态">
        <Steps
          responsive
          current={Math.max(0, steps.findIndex((step) => ['running', 'queued', 'awaiting', 'failed', 'paused'].includes(step.status)))}
          items={steps.map((step) => ({
            title: step.name || step.step_id,
            description: <Space direction="vertical" size={0}><Text type="secondary">{STEP_KIND_LABEL[step.kind] ?? step.kind}</Text><StepStatusTag status={step.status} /></Space>,
            status: antStepStatus(step.status),
          }))}
        />
        <Divider />
        <StepRunTable
          steps={steps}
          outputs={snapshot.step_outputs}
          acting={acting}
          onRetry={(step) => transition('retry', () => v2TasksApi.retry(task.id, step.step_id))}
        />
      </Card>

      {task.status === 'awaiting' && validation && reviewStep ? (
        <ReviewWorkspace
          key={validation.id}
          validation={validation}
          schema={schemaQuery.data?.schema}
          dataset={datasetQuery.data?.dataset}
          allowEdit={reviewStep.config?.allow_edit !== false}
          submitting={acting === 'review'}
          onSubmit={submitReview}
        />
      ) : task.status === 'awaiting' && analysisQuery.data?.analysis_result && reviewStep ? (
        <Card title="人工确认结构化分析" extra={<Button type="primary" loading={acting === 'review'} onClick={approveAnalysis}>确认并继续</Button>}>
          <Alert type="info" showIcon message="这是资源放行 Gate" description="确认后将原 AnalysisResult 原样绑定到审核输出；浏览器不能提交或替换资源 ID。" style={{ marginBottom: 16 }} />
          <pre style={{ whiteSpace: 'pre-wrap', background: '#0f172a', color: '#e2e8f0', padding: 18, borderRadius: 8 }}>{JSON.stringify(analysisQuery.data.analysis_result.output, null, 2)}</pre>
        </Card>
      ) : task.status === 'awaiting' ? (
        <Card><Spin tip={validationQuery.isLoading ? '读取校验结果…' : '准备审核上下文…'} /></Card>
      ) : null}

      {approvedQuery.data?.approved_record_set && <ApprovedSummary approved={approvedQuery.data.approved_record_set} />}

      {batchRef && (
        <Alert
          type="success"
          showIcon
          message="Dataset Batch 已发布"
          description={<Space split={<Divider type="vertical" />}><Text code>{batchRef.resource_id}</Text><Text>目标数据写入已由原子事务提交，可通过 Batch 追溯本次发布。</Text></Space>}
        />
      )}

      {artifactRef && <Alert type="success" showIcon message="业务制品已生成" description={<Space><Text code>{artifactRef.resource_id}</Text><Button type="link" icon={<DownloadOutlined />} href={`/api/v2/artifacts/${artifactRef.resource_id}/content`}>下载制品</Button></Space>} />}

      <Row gutter={[16, 16]}>
        <Col xs={24} xl={12}><ResourceCard title="任务输入" resources={snapshot.inputs ?? []} /></Col>
        <Col xs={24} xl={12}><ResourceCard title="任务输出" resources={snapshot.outputs ?? []} /></Col>
      </Row>
    </Space>
  )
}

function findResource(snapshot: V2TaskSnapshot | undefined, resourceType: string): V2Resource | undefined {
  if (!snapshot) return undefined
  const all = [...(snapshot.outputs ?? []), ...Object.values(snapshot.step_outputs ?? {}).flat()]
  for (let index = all.length - 1; index >= 0; index--) {
    if (all[index].resource_type === resourceType) return all[index]
  }
  return undefined
}

function antStepStatus(status: V2StepRun['status']): 'wait' | 'process' | 'finish' | 'error' {
  if (status === 'succeeded' || status === 'skipped') return 'finish'
  if (status === 'failed') return 'error'
  if (status === 'running' || status === 'queued' || status === 'awaiting' || status === 'paused') return 'process'
  return 'wait'
}

function StepRunTable({
  steps, outputs, acting, onRetry,
}: {
  steps: V2StepRun[]
  outputs: Record<string, V2Resource[]>
  acting?: string
  onRetry: (step: V2StepRun) => void
}) {
  const columns: ColumnsType<V2StepRun> = [
    { title: '#', dataIndex: 'ordinal', width: 55 },
    { title: '步骤', render: (_, step) => <Space direction="vertical" size={0}><Text strong>{step.name || step.step_id}</Text><Text type="secondary">{step.step_id}</Text></Space> },
    { title: '执行器', dataIndex: 'kind', width: 150, render: (value: string) => STEP_KIND_LABEL[value] ?? value },
    { title: '状态', dataIndex: 'status', width: 105, render: (value) => <StepStatusTag status={value} /> },
    { title: '尝试', dataIndex: 'attempt', width: 70, align: 'center' },
    {
      title: '进度 / 错误', width: 300,
      render: (_, step) => step.error_message
        ? <Text type="danger">{step.error_code && `[${step.error_code}] `}{step.error_message}</Text>
        : <Text type="secondary">{step.progress && Object.keys(step.progress).length ? JSON.stringify(step.progress) : '—'}</Text>,
    },
    {
      title: '产物', width: 150,
      render: (_, step) => <Space direction="vertical" size={2}>{(outputs[step.step_id] ?? []).map((resource) => <Tag key={`${resource.port_name}-${resource.resource_id}`} color="blue">{resource.port_name}: {resource.resource_type}</Tag>)}</Space>,
    },
    {
      title: '操作', width: 90,
      render: (_, step) => step.status === 'failed' ? <Button size="small" loading={acting === 'retry'} onClick={() => onRetry(step)}>重试</Button> : null,
    },
  ]
  return <Table<V2StepRun> rowKey="id" size="small" pagination={false} dataSource={steps} columns={columns} scroll={{ x: 1100 }} />
}

function ResourceCard({ title, resources }: { title: string; resources: V2Resource[] }) {
  return (
    <Card title={title} size="small">
      {resources.length === 0 ? <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="暂无资源" /> : (
        <Descriptions size="small" column={1} items={resources.map((resource) => ({
          key: `${resource.port_name}-${resource.resource_id}`,
          label: <Space><Text strong>{resource.port_name}</Text><Tag>{resource.resource_type}</Tag></Space>,
          children: <Space direction="vertical" size={2}><Text code copyable>{resource.resource_id}</Text>{resource.boundary && Object.keys(resource.boundary).length > 0 && <Text type="secondary">边界 {JSON.stringify(resource.boundary)}</Text>}</Space>,
        }))} />
      )}
    </Card>
  )
}

function ApprovedSummary({ approved }: { approved: ApprovedRecordSet }) {
  const columns: ColumnsType<RecordReviewDecision> = [
    { title: '#', dataIndex: 'ordinal', width: 55, render: (value: number) => value + 1 },
    { title: '决定', dataIndex: 'action', width: 110, render: (value: string) => <Tag color={value === 'exclude' ? 'default' : value === 'edit' ? 'purple' : 'green'}>{value === 'exclude' ? '排除' : value === 'edit' ? '编辑后批准' : '批准'}</Tag> },
    { title: '记录', dataIndex: 'fields', render: (fields: Record<string, unknown>) => <Text>{Object.entries(fields).slice(0, 3).map(([key, value]) => `${key}: ${typeof value === 'string' ? value : JSON.stringify(value)}`).join(' · ')}</Text> },
    { title: '备注', dataIndex: 'note', width: 220, render: (value?: string) => value || '—' },
  ]
  return (
    <Card title="不可变审核记录" extra={<Text code copyable>{approved.id}</Text>}>
      <Descriptions bordered size="small" column={3} items={[
        { key: 'reviewer', label: '审核人', children: approved.reviewer },
        { key: 'time', label: '审核时间', children: new Date(approved.created_at).toLocaleString('zh-CN') },
        { key: 'through', label: '复查位点', children: approved.reviewed_through_seq },
        { key: 'counts', label: '决定统计', children: `批准 ${approved.approved_count} / 编辑 ${approved.edited_count} / 排除 ${approved.excluded_count}` },
        { key: 'hash', label: '审核哈希', span: 2, children: <Text code copyable>{approved.review_hash}</Text> },
        { key: 'rationale', label: '审核理由', span: 3, children: <Paragraph style={{ margin: 0 }}>{approved.rationale}</Paragraph> },
      ]} />
      <Table<RecordReviewDecision> rowKey="id" size="small" pagination={false} dataSource={approved.decisions} columns={columns} style={{ marginTop: 16 }} />
    </Card>
  )
}
