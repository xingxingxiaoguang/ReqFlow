import { useMemo, useState } from 'react'
import { App, Button, Card, Col, Form, Input, Result, Row, Select, Space, Steps, Tag, Typography } from 'antd'
import { ArrowLeftOutlined, RocketOutlined } from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { useNavigate } from 'react-router-dom'
import { v2CatalogApi } from '../../api/v2/catalog'
import { v2TasksApi } from '../../api/v2/tasks'
import { createDefinition, NO_CODE_TEMPLATES, type NoCodeTemplateId } from './taskTemplates'

const { Paragraph, Text, Title } = Typography

interface FormValues {
  title: string
  assetSetId?: string
  targetDatasetId?: string
  nodesDatasetId?: string
  edgesDatasetId?: string
  retrievalSnapshotId?: string
  analysisProfileId?: string
  extractionProfileId?: string
  retrievalProfileId?: string
}

export default function NoCodeTaskNew() {
  const navigate = useNavigate()
  const { message } = App.useApp()
  const [template, setTemplate] = useState<NoCodeTemplateId>()
  const [creating, setCreating] = useState(false)
  const [form] = Form.useForm<FormValues>()
  const datasets = useQuery({ queryKey: ['v2-datasets'], queryFn: () => v2CatalogApi.listDatasets({ status: 'active' }) })
  const assetSets = useQuery({ queryKey: ['v2-asset-sets'], queryFn: v2CatalogApi.listAssetSets })
  const extractionProfiles = useQuery({ queryKey: ['v2-extraction-profiles'], queryFn: v2CatalogApi.listExtractionProfiles })
  const retrievalProfiles = useQuery({ queryKey: ['v2-retrieval-profiles'], queryFn: v2CatalogApi.listRetrievalProfiles })
  const retrievalSnapshots = useQuery({ queryKey: ['v2-retrieval-snapshots'], queryFn: v2CatalogApi.listRetrievalSnapshots })
  const analysisProfiles = useQuery({ queryKey: ['v2-analysis-profiles'], queryFn: v2CatalogApi.listAnalysisProfiles })
  const selected = useMemo(() => NO_CODE_TEMPLATES.find((item) => item.id === template), [template])
  const datasetOptions = (datasets.data?.datasets ?? []).map((item) => ({ value: item.id, label: `${item.name} · ${item.purpose} · ${item.item_count} 条` }))

  const create = async (values: FormValues) => {
    if (!template) return
    setCreating(true)
    try {
      const definition = createDefinition(template, values.title, values)
      const saved = await v2CatalogApi.createDefinition(definition)
      const bindings = template === 'data_clean_import'
        ? [{ port_name: 'assets', resource_type: 'asset_set', resource_id: values.assetSetId! }, { port_name: 'target', resource_type: 'dataset', resource_id: values.targetDatasetId! }]
        : template === 'retrieval_index'
          ? [{ port_name: 'dataset', resource_type: 'dataset_boundary', resource_id: values.targetDatasetId! }]
          : template === 'knowledge_graph_build'
            ? [{ port_name: 'knowledge', resource_type: 'retrieval_snapshot', resource_id: values.retrievalSnapshotId! }, { port_name: 'nodes_target', resource_type: 'dataset_boundary', resource_id: values.nodesDatasetId! }, { port_name: 'edges_target', resource_type: 'dataset_boundary', resource_id: values.edgesDatasetId! }]
            : template === 'bug_analysis'
              ? [{ port_name: 'knowledge', resource_type: 'retrieval_snapshot', resource_id: values.retrievalSnapshotId! }, { port_name: 'target', resource_type: 'dataset_boundary', resource_id: values.targetDatasetId! }]
              : [{ port_name: 'knowledge', resource_type: 'retrieval_snapshot', resource_id: values.retrievalSnapshotId! }]
      const created = await v2TasksApi.create({ definition_id: saved.definition.id, title: values.title, bindings })
      message.success('无代码任务已创建，资源边界已固化')
      navigate(`/tasks/${created.task.id}`)
    } catch (error) {
      message.error((error as Error).message)
    } finally {
      setCreating(false)
    }
  }

  return (
    <Space direction="vertical" size={18} style={{ width: '100%' }}>
      <Card>
        <Space align="start">
          <Button type="text" icon={<ArrowLeftOutlined />} onClick={() => navigate('/tasks')} />
          <div><Title level={3} style={{ margin: 0 }}>创建无代码任务</Title><Paragraph type="secondary" style={{ marginBottom: 0 }}>选择业务模板、绑定版本化资源，系统自动生成并冻结 TaskDefinition。</Paragraph></div>
        </Space>
      </Card>
      <Card title="1. 选择任务模板">
        <Row gutter={[16, 16]}>
          {NO_CODE_TEMPLATES.map((item) => (
            <Col xs={24} md={12} xl={8} key={item.id}>
              <Card hoverable onClick={() => { setTemplate(item.id); form.setFieldValue('title', item.name) }} style={{ height: '100%', borderColor: template === item.id ? item.tone : undefined, boxShadow: template === item.id ? `0 0 0 2px ${item.tone}22` : undefined }}>
                <Space direction="vertical"><Tag color={item.tone}>V2 SOP</Tag><Text strong style={{ fontSize: 17 }}>{item.name}</Text><Text type="secondary">{item.description}</Text></Space>
              </Card>
            </Col>
          ))}
        </Row>
      </Card>
      {!template ? <Result status="info" title="先选择一种任务模板" subTitle="这里没有旧的内置任务流程；每次创建都会得到可审计、不可变的 V2 流程定义。" /> : (
        <Card title={`2. 配置 ${selected?.name}`} extra={<Steps size="small" current={1} items={[{ title: '模板' }, { title: '资源绑定' }, { title: '创建' }]} style={{ width: 420 }} />}>
          <Form form={form} layout="vertical" onFinish={create} initialValues={{ title: selected?.name }}>
            <Form.Item name="title" label="任务名称" rules={[{ required: true }]}><Input maxLength={200} /></Form.Item>
            {template === 'data_clean_import' && <>
              <Form.Item name="assetSetId" label="文件集" rules={[{ required: true }]}><Select showSearch optionFilterProp="label" options={(assetSets.data?.asset_sets ?? []).map((item) => ({ value: item.id, label: item.name }))} placeholder="先在元数据与资源页建立文件集" /></Form.Item>
              <Form.Item name="extractionProfileId" label="抽取 Profile" rules={[{ required: true }]}><Select showSearch optionFilterProp="label" options={(extractionProfiles.data?.extraction_profiles ?? []).map((item) => ({ value: item.id, label: item.name }))} /></Form.Item>
              <Form.Item name="targetDatasetId" label="目标数据集" rules={[{ required: true }]}><Select showSearch optionFilterProp="label" options={datasetOptions} /></Form.Item>
            </>}
            {template === 'retrieval_index' && <>
              <Form.Item name="targetDatasetId" label="待索引数据集" rules={[{ required: true }]}><Select showSearch optionFilterProp="label" options={datasetOptions} /></Form.Item>
              <Form.Item name="retrievalProfileId" label="检索策略 Profile" rules={[{ required: true }]}><Select showSearch optionFilterProp="label" options={(retrievalProfiles.data?.retrieval_profiles ?? []).map((item) => ({ value: item.id, label: item.name }))} /></Form.Item>
            </>}
            {['bug_analysis', 'product_spec_generate', 'knowledge_graph_build'].includes(template) && <>
              <Form.Item name="retrievalSnapshotId" label="知识检索快照" rules={[{ required: true }]}><Select showSearch optionFilterProp="label" options={(retrievalSnapshots.data?.retrieval_snapshots ?? []).map((item) => ({ value: item.id, label: `${item.id.slice(0, 8)} · seq ${item.source_seq}` }))} /></Form.Item>
              <Form.Item name="analysisProfileId" label="分析 Profile" rules={[{ required: true }]}><Select showSearch optionFilterProp="label" options={(analysisProfiles.data?.analysis_profiles ?? []).map((item) => ({ value: item.id, label: item.name }))} /></Form.Item>
            </>}
            {template === 'bug_analysis' && <Form.Item name="targetDatasetId" label="分析记录数据集" rules={[{ required: true }]}><Select showSearch optionFilterProp="label" options={datasetOptions} /></Form.Item>}
            {template === 'knowledge_graph_build' && <Row gutter={16}><Col span={12}><Form.Item name="nodesDatasetId" label="节点数据集" rules={[{ required: true }]}><Select showSearch optionFilterProp="label" options={datasetOptions} /></Form.Item></Col><Col span={12}><Form.Item name="edgesDatasetId" label="关系数据集" rules={[{ required: true }]}><Select showSearch optionFilterProp="label" options={datasetOptions} /></Form.Item></Col></Row>}
            <Button type="primary" size="large" icon={<RocketOutlined />} htmlType="submit" loading={creating}>创建任务</Button>
          </Form>
        </Card>
      )}
    </Space>
  )
}
