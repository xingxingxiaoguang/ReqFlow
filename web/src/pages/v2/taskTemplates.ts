import type { DefinitionInput } from '../../api/v2/catalog'

export type NoCodeTemplateId = 'data_clean_import' | 'retrieval_index' | 'bug_analysis' | 'product_spec_generate' | 'knowledge_graph_build'

export interface TemplateValues {
  analysisProfileId?: string
  extractionProfileId?: string
  retrievalProfileId?: string
}

export const NO_CODE_TEMPLATES: Array<{ id: NoCodeTemplateId; name: string; description: string; tone: string }> = [
  { id: 'data_clean_import', name: '数据清洗入库', description: '文件解析、结构化抽取、清洗校验、人工审核并原子发布到数据集。', tone: '#0f766e' },
  { id: 'retrieval_index', name: '精准 + 语义索引', description: '对数据集固定边界构建混合检索快照，策略由检索 Profile 管理。', tone: '#2563eb' },
  { id: 'bug_analysis', name: 'Bug 分析', description: '检索产品知识，生成结构化分析记录和可交付报告。', tone: '#dc2626' },
  { id: 'product_spec_generate', name: '产品方案生成', description: '基于检索快照生成结构化产品方案，审核后固化为制品。', tone: '#7c3aed' },
  { id: 'knowledge_graph_build', name: '知识图谱构建', description: '从知识快照提取节点和关系，分别发布并生成图谱 Manifest。', tone: '#b45309' },
]

const port = (resource_type: string, description: string) => ({ resource_type, required: true, description })

export function createDefinition(template: NoCodeTemplateId, name: string, values: TemplateValues): DefinitionInput {
  const common = {
    key: `${template}_${Date.now()}`,
    name,
    description: `由可编辑流程模板创建：${name}`,
    status: 'active' as const,
  }
  if (template === 'data_clean_import') return {
    ...common,
    input_ports: { assets: port('asset_set', '待解析文件集'), target: port('dataset', '写入目标数据集') },
    output_ports: { batch: port('dataset_batch', '已提交数据批次') },
    output_bindings: { batch: '$step.publish.batch' },
    steps: [
      { id: 'parse', name: '解析文件', kind: 'source.parse', inputs: { assets: '$task.assets' }, outputs: { documents: 'parsed_documents' }, config: {} },
      { id: 'extract', name: '结构化抽取', kind: 'document.extract', depends_on: ['parse'], inputs: { documents: '$step.parse.documents' }, outputs: { drafts: 'record_drafts' }, config: { extraction_profile_id: values.extractionProfileId } },
      { id: 'transform', name: '确定性清洗', kind: 'data.transform', depends_on: ['extract'], inputs: { drafts: '$step.extract.drafts' }, outputs: { records: 'transformed_records' }, config: {} },
      { id: 'validate', name: 'Schema 与业务校验', kind: 'data.validate', depends_on: ['transform'], inputs: { records: '$step.transform.records', dataset: '$task.target' }, outputs: { validation: 'validation_results' }, config: {} },
      { id: 'review', name: '人工审核', kind: 'human.review', depends_on: ['validate'], inputs: { validation: '$step.validate.validation' }, outputs: { approved: 'approved_records' }, config: { allow_edit: true } },
      { id: 'publish', name: '原子发布', kind: 'data.publish', depends_on: ['review'], inputs: { approved: '$step.review.approved' }, outputs: { batch: 'dataset_batch', dataset: 'dataset_boundary' }, config: {} },
    ],
  }
  if (template === 'retrieval_index') return {
    ...common,
    input_ports: { dataset: port('dataset_boundary', '需要建立索引的数据集固定边界') },
    output_ports: { snapshot: port('retrieval_snapshot', '可复现检索快照') },
    output_bindings: { snapshot: '$step.build.snapshot' },
    steps: [{ id: 'build', name: '构建混合检索索引', kind: 'retrieval.build', inputs: { dataset: '$task.dataset' }, outputs: { snapshot: 'retrieval_snapshot' }, config: { retrieval_profile_id: values.retrievalProfileId } }],
  }
  const analyze = {
    id: 'analyze', name: '知识检索与结构化分析', kind: 'knowledge.analyze',
    inputs: { knowledge: '$task.knowledge' }, outputs: { analysis: 'analysis_result' },
    config: { analysis_profile_id: values.analysisProfileId, knowledge_sources: { knowledge: { name: 'business_knowledge', description: '本任务的权威业务知识' } } },
  }
  const review = {
    id: 'review', name: '人工确认分析结果', kind: 'human.review', depends_on: ['analyze'],
    inputs: { analysis: '$step.analyze.analysis' }, outputs: { analysis: 'analysis_result' }, config: {},
  }
  if (template === 'product_spec_generate') return {
    ...common,
    input_ports: { knowledge: port('retrieval_snapshot', '产品与需求知识快照') },
    output_ports: { artifact: port('artifact', '产品方案制品') },
    output_bindings: { artifact: '$step.render.artifact' },
    steps: [analyze, review, { id: 'render', name: '固化产品方案', kind: 'artifact.render', depends_on: ['review'], inputs: { analysis: '$step.review.analysis' }, outputs: { artifact: 'artifact' }, config: { name: name, kind: 'markdown', content_path: 'report' } }],
  }
  if (template === 'bug_analysis') return {
    ...common,
    input_ports: { knowledge: port('retrieval_snapshot', '产品与需求知识快照'), target: port('dataset_boundary', 'Bug 分析记录数据集') },
    output_ports: { batch: port('dataset_batch', '分析记录批次'), artifact: port('artifact', 'Bug 分析报告') },
    output_bindings: { batch: '$step.publish.batch', artifact: '$step.render.artifact' },
    steps: [analyze, review,
      { id: 'publish', name: '发布分析记录', kind: 'data.analysis_publish', depends_on: ['review'], inputs: { analysis: '$step.review.analysis', target: '$task.target' }, outputs: { batch: 'dataset_batch' }, config: { records_path: 'records' } },
      { id: 'render', name: '固化分析报告', kind: 'artifact.render', depends_on: ['review'], inputs: { analysis: '$step.review.analysis' }, outputs: { artifact: 'artifact' }, config: { name, kind: 'markdown', content_path: 'report' } },
    ],
  }
  return {
    ...common,
    input_ports: {
      knowledge: port('retrieval_snapshot', '知识快照'), nodes_target: port('dataset_boundary', '图谱节点数据集'),
      edges_target: port('dataset_boundary', '图谱关系数据集'),
    },
    output_ports: { nodes_batch: port('dataset_batch', '节点批次'), edges_batch: port('dataset_batch', '关系批次'), manifest: port('artifact', '图谱 Manifest') },
    output_bindings: { nodes_batch: '$step.publish_nodes.batch', edges_batch: '$step.publish_edges.batch', manifest: '$step.graph.manifest' },
    steps: [analyze, review,
      { id: 'publish_nodes', name: '发布图谱节点', kind: 'data.analysis_publish', depends_on: ['review'], inputs: { analysis: '$step.review.analysis', target: '$task.nodes_target' }, outputs: { batch: 'dataset_batch' }, config: { records_path: 'nodes' } },
      { id: 'publish_edges', name: '发布图谱关系', kind: 'data.analysis_publish', depends_on: ['review'], inputs: { analysis: '$step.review.analysis', target: '$task.edges_target' }, outputs: { batch: 'dataset_batch' }, config: { records_path: 'edges' } },
      { id: 'graph', name: '生成图谱 Manifest', kind: 'graph.build', depends_on: ['publish_nodes', 'publish_edges'], inputs: { analysis: '$step.review.analysis', nodes_batch: '$step.publish_nodes.batch', edges_batch: '$step.publish_edges.batch' }, outputs: { manifest: 'artifact' }, config: { name } },
    ],
  }
}
