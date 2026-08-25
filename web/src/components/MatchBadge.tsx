import { Tag, Tooltip, Typography } from 'antd'
import type { DuplicateMatch } from '../api/types'

const { Text } = Typography

/** 查重徽标：新需求（绿）/ 精确命中（红）/ 语义相似（橙，含分数） */
export default function MatchBadge({ match }: { match?: DuplicateMatch | null }) {
  if (!match) return <Tag color="green" style={{ margin: 0 }}>新需求</Tag>
  if (match.match_type === 'exact') {
    return (
      <Tooltip title={`与「${match.title}」标题完全一致`}>
        <Tag color="red" style={{ margin: 0 }}>疑似重复</Tag>
      </Tooltip>
    )
  }
  return (
    <Tooltip title={`与「${match.title}」语义相似度 ${(match.score * 100).toFixed(0)}%`}>
      <Tag color="orange" style={{ margin: 0 }}>
        相似 <Text style={{ fontSize: 12 }}>{(match.score * 100).toFixed(0)}%</Text>
      </Tag>
    </Tooltip>
  )
}
