import type { V2StepDefinition } from '../../api/v2/types'

export interface WorkflowBlockPort {
  name: string
  label: string
  resourceType: string
}

export interface WorkflowBlock {
  id: string
  kind: string
  label: string
  description: string
  suggestedId: string
  inputs: WorkflowBlockPort[]
  outputs: WorkflowBlockPort[]
  defaultConfig: Record<string, unknown>
}

const input = (name: string, label: string, resourceType: string): WorkflowBlockPort => ({ name, label, resourceType })

export const RESOURCE_TYPE_LABEL: Record<string, string> = {
  asset_set: '文件集',
  parsed_documents: '解析文档集',
  record_drafts: '结构化草稿',
  transformed_records: '清洗后记录',
  validation_results: '校验结果',
  approved_records: '审核通过记录',
  dataset: '可追加数据集',
  dataset_boundary: '数据集固定快照',
  dataset_batch: '数据批次',
  pipeline_cursor: '增量游标',
  retrieval_snapshot: '检索快照',
  analysis_result: '分析结果',
  artifact: '业务制品',
}

/**
 * 可由创建任务的人直接选择的流程入口资源。
 *
 * 其余类型都是 Executor 中间产物，必须由上游步骤生成。这个边界使
 * 编排器可以真正拦截“缺少必要上游”的步骤，而不是把任意中间态伪装成外部输入。
 */
export const TASK_BINDABLE_RESOURCE_TYPES = [
  'asset_set', 'dataset', 'dataset_boundary', 'retrieval_snapshot',
] as const

const taskBindableResourceTypes = new Set<string>(TASK_BINDABLE_RESOURCE_TYPES)

export const WORKFLOW_BLOCKS: WorkflowBlock[] = [
  {
    id: 'source_parse', kind: 'source.parse', label: '解析文件', suggestedId: 'parse',
    description: '将文件集解析为可供抽取使用的文档集合。',
    inputs: [input('assets', '待解析文件', 'asset_set')],
    outputs: [input('documents', '解析后的文档', 'parsed_documents')], defaultConfig: {},
  },
  {
    id: 'llm_extract', kind: 'llm.extract', label: '结构化抽取', suggestedId: 'extract',
    description: '按照抽取规则把文档转换成结构化记录草稿。',
    inputs: [input('documents', '解析后的文档', 'parsed_documents')],
    outputs: [input('drafts', '结构化草稿', 'record_drafts')], defaultConfig: { extraction_profile_id: '' },
  },
  {
    id: 'data_transform', kind: 'data.transform', label: '确定性清洗', suggestedId: 'transform',
    description: '执行可复现的字段标准化与数据清洗。',
    inputs: [input('drafts', '结构化草稿', 'record_drafts')],
    outputs: [input('records', '清洗后记录', 'transformed_records')], defaultConfig: {},
  },
  {
    id: 'data_validate', kind: 'data.validate', label: '数据校验', suggestedId: 'validate',
    description: '依据目标数据集结构与业务约束校验记录。',
    inputs: [input('records', '清洗后记录', 'transformed_records'), input('dataset', '目标数据集', 'dataset')],
    outputs: [input('validation', '校验结果', 'validation_results')], defaultConfig: {},
  },
  {
    id: 'human_review_records', kind: 'human.review', label: '人工审核数据', suggestedId: 'review',
    description: '暂停流程，由业务人员确认、修改或排除待入库记录。',
    inputs: [input('validation', '校验结果', 'validation_results')],
    outputs: [input('approved', '审核通过记录', 'approved_records')], defaultConfig: { allow_edit: true },
  },
  {
    id: 'data_publish', kind: 'data.publish', label: '发布数据', suggestedId: 'publish',
    description: '将审核通过的记录原子追加到目标数据集。',
    inputs: [input('approved', '审核通过记录', 'approved_records')],
    outputs: [input('batch', '数据批次', 'dataset_batch')], defaultConfig: {},
  },
  {
    id: 'query_derive', kind: 'data.query_derive', label: '增量派生查询数据', suggestedId: 'derive',
    description: '从业务数据集增量派生面向检索的 Query Dataset。',
    inputs: [input('source', '来源数据集快照', 'dataset_boundary'), input('target', '查询数据集', 'dataset')],
    outputs: [input('batch', '派生数据批次', 'dataset_batch'), input('cursor', '增量游标', 'pipeline_cursor')],
    defaultConfig: { pipeline_key: '', title_field: '', definition_fields: [], alias_fields: [], keyword_fields: [] },
  },
  {
    id: 'retrieval_build', kind: 'retrieval.build', label: '构建混合检索索引', suggestedId: 'build_index',
    description: '为固定数据边界构建精准与语义混合检索快照。',
    inputs: [input('dataset', '待索引数据集', 'dataset_boundary')],
    outputs: [input('snapshot', '检索快照', 'retrieval_snapshot')], defaultConfig: { retrieval_profile_id: '' },
  },
  {
    id: 'agent_analyze', kind: 'agent.analyze', label: '智能检索分析', suggestedId: 'analyze',
    description: '检索权威知识并按照分析规则生成结构化结果。',
    inputs: [input('knowledge', '知识检索快照', 'retrieval_snapshot')],
    outputs: [input('analysis', '分析结果', 'analysis_result')],
    defaultConfig: {
      analysis_profile_id: '',
      knowledge_sources: { knowledge: { name: 'business_knowledge', description: '本流程使用的权威业务知识' } },
    },
  },
  {
    id: 'human_review_analysis', kind: 'human.review', label: '人工确认分析', suggestedId: 'review_analysis',
    description: '暂停流程，由业务人员确认分析结果后继续。',
    inputs: [input('analysis', '分析结果', 'analysis_result')],
    outputs: [input('analysis', '已确认分析结果', 'analysis_result')], defaultConfig: {},
  },
  {
    id: 'analysis_publish', kind: 'data.analysis_publish', label: '发布分析记录', suggestedId: 'publish_analysis',
    description: '将分析结果中的记录数组追加到目标数据集。',
    inputs: [input('analysis', '分析结果', 'analysis_result'), input('target', '目标数据集', 'dataset_boundary')],
    outputs: [input('batch', '数据批次', 'dataset_batch')], defaultConfig: { records_path: 'records' },
  },
  {
    id: 'artifact_render', kind: 'artifact.render', label: '生成业务制品', suggestedId: 'render',
    description: '将分析结果固化为 Markdown 或 JSON 业务制品。',
    inputs: [input('analysis', '分析结果', 'analysis_result')],
    outputs: [input('artifact', '业务制品', 'artifact')],
    defaultConfig: { name: '分析报告', kind: 'markdown', content_path: 'report' },
  },
  {
    id: 'graph_build', kind: 'graph.build', label: '生成图谱清单', suggestedId: 'build_graph',
    description: '把节点、关系批次与分析来源固化为可追溯图谱清单。',
    inputs: [
      input('analysis', '分析结果', 'analysis_result'), input('nodes_batch', '节点数据批次', 'dataset_batch'),
      input('edges_batch', '关系数据批次', 'dataset_batch'),
    ],
    outputs: [input('manifest', '图谱清单', 'artifact')], defaultConfig: { name: '知识图谱' },
  },
]

export interface WorkflowBlockAvailability {
  canAdd: boolean
  /** 当前已有来源，无需创建任务输入的端口。 */
  connectedPorts: WorkflowBlockPort[]
  /** 允许在添加步骤时一起创建任务输入的端口。 */
  taskInputPorts: WorkflowBlockPort[]
  /** 只能由上游 Executor 产出，当前却缺失来源的端口。 */
  missingUpstreamPorts: WorkflowBlockPort[]
}

export function canBindTaskInput(resourceType: string) {
  return taskBindableResourceTypes.has(resourceType)
}

/** 根据插入点之前已有的资源类型，判定某个步骤是否能合法加入。 */
export function workflowBlockAvailability(
  block: WorkflowBlock,
  availableResourceTypes: ReadonlySet<string>,
): WorkflowBlockAvailability {
  const connectedPorts: WorkflowBlockPort[] = []
  const taskInputPorts: WorkflowBlockPort[] = []
  const missingUpstreamPorts: WorkflowBlockPort[] = []
  for (const port of block.inputs) {
    if (availableResourceTypes.has(port.resourceType)) connectedPorts.push(port)
    else if (canBindTaskInput(port.resourceType)) taskInputPorts.push(port)
    else missingUpstreamPorts.push(port)
  }
  return {
    canAdd: missingUpstreamPorts.length === 0,
    connectedPorts,
    taskInputPorts,
    missingUpstreamPorts,
  }
}

export function producerBlocks(resourceType: string) {
  return WORKFLOW_BLOCKS.filter((block) => block.outputs.some((port) => port.resourceType === resourceType))
}

export function workflowBlockForStep(step: V2StepDefinition): WorkflowBlock | undefined {
  return WORKFLOW_BLOCKS.find((block) => block.kind === step.kind &&
    block.inputs.every((port) => Object.hasOwn(step.inputs ?? {}, port.name)) &&
    block.outputs.every((port) => step.outputs?.[port.name] === port.resourceType))
}

export function createWorkflowStep(block: WorkflowBlock, id: string): V2StepDefinition {
  return {
    id,
    name: block.label,
    kind: block.kind,
    inputs: Object.fromEntries(block.inputs.map((port) => [port.name, ''])),
    outputs: Object.fromEntries(block.outputs.map((port) => [port.name, port.resourceType])),
    config: structuredClone(block.defaultConfig),
  }
}

export function uniqueStepId(suggested: string, steps: V2StepDefinition[]) {
  if (!steps.some((step) => step.id === suggested)) return suggested
  let suffix = 2
  while (steps.some((step) => step.id === `${suggested}_${suffix}`)) suffix += 1
  return `${suggested}_${suffix}`
}

export function referencedStepIds(inputs: Record<string, string> | undefined) {
  const result = new Set<string>()
  for (const reference of Object.values(inputs ?? {})) {
    const match = /^\$step\.([a-z][a-z0-9_]*)\./.exec(reference)
    if (match) result.add(match[1])
  }
  return [...result]
}
