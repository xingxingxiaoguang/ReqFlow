export const DATASET_PURPOSE_OPTIONS: Array<{ value: string; label: string; shortLabel: string }> = [
  { value: 'base', label: '记录日常业务信息（客户、产品、需求、订单等）', shortLabel: '日常业务信息' },
  { value: 'query', label: '用于搜索和智能问答（已经整理好的知识内容）', shortLabel: '搜索与问答知识' },
  { value: 'analysis', label: '保存分析和判断结果（总结、分类、评分等）', shortLabel: '分析与判断结果' },
  { value: 'graph_node', label: '登记业务中的对象（人物、公司、产品等）', shortLabel: '人物、公司、产品等' },
  { value: 'graph_edge', label: '登记对象之间的关系（隶属、依赖、引用等）', shortLabel: '对象之间的关系' },
]

const DATASET_PURPOSE_LABELS = new Map<string, string>(
  DATASET_PURPOSE_OPTIONS.map((option) => [option.value, option.shortLabel]),
)

export function datasetPurposeLabel(value: string) {
  return DATASET_PURPOSE_LABELS.get(value) ?? value
}
