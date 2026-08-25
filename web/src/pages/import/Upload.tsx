import { Card, Typography, Alert } from 'antd'
import { InboxOutlined } from '@ant-design/icons'
import { Upload as AntUpload, Input } from 'antd'
import { useNavigate } from 'react-router-dom'
import { useImportWizard } from '../../stores/importWizard'
import { useQuery } from '@tanstack/react-query'
import { api } from '../../api/client'
import type { SettingsView } from '../../api/types'

const { Dragger } = AntUpload
const { Text } = Typography

/** 阶段 1：上传文档 + 额外要求 */
export default function Upload() {
  const navigate = useNavigate()
  const { uploadAndParse, parsing, parseMessage, specialRequirements, setField, reset } = useImportWizard()
  const { data: settings } = useQuery({ queryKey: ['settings'], queryFn: () => api.get<SettingsView>('/api/settings') })

  const handleFile = async (file: File) => {
    reset()
    try {
      await uploadAndParse(file)
      navigate('/import/review')
    } catch (e) {
      // parseSSE 内已写 store；这里兜底提示
      console.error(e)
    }
    return false // 阻止 antd 自动上传
  }

  return (
    <Card style={{ maxWidth: 860 }}>
      {settings && !settings.llm.configured && (
        <Alert
          type="warning" showIcon style={{ marginBottom: 16 }}
          message="LLM 未配置"
          description="尚未配置 llm.api_key，文档可以解析，但 AI 分析不可用。请编辑 config.yaml 后重启（见设置页）。"
        />
      )}
      <Dragger
        name="file"
        accept=".docx,.pdf,.md,.txt"
        showUploadList={false}
        disabled={parsing}
        customRequest={() => {}}
        beforeUpload={handleFile}
      >
        <p className="ant-upload-drag-icon"><InboxOutlined /></p>
        <p className="ant-upload-text">{parsing ? '正在解析…' : '拖拽或点击上传需求文档'}</p>
        <p className="ant-upload-hint">
          支持 docx / pdf（MinerU 云端解析）/ markdown / 纯文本 · 解析后可先预览确认再交给 AI
        </p>
      </Dragger>

      {parsing && parseMessage && (
        <Alert type="info" showIcon style={{ marginTop: 16 }} message={parseMessage} />
      )}

      <div style={{ marginTop: 24 }}>
        <Text type="secondary">额外要求（可选，将注入分析提示词，优先级高于默认约定）</Text>
        <Input.TextArea
          rows={3}
          maxLength={2000}
          showCount
          value={specialRequirements}
          onChange={(e) => setField({ specialRequirements: e.target.value })}
          placeholder="例：每个需求必须给出验收标准；工时按 2 人日上限评估；忽略附录里的术语表…"
          style={{ marginTop: 8 }}
        />
      </div>

    </Card>
  )
}
