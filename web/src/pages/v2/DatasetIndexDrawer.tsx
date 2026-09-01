import { useEffect, useMemo, useRef, useState } from 'react'
import {
  Alert, App, Button, Drawer, Empty, Popconfirm, Select, Space, Table, Tag, Typography,
} from 'antd'
import type { ColumnsType } from 'antd/es/table'
import { PlusOutlined, ThunderboltOutlined } from '@ant-design/icons'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { v2CatalogApi } from '../../api/v2/catalog'
import { v2TasksApi } from '../../api/v2/tasks'
import type { V2Dataset, V2RetrievalSnapshot } from '../../api/v2/types'
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

const STATUS_META: Record<string, { label: string; color: string }> = {
  active: { label: '检索使用中', color: 'green' },
  building: { label: '构建中', color: 'blue' },
  validating: { label: '校验中', color: 'blue' },
  failed: { label: '失败', color: 'red' },
  retired: { label: '已过期', color: 'default' },
}

/**
 * 数据集上的「索引」抽屉（与「字段」抽屉同级）：
 * 选择或就地创建与字段结构绑定的索引规则并一键创建索引任务；
 * 展示快照列表、按规则筛选、提示索引落后数据、支持删除过期快照。
 */
export default function DatasetIndexDrawer({ dataset, open, onCancel, onSucceeded, onStarted }: Props) {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const [selectedProfileID, setSelectedProfileID] = useState<string>()
  const [profileCreateOpen, setProfileCreateOpen] = useState(false)
  const [submitting, setSubmitting] = useState(false)
  const [indexTaskID, setIndexTaskID] = useState<string>()
  const [ruleFilter, setRuleFilter] = useState<string>()
  const handledTaskRef = useRef<string>()

  const profiles = useQuery({
    queryKey: ['v2-retrieval-profiles', dataset?.schema_id],
    queryFn: () => v2CatalogApi.queryRetrievalProfiles({ datasetSchemaId: dataset!.schema_id }),
    enabled: open && Boolean(dataset?.schema_id),
  })
  const profileName = useMemo(
    () => new Map((profiles.data?.retrieval_profiles ?? []).map((item) => [item.id, item.name])),
    [profiles.data],
  )
  const fixed = useQuery({
    queryKey: ['v2-definitions', 'retrieval_index'],
    queryFn: () => v2CatalogApi.listDefinitions({ status: 'active', limit: 200 }),
    enabled: open,
    staleTime: 60_000,
  })
  const snapshots = useQuery({
    queryKey: ['v2-retrieval-snapshots', dataset?.id, 'all'],
    queryFn: () => v2CatalogApi.queryRetrievalSnapshots({ datasetId: dataset!.id, limit: 200 }),
    enabled: open && Boolean(dataset?.id),
    refetchInterval: indexTaskID ? 1500 : undefined,
  })
  const indexTask = useQuery({
    queryKey: ['v2-task', indexTaskID],
    queryFn: () => v2TasksApi.get(indexTaskID!),
    enabled: Boolean(indexTaskID),
    refetchInterval: 1500,
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
    if (!taskID || !status || handledTaskRef.current === taskID) return
    if (status !== 'succeeded' && status !== 'failed') return
    handledTaskRef.current = taskID
    setIndexTaskID(undefined)
    void queryClient.invalidateQueries({ queryKey: ['v2-retrieval-snapshots'] })
    if (status === 'succeeded') {
      message.success(`数据集「${dataset?.name ?? ''}」索引已建立`)
      onSucceeded?.()
    } else {
      message.error(indexTask.data?.task.error_message || '索引任务执行失败')
    }
  }, [taskID, status, indexTask.data, message, onCancel, onSucceeded, queryClient, dataset?.name])

  const allSnapshots = useMemo(
    () => [...(snapshots.data?.retrieval_snapshots ?? [])].sort((a, b) =>
      b.source_seq - a.source_seq || (b.activated_at ?? b.created_at).localeCompare(a.activated_at ?? a.created_at)),
    [snapshots.data],
  )
  const visibleSnapshots = ruleFilter
    ? allSnapshots.filter((item) => item.retrieval_profile_id === ruleFilter)
    : allSnapshots
  const activeSnapshots = allSnapshots.filter((item) => item.status === 'active')
  const coveredSeq = activeSnapshots.reduce((max, item) => Math.max(max, item.source_seq), 0)
  const behindCount = Math.max(0, (dataset?.current_seq ?? 0) - coveredSeq)

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

  const removeSnapshot = async (snapshot: V2RetrievalSnapshot) => {
    try {
      await v2CatalogApi.deleteRetrievalSnapshot(snapshot.id)
      message.success('快照已删除')
      await queryClient.invalidateQueries({ queryKey: ['v2-retrieval-snapshots'] })
      onSucceeded?.()
    } catch (error) {
      message.error((error as Error).message)
    }
  }

  const columns: ColumnsType<V2RetrievalSnapshot> = [
    {
      title: '状态', width: 110,
      render: (_, item) => {
        const meta = STATUS_META[item.status] ?? { label: item.status, color: 'default' }
        return <Tag color={meta.color}>{meta.label}</Tag>
      },
    },
    { title: '索引规则', render: (_, item) => profileName.get(item.retrieval_profile_id) ?? item.retrieval_profile_id.slice(0, 8) },
    { title: '覆盖', width: 90, render: (_, item) => <Text code>≤ {item.source_seq}</Text> },
    { title: '内容', width: 130, render: (_, item) => <Space size={4}><Tag color="blue">精准 {item.lexical_count}</Tag><Tag color="purple">语义 {item.vector_count}</Tag></Space> },
    { title: '创建时间', width: 165, render: (_, item) => new Date(item.created_at).toLocaleString('zh-CN') },
    {
      title: '操作', width: 80,
      render: (_, item) => ['building', 'validating'].includes(item.status)
        ? <Text type="secondary">构建中</Text>
        : <Popconfirm
            title="删除该快照？"
            description={item.status === 'active' ? '这是当前检索使用的快照，删除后该规则的语义检索会立即失效。' : '删除后不可恢复。'}
            onConfirm={() => void removeSnapshot(item)}
          >
            <Button size="small" danger type="text">删除</Button>
          </Popconfirm>,
    },
  ]

  const running = Boolean(indexTaskID)

  return <>
    <Drawer
      width={880}
      title={dataset ? `${dataset.name} · 索引` : '索引'}
      open={open && Boolean(dataset)}
      onClose={() => !submitting && !running && onCancel()}
      destroyOnHidden
    >
      {dataset && (activeSnapshots.length === 0
        ? (dataset.item_count > 0 || dataset.current_seq > 0) && <Alert
            type="warning"
            showIcon
            style={{ marginBottom: 16 }}
            message="该数据集还没有可用索引"
            description={`已提交 ${dataset.current_seq || dataset.item_count} 条内容；选择或新建索引规则后点击“创建索引任务”，完成后即可语义检索。`}
          />
        : behindCount > 0 && <Alert
            type="warning"
            showIcon
            style={{ marginBottom: 16 }}
            message={`索引落后数据 ${behindCount} 条`}
            description={`最新可用索引覆盖到第 ${coveredSeq} 条，数据集已提交 ${dataset.current_seq} 条。用同一索引规则再次创建即可增量补齐。`}
          />)}

      <Alert type="info" showIcon style={{ marginBottom: 16 }}
        message="索引经由固定 retrieval.build 流程任务生成，不会绕过流程直接写入数据；创建后会自动运行。" />

      <Space.Compact style={{ width: '100%', marginBottom: 8 }}>
        <Select
          value={selectedProfileID}
          onChange={setSelectedProfileID}
          loading={profiles.isLoading}
          showSearch
          optionFilterProp="label"
          placeholder="选择适用于当前字段结构的索引规则"
          options={(profiles.data?.retrieval_profiles ?? []).map((item) => ({ value: item.id, label: item.name }))}
          style={{ flex: 1 }}
        />
        <Button icon={<PlusOutlined />} onClick={() => setProfileCreateOpen(true)}>新建规则</Button>
        <Button type="primary" icon={<ThunderboltOutlined />} loading={submitting || running}
          disabled={!selectedProfileID || fixed.isLoading} onClick={() => void startIndexing()}>
          {running ? '索引任务运行中…' : '创建索引任务'}
        </Button>
      </Space.Compact>
      {!profiles.isLoading && profiles.data?.retrieval_profiles.length === 0 && <Text type="secondary">当前字段结构还没有索引规则，请先新建。</Text>}

      <Space style={{ width: '100%', justifyContent: 'space-between', marginBlock: '20px 8px' }}>
        <Text strong>索引快照</Text>
        <Select
          size="small"
          style={{ minWidth: 180 }}
          value={ruleFilter}
          onChange={setRuleFilter}
          allowClear
          placeholder="全部规则"
          options={(profiles.data?.retrieval_profiles ?? []).map((item) => ({ value: item.id, label: item.name }))}
        />
      </Space>
      <Table
        rowKey="id"
        size="small"
        loading={snapshots.isLoading}
        dataSource={visibleSnapshots}
        columns={columns}
        pagination={false}
        locale={{ emptyText: <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="还没有索引快照" /> }}
      />
    </Drawer>

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
