import { Alert, App, Button, Card, Collapse, Empty, Input, List, Space, Tag, Typography } from 'antd'
import { ArrowLeftOutlined, PauseCircleOutlined, PlayCircleOutlined, ReloadOutlined } from '@ant-design/icons'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useNavigate, useParams } from 'react-router-dom'
import { useState } from 'react'
import { workflowRunsApi, type NodeRun, type ResourceBinding } from '../../api/workflowRuns'

const statusColor: Record<string, string> = { queued: 'blue', running: 'processing', paused: 'orange', awaiting_manual_completion: 'purple', failed: 'red', succeeded: 'green', retry_wait: 'gold' }

export default function WorkflowRunDetail() {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const { message } = App.useApp()
  const client = useQueryClient()
	const [manualText, setManualText] = useState('{}')
  const query = useQuery({ queryKey: ['workflow-run', id], queryFn: () => workflowRunsApi.get(id), enabled: Boolean(id) })
  const change = useMutation({
    mutationFn: (action: 'start' | 'pause' | 'resume') => workflowRunsApi[action](id),
    onSuccess: () => { void client.invalidateQueries({ queryKey: ['workflow-run', id] }); void client.invalidateQueries({ queryKey: ['workflow-runs'] }) },
    onError: (error) => message.error((error as Error).message),
  })
  const retry = useMutation({ mutationFn: (nodeID: string) => workflowRunsApi.retry(id, nodeID), onSuccess: () => { void client.invalidateQueries({ queryKey: ['workflow-run', id] }) }, onError: (error) => message.error((error as Error).message) })
  const manual = useMutation({
    mutationFn: async ({ nodeID, text }: { nodeID: string; text: string }) => workflowRunsApi.completeManual(id, nodeID, JSON.parse(text)),
    onSuccess: () => { void client.invalidateQueries({ queryKey: ['workflow-run', id] }); setManualText('{}') },
    onError: (error) => message.error((error as Error).message),
  })
  const snapshot = query.data
  if (query.isLoading) return <Card style={{ margin: 24 }}>加载运行…</Card>
  if (!snapshot) return <Card style={{ margin: 24 }}><Empty description="运行不存在" /></Card>
  const run = snapshot.run
  const bindingsByNode = new Map<string, ResourceBinding[]>()
  for (const binding of snapshot.bindings) bindingsByNode.set(binding.node_run_id ?? '', [...(bindingsByNode.get(binding.node_run_id ?? '') ?? []), binding])
  return (
    <div style={{ padding: 24, maxWidth: 1240, margin: '0 auto' }}>
      <Space style={{ marginBottom: 18 }}><Button type="text" icon={<ArrowLeftOutlined />} onClick={() => navigate('/runs')} /><Typography.Title level={3} style={{ margin: 0 }}>{run.revision.name}</Typography.Title><Tag color={statusColor[run.status] ?? 'default'}>{run.status}</Tag></Space>
      {run.error_message && <Alert type="error" message={run.error_code} description={run.error_message} showIcon style={{ marginBottom: 18 }} />}
      <Card title="运行控制" extra={<Typography.Text type="secondary">Revision {run.revision_id}</Typography.Text>} style={{ marginBottom: 18 }}>
        <Space wrap>
          <Button type="primary" icon={<PlayCircleOutlined />} disabled={run.status !== 'queued'} loading={change.isPending} onClick={() => change.mutate('start')}>启动</Button>
          <Button icon={<PauseCircleOutlined />} disabled={run.status !== 'running'} loading={change.isPending} onClick={() => change.mutate('pause')}>暂停</Button>
          <Button icon={<ReloadOutlined />} disabled={run.status !== 'paused'} loading={change.isPending} onClick={() => change.mutate('resume')}>继续</Button>
        </Space>
      </Card>
      <Card title="节点运行链">
        <List dataSource={snapshot.nodes} renderItem={(node: NodeRun) => <List.Item>
          <List.Item.Meta avatar={<Tag color={statusColor[node.status] ?? 'default'}>{node.ordinal}</Tag>} title={<Space><Typography.Text strong>{node.node.name}</Typography.Text><Tag>{node.status}</Tag><Typography.Text type="secondary">attempt {node.attempt}</Typography.Text></Space>} description={node.error_message || `${node.node.capability.ref.kind}@${node.node.capability.ref.version}`} />
          <Space direction="vertical" align="end">
            {(bindingsByNode.get(node.id) ?? []).length > 0 && <Collapse size="small" items={[{ key: node.id, label: `${(bindingsByNode.get(node.id) ?? []).length} 个产物`, children: <List size="small" dataSource={bindingsByNode.get(node.id) ?? []} renderItem={(binding) => <List.Item><Typography.Text code>{binding.port}</Typography.Text><Typography.Text>{binding.resource_type}:{binding.resource_id}</Typography.Text></List.Item>} /> }]} />}
            {node.status === 'failed' && <Button size="small" onClick={() => retry.mutate(node.node_id)} loading={retry.isPending}>重试节点</Button>}
            {node.status === 'awaiting_manual_completion' && <Space direction="vertical" align="end">
              <Input.TextArea rows={4} value={manualText} onChange={(event) => setManualText(event.target.value)}
                placeholder={node.node.capability.ref.kind === 'human.review_records'
                  ? '{"rationale":"…","decisions":[{"validation_result_id":"…","action":"approve|edit|exclude","fields":{…},"note":"…"}]}'
                  : '{"decision":"approve|edit","output":{…},"rationale":"…"}'} />
              <Button type="primary" size="small" onClick={() => manual.mutate({ nodeID: node.node_id, text: manualText })} loading={manual.isPending}>提交人工确认</Button>
            </Space>}
          </Space>
        </List.Item>} />
      </Card>
    </div>
  )
}
