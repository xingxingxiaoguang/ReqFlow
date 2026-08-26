import { Card, Typography, Tag, Steps, Timeline, Alert } from 'antd'
import { BugOutlined } from '@ant-design/icons'

const { Text, Title, Paragraph } = Typography

/** Bug 处理：第二波占位页 —— 展示已定稿的链路设计，不做死链 */
export default function Bugs() {
  return (
    <Card>
      <Alert
        type="info" showIcon style={{ marginBottom: 20 }}
        message="Bug 处理链路将在第二波开放"
        description="Excel 行级解析、编号/语义双层匹配、top3 人工确认、P0-P3 批量定级所需的后端骨架（xlsx 解析、匹配策略、bug 域数据模型）已在第一波预留。"
      />

      <Title level={5}><BugOutlined /> 规划中的处理流</Title>
      <Steps
        size="small" direction="vertical" style={{ marginTop: 12 }}
        items={[
          { title: '导入 Bug 反馈 Excel', description: '上传 .xlsx，按表头映射为结构化 bug 行（编号 / 标题 / 描述 / 复现步骤）' },
          { title: '双层匹配需求', description: '有编号 → 与已同步工作项编号精确匹配；无编号 → 语义匹配取 top3 候选' },
          { title: '人工确认关联', description: 'top3 候选人工确认或否决；匹配度过低的行标记为无效 bug' },
          { title: 'AI 批量定级', description: 'LLM 按 P0（阻塞性）/ P1（核心功能性）/ P2（次要功能性）/ P3（边缘性）定档，并给出理由' },
          { title: '生成 Bug 数据集', description: '确认关联的 bug 写入 Bug 数据集，关联需求（来自需求数据集）写入字段' },
        ]}
      />

      <Card size="small" style={{ marginTop: 20 }} title={<Text strong>优先级档位定义</Text>}>
        <Timeline
          items={[
            { color: 'red', children: <><Tag color="red">P0</Tag> 阻塞性 —— 主流程不可用、数据丢失/损坏、安全漏洞，需立即修复</> },
            { color: 'orange', children: <><Tag color="orange">P1</Tag> 核心功能性 —— 核心功能错误但有绕行方案，本迭代内修复</> },
            { color: 'gold', children: <><Tag color="gold">P2</Tag> 次要功能性 —— 边缘场景问题、体验缺陷，排期修复</> },
            { color: 'default', children: <><Tag>P3</Tag> 边缘性 —— 趋近无效（无法复现/描述不清/极低频），转入观察或关闭</> },
          ]}
        />
      </Card>

      <Paragraph type="secondary" style={{ marginTop: 16 }}>
        当前版本聚焦需求导入全链路；本页功能上线前，bug 可先以 docx/md 文档走「需求导入」向导分析（类型选择 bug）。
      </Paragraph>
    </Card>
  )
}
