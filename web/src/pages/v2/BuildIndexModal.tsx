import { useEffect, useRef, useState } from 'react'
import { Alert, App, Button, Modal, Select, Space, Typography } from 'antd'
import { PlusOutlined, ThunderboltOutlined } from '@ant-design/icons'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { v2CatalogApi } from '../../api/v2/catalog'
import { v2TasksApi } from '../../api/v2/tasks'
import type { V2Dataset } from '../../api/v2/types'
import EmbeddedResourceCreate, { type EmbeddedResource } from './EmbeddedResourceCreate'

const { Text } = Typography

interface Props {
  dataset?: V2Dataset
  open: boolean
  onCancel: () => void
  /** 任务成功建立索引后回调（例如刷新快照列表）。 */
  onSucceeded?: () => void
  /** 任务创建并启动后回调（例如展示“查看任务”入口）。 */
  onStarted?: (taskID: string) => void
}

/**
 * 数据集上的「建立索引」弹窗：选择或就地创建适用于该数据集字段结构的索引规则，
 * 提交后自动创建并启动隐式索引任务（底层经由固定 retrieval_index 流程执行）。
 */
export default function BuildIndexModal({ dataset, open, onCancel, onSucceeded, onStarted }: Props) {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const [selectedProfileID, setSelectedProfileID] = useState<string>()
  const [profileCreateOpen, setProfileCreateOpen] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [indexTaskID, setIndexTaskID] = useState<string>()
  const handledRef = useRef<string>()

  const profiles = useQuery({
    queryKey: ['v2-retrieval-profiles', dataset?.schema_id],
    queryFn: () => v2CatalogApi.queryRetrievalProfiles({ datasetSchemaId: dataset!.schema_id }),
    enabled: open && Boolean(dataset?.schema_id),
  })
  const fixed = useQuery({
    queryKey: ['v2-definitions', 'retrieval_index'],
    queryFn: () => v2CatalogApi.listDefinitions({ status: 'active', limit: 200 }),
    enabled: open,
    staleTime: 60_000,
  })
  const indexTask = useQuery({
    queryKey: ['v2-task', indexTaskID],
    queryFn: () => v2TasksApi.get(indexTaskID!),
    enabled: Boolean(indexTaskID),
    refetchInterval: (query) => {
      const status = query.state.data?.task.status
      return status && ['succeeded', 'failed'].includes(status) ? false : 1000
    },
  })

  useEffect(() => {
    if (!open) return
    const available = profiles.data?.retrieval_profiles ?? []
    if (!available.some((item) => item.id === selectedProfileID)) {
      setSelectedProfileID(available[0]?.id)
    }
  }, [open, profiles.data, selectedProfileID])

  const taskID = indexTask.data?.task.id
  const status = indexTask.data?.task.status
  useEffect(() => {
    if (!taskID || !status || handledRef.current === taskID) return
    if (status !== 'succeeded' && status !== 'failed') return
    handledRef.current = taskID
    setIndexTaskID(undefined)
    if (status === 'succeeded') {
      void queryClient.invalidateQueries({ queryKey: ['v2-retrieval-snapshots'] })
      message.success(`数据集「${dataset?.name ?? ''}」索引已建立，检索自动启用`)
      onSucceeded?.()
      onCancel()
    } else {
      message.error(indexTask.data?.task.error_message || '索引任务执行失败')
    }
  }, [taskID, status, indexTask.data, message, onCancel, onSucceeded, queryClient, dataset?.name])

  const startIndexing = async () => {
    if (!dataset || !selectedProfileID) return
    setSubmitting(true)
    try {
      const definition = (fixed.data?.task_definitions ?? []).find((item) => item.key === 'retrieval_index')
      if (!definition) throw new Error('固定索引流程未就绪，请稍后重试或联系管理员')
      const buildStep = definition.steps.find((item) => item.kind === 'retrieval.build')
      if (!buildStep) throw new Error('固定索引流程缺少 retrieval.build 步骤')
      const created = await v2TasksApi.create({
        definition_id: definition.id,
        title: `为「${dataset.name}」建立检索索引`,
        bindings: [{ port_name: 'dataset', resource_type: 'dataset_boundary', resource_id: dataset.id }],
        step_configs: { [buildStep.id]: { retrieval_profile_id: selectedProfileID } },
      })
      await v2TasksApi.start(created.task.id)
      setIndexTaskID(created.task.id)
      onStarted?.(created.task.id)
      message.info('索引任务已启动，完成后会自动启用检索')
    } catch (error) {
      message.error((error as Error).message)
    } finally {
      setSubmitting(false)
    }
  }

  const running = Boolean(indexTaskID)

  return <>
    <Modal
      title={dataset ? `为「${dataset.name}」建立索引` : '建立索引'}
      open={open && Boolean(dataset)}
      onCancel={() => !submitting && !running && onCancel()}
      footer={<Space>
        <Button disabled={submitting || running} onClick={onCancel}>关闭</Button>
        <Button type="primary" icon={<ThunderboltOutlined />} loading={submitting || running}
          disabled={!selectedProfileID || fixed.isLoading} onClick={() => void startIndexing()}>
          {running ? '索引任务运行中…' : '创建索引规则任务并运行'}
        </Button>
      </Space>}
      destroyOnHidden
    >
      <Alert type="info" showIcon style={{ marginBottom: 16 }}
        message="索引经由固定 retrieval.build 流程任务生成，不会绕过流程直接写入数据；创建后会自动运行。" />
      <Space.Compact style={{ width: '100%' }}>
        <Select
          value={selectedProfileID}
          onChange={setSelectedProfileID}
          loading={profiles.isLoading}
          showSearch
          optionFilterProp="label"
          placeholder="选择适用于当前字段结构的索引规则"
          options={(profiles.data?.retrieval_profiles ?? []).map((item) => ({ value: item.id, label: item.name }))}
          style={{ width: 'calc(100% - 112px)' }}
        />
        <Button icon={<PlusOutlined />} onClick={() => setProfileCreateOpen(true)}>新建规则</Button>
      </Space.Compact>
      {!profiles.isLoading && profiles.data?.retrieval_profiles.length === 0 && <Text type="secondary">当前字段结构还没有索引规则，请直接新建。</Text>}
      {dataset && <Space size={4} wrap style={{ marginTop: 12 }}>
        <Text type="secondary">将索引该数据集当前已提交的 {dataset.item_count} 条内容。</Text>
      </Space>}
    </Modal>

    <EmbeddedResourceCreate
      kind={profileCreateOpen ? 'retrieval' : undefined}
      fixedSchemaId={dataset?.schema_id}
      onCancel={() => setProfileCreateOpen(false)}
      onCreated={(resource: EmbeddedResource) => {
        setSelectedProfileID(resource.id)
        void queryClient.invalidateQueries({ queryKey: ['v2-retrieval-profiles'] })
        setProfileCreateOpen(false)
      }}
    />
  </>
}
