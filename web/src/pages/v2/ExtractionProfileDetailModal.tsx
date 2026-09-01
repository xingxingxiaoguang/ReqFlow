import { App, Button, Descriptions, Empty, Modal, Popconfirm, Space, Spin, Tag, Typography } from 'antd'
import { DeleteOutlined } from '@ant-design/icons'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { v2CatalogApi } from '../../api/v2/catalog'
import type { V2ExtractionProfileDetail } from '../../api/v2/types'

const { Paragraph, Text } = Typography

interface Props {
  /** 要预览的抽取规则 ID；为空时不弹窗。 */
  profileID?: string
  schemaName?: string
  open: boolean
  onClose: () => void
  /** 删除成功后回调（例如清空表单里对已删规则的选中）。 */
  onDeleted?: (deletedID: string) => void
}

const guideText = (value: unknown) => {
  if (value === null || value === undefined) return ''
  if (typeof value === 'string') return value
  return JSON.stringify(value, null, 2)
}

/** 抽取规则详情：展示记录粒度、抽取要求与字段指南，并提供删除入口。
 * 删除保护在后端：已被历史任务产物引用的规则会返回 409。 */
export default function ExtractionProfileDetailModal({ profileID, schemaName, open, onClose, onDeleted }: Props) {
  const { message } = App.useApp()
  const queryClient = useQueryClient()
  const profileQuery = useQuery({
    queryKey: ['v2-extraction-profile', profileID],
    queryFn: () => v2CatalogApi.getExtractionProfile(profileID!),
    enabled: open && Boolean(profileID),
  })
  const profile: V2ExtractionProfileDetail | undefined = profileQuery.data?.extraction_profile

  const remove = async () => {
    if (!profile) return
    try {
      await v2CatalogApi.deleteExtractionProfile(profile.id)
      message.success(`抽取规则「${profile.name}」已删除`)
      onClose()
      await queryClient.invalidateQueries({ queryKey: ['v2-extraction-profiles'] })
      onDeleted?.(profile.id)
    } catch (error) {
      message.error((error as Error).message)
    }
  }

  const fieldGuides = Object.entries(profile?.field_guides ?? {})
  const validationRules = profile?.validation_rules ?? []
  const normalizationRules = profile?.normalization_rules ?? []
  const examples = profile?.examples ?? []

  return <Modal
    width={680}
    title={`抽取规则 · ${profile?.name ?? ''}`}
    open={open && Boolean(profileID)}
    onCancel={onClose}
    footer={<Space>
      <Popconfirm
        title="删除该抽取规则？"
        description="已被历史任务使用过的规则无法删除（系统会提示）；删除不影响已入库数据。"
        onConfirm={() => void remove()}
      >
        <Button danger icon={<DeleteOutlined />}>删除规则</Button>
      </Popconfirm>
      <Button type="primary" onClick={onClose}>关闭</Button>
    </Space>}
  >
    {profileQuery.isLoading ? <Space style={{ width: '100%', justifyContent: 'center', padding: 32 }}><Spin /></Space>
      : profile ? <Space direction="vertical" size={16} style={{ width: '100%' }}>
        <Descriptions bordered size="small" column={2}>
          <Descriptions.Item label="目标数据结构">{schemaName || profile.target_schema_id}</Descriptions.Item>
          <Descriptions.Item label="创建时间">{new Date(profile.created_at).toLocaleString('zh-CN')}</Descriptions.Item>
          <Descriptions.Item label="一条记录代表" span={2}>{profile.record_granularity || '—'}</Descriptions.Item>
        </Descriptions>
        <Descriptions bordered size="small" column={1}>
          <Descriptions.Item label="抽取要求">
            <Paragraph style={{ marginBottom: 0, whiteSpace: 'pre-wrap' }}>{profile.system_instruction || '—'}</Paragraph>
          </Descriptions.Item>
          <Descriptions.Item label={`字段指南（${fieldGuides.length}）`}>
            {fieldGuides.length > 0
              ? <Space direction="vertical" size={6} style={{ width: '100%' }}>
                {fieldGuides.map(([field, guide]) => <Space key={field} align="start" size={6}>
                  <Tag color="blue" style={{ marginTop: 2 }}>{field}</Tag>
                  <Text style={{ whiteSpace: 'pre-wrap' }}>{guideText(guide) || '—'}</Text>
                </Space>)}
              </Space>
              : <Text type="secondary">未配置字段指南</Text>}
          </Descriptions.Item>
          <Descriptions.Item label="配套规则">
            {`归一化 ${normalizationRules.length} 条 · 校验 ${validationRules.length} 条 · 示例 ${examples.length} 个`}
          </Descriptions.Item>
        </Descriptions>
        <Text type="secondary" style={{ fontSize: 12 }}>规则指纹：{profile.profile_hash}</Text>
      </Space>
        : <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="规则不存在或已被删除" />}
  </Modal>
}
