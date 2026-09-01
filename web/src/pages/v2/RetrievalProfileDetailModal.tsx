import { App, Button, Descriptions, Empty, Modal, Popconfirm, Space, Tag, Typography } from 'antd'
import { DeleteOutlined } from '@ant-design/icons'
import { useQueryClient } from '@tanstack/react-query'
import { v2CatalogApi } from '../../api/v2/catalog'
import type { V2RetrievalProfile } from '../../api/v2/types'

const { Text } = Typography

interface Props {
  profile?: V2RetrievalProfile | null
  schemaName?: string
  open: boolean
  onClose: () => void
  /** 删除成功后回调（例如刷新规则列表、重置选中项）。 */
  onDeleted?: () => void
}

/** 索引规则详情：完整展示精准/语义/融合/筛选配置，并提供删除入口。
 * 删除保护在后端：仍有索引快照的规则会返回 409 并提示先删快照。 */
export default function RetrievalProfileDetailModal({ profile, schemaName, open, onClose, onDeleted }: Props) {
  const { message } = App.useApp()
  const queryClient = useQueryClient()

  const remove = async () => {
    if (!profile) return
    try {
      await v2CatalogApi.deleteRetrievalProfile(profile.id)
      message.success(`索引规则「${profile.name}」已删除`)
      onClose()
      await queryClient.invalidateQueries({ queryKey: ['v2-retrieval-profiles'] })
      await queryClient.invalidateQueries({ queryKey: ['v2-retrieval-snapshots'] })
      onDeleted?.()
    } catch (error) {
      message.error((error as Error).message)
    }
  }

  const lexicalFields = Object.entries((profile?.lexical?.fields ?? {}) as Record<string, number>)
  const vectorFields = (profile?.vector?.fields ?? []) as string[]
  const fusion = (profile?.fusion ?? {}) as Record<string, unknown>

  return <Modal
    width={680}
    title={`索引规则 · ${profile?.name ?? ''}`}
    open={open && Boolean(profile)}
    onCancel={onClose}
    footer={<Space>
      <Popconfirm
        title="删除该索引规则？"
        description="已有索引快照的规则需要先删除对应快照；删除规则不影响已入库数据。"
        onConfirm={() => void remove()}
      >
        <Button danger icon={<DeleteOutlined />}>删除规则</Button>
      </Popconfirm>
      <Button type="primary" onClick={onClose}>关闭</Button>
    </Space>}
  >
    {profile ? <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Descriptions bordered size="small" column={2}>
        <Descriptions.Item label="目标数据结构">{schemaName || profile.dataset_schema_id}</Descriptions.Item>
        <Descriptions.Item label="创建时间">{new Date(profile.created_at).toLocaleString('zh-CN')}</Descriptions.Item>
        <Descriptions.Item label="精准分词器">{String((profile.lexical as Record<string, unknown>)?.analyzer ?? 'standard')}</Descriptions.Item>
        <Descriptions.Item label="筛选字段">{profile.filter_fields.length > 0 ? profile.filter_fields.join('、') : '—'}</Descriptions.Item>
      </Descriptions>
      <Descriptions bordered size="small" column={1}>
        <Descriptions.Item label="精准搜索字段（BM25）">
          {lexicalFields.length > 0
            ? <Space size={4} wrap>{lexicalFields.map(([field, boost]) => <Tag key={field} color="blue">{field}{Number(boost) !== 1 ? ` ×${boost}` : ''}</Tag>)}</Space>
            : '未配置'}
        </Descriptions.Item>
        <Descriptions.Item label="语义搜索字段（向量）">
          {vectorFields.length > 0
            ? <Space size={4} wrap>{vectorFields.map((field) => <Tag key={field} color="purple">{field}</Tag>)}</Space>
            : '未配置'}
        </Descriptions.Item>
        <Descriptions.Item label="语义切片">
          {`片段长度 ${(profile.vector as Record<string, unknown>)?.chunk_size ?? '—'} · 相邻重叠 ${(profile.vector as Record<string, unknown>)?.chunk_overlap ?? '—'}`}
        </Descriptions.Item>
        <Descriptions.Item label="两路融合">
          {`${String(fusion.method ?? 'rrf')} · 精准候选 ${fusion.lexical_candidates ?? '—'} · 语义候选 ${fusion.vector_candidates ?? '—'} · RANK 常数 ${fusion.rank_constant ?? '—'}`}
        </Descriptions.Item>
      </Descriptions>
      <Text type="secondary" copyable={{ text: profile.profile_hash }} style={{ fontSize: 12 }}>
        规则指纹：{profile.profile_hash.slice(0, 16)}…
      </Text>
    </Space> : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="规则不存在或已被删除" />}
  </Modal>
}
