import { Card, Descriptions, Button, Space, Tag, Typography, App } from 'antd'
import { ApiOutlined, CloudServerOutlined, ExperimentOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { api } from '../api/client'
import type { SettingsView } from '../api/types'

const { Text, Paragraph } = Typography

/** 设置页：配置只读（脱敏）+ 连通性测试。修改一律走本地 config.yaml + 重启（零硬编码原则）。 */
export default function Settings() {
  const { message } = App.useApp()
  const { data, refetch } = useQuery({ queryKey: ['settings'], queryFn: () => api.get<SettingsView>('/api/settings') })

  const test = async (kind: 'llm' | 'pingcode') => {
    const key = ['test', kind]
    try {
      await api.post(`/api/settings/test-${kind}`)
      message.success(kind === 'llm' ? 'LLM 连接正常' : 'PingCode 连接正常')
    } catch (e) {
      message.error((e as Error).message)
    }
    void key
    refetch()
  }

  if (!data) return <Card loading />

  return (
    <Card title={<Text strong>设置</Text>} style={{ maxWidth: 860 }}>
      <Paragraph type="secondary">
        所有配置来自本地 <Text code>config.yaml</Text>（启动时加载，环境变量 <Text code>REQFLOW_*</Text> 可覆盖）。
        代码与打包产物不含任何硬编码配置；修改后需重启服务生效。
      </Paragraph>

      <Descriptions title="LLM 分析模型" bordered size="small" column={2}
        extra={<Button size="small" icon={<ExperimentOutlined />} onClick={() => test('llm')}>测试连接</Button>}>
        <Descriptions.Item label="状态">{data.llm.configured ? <Tag color="green">已配置</Tag> : <Tag color="orange">未配置</Tag>}</Descriptions.Item>
        <Descriptions.Item label="协议"><Space><ApiOutlined />OpenAI 兼容</Space></Descriptions.Item>
        <Descriptions.Item label="Base URL">{data.llm.baseUrl}</Descriptions.Item>
        <Descriptions.Item label="模型">{data.llm.model}</Descriptions.Item>
      </Descriptions>

      <Descriptions title="语义匹配（Embedding）" bordered size="small" column={2} style={{ marginTop: 24 }}>
        <Descriptions.Item label="状态">{data.embedding.configured ? <Tag color="green">已配置</Tag> : <Tag color="default">未启用 · 仅精确匹配</Tag>}</Descriptions.Item>
        <Descriptions.Item label="Base URL">{data.embedding.baseUrl}</Descriptions.Item>
        <Descriptions.Item label="模型">{data.embedding.model}</Descriptions.Item>
        <Descriptions.Item label="说明">未配置不影响主流程，语义查重与项目推荐自动降级</Descriptions.Item>
      </Descriptions>

      <Descriptions title="PingCode 连接" bordered size="small" column={2} style={{ marginTop: 24 }}
        extra={<Button size="small" icon={<CloudServerOutlined />} onClick={() => test('pingcode')}>测试连接</Button>}>
        <Descriptions.Item label="状态">{data.pingcode.configured ? <Tag color="green">已配置</Tag> : <Tag color="orange">未配置</Tag>}</Descriptions.Item>
        <Descriptions.Item label="授权方式">企业授权（client_credentials）</Descriptions.Item>
        <Descriptions.Item label="Host" span={2}>{data.pingcode.host}</Descriptions.Item>
      </Descriptions>

      <Descriptions title="PDF 云端解析（MinerU）" bordered size="small" column={2} style={{ marginTop: 24 }}>
        <Descriptions.Item label="状态">{data.mineru.configured ? <Tag color="green">可用</Tag> : <Tag color="default">未配置 · PDF 不可解析</Tag>}</Descriptions.Item>
        <Descriptions.Item label="说明">docx / md / txt 本地解析，不依赖该服务</Descriptions.Item>
      </Descriptions>
    </Card>
  )
}
