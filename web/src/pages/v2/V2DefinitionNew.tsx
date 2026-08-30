import { useEffect, useMemo, useState, type ReactNode } from 'react'
import {
  Alert, App, Button, Card, Checkbox, Col, Divider, Empty, Form, Input,
  Modal, Popconfirm, Row, Select, Space, Tag, Tooltip, Typography,
} from 'antd'
import {
  ArrowLeftOutlined, CheckCircleOutlined, DeleteOutlined, EditOutlined,
  PlusOutlined, SaveOutlined, WarningOutlined,
} from '@ant-design/icons'
import { useQuery } from '@tanstack/react-query'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { v2CatalogApi, type DefinitionInput } from '../../api/v2/catalog'
import type { V2StepDefinition } from '../../api/v2/types'
import { createDefinition, NO_CODE_TEMPLATES, type NoCodeTemplateId } from './taskTemplates'
import {
  canBindTaskInput, createWorkflowStep, producerBlocks, referencedStepIds,
  RESOURCE_TYPE_LABEL, uniqueStepId, workflowBlockAvailability, workflowBlockForStep,
  WORKFLOW_BLOCKS, type WorkflowBlock, type WorkflowBlockPort,
} from './workflowBlocks'
import {
  getLocalWorkflowDraft, listLocalWorkflowDrafts, removeLocalWorkflowDraft,
  saveLocalWorkflowDraft, type LocalWorkflowDraft,
} from './workflowDrafts'
import EmbeddedResourceCreate, {
  type EmbeddedResourceKind,
} from './EmbeddedResourceCreate'

const { Paragraph, Text, Title } = Typography
const identifierPattern = /^[a-z][a-z0-9_]*$/
const CREATE_TASK_INPUT = '__create_task_input__'

interface InputPortForm {
  name: string
  description: string
  resource_type: string
  required: boolean
}

interface InputTarget {
  stepIndex: number
  port: WorkflowBlockPort
}

interface InsertRequest {
  index: number
  resourceType?: string
}

interface SourceOption {
  value: string
  label: string
  source: 'task' | 'step'
}

interface InsertStepValues {
  name: string
  sources: Record<string, string>
  new_inputs?: Record<string, { name: string; description: string }>
}

interface DefinitionIssue {
  stepId?: string
  message: string
}

function blankDefinition(): DefinitionInput {
  return {
    key: `workflow_${Date.now()}`,
    name: '未命名流程',
    description: '',
    status: 'draft',
    input_ports: {},
    output_ports: {},
    output_bindings: {},
    steps: [],
  }
}

function cloneDefinition(value: DefinitionInput): DefinitionInput {
  return structuredClone(value)
}

function newDraftID() {
  return crypto.randomUUID()
}

function sourceOptions(draft: DefinitionInput, stepIndex: number, resourceType: string): SourceOption[] {
  const stepSources = draft.steps.slice(0, stepIndex).reverse().flatMap((step) =>
    Object.entries(step.outputs ?? {})
      .filter(([, type]) => type === resourceType)
      .map(([port]) => ({
        value: `$step.${step.id}.${port}`,
        label: `上游步骤 · ${step.name} / ${port}`,
        source: 'step' as const,
      })),
  )
  const taskSources = Object.entries(draft.input_ports)
    .filter(([, port]) => port.resource_type === resourceType)
    .map(([name, port]) => ({
      value: `$task.${name}`,
      label: `任务输入 · ${port.description || name}`,
      source: 'task' as const,
    }))
  return [...stepSources, ...taskSources]
}

function availableResourceTypes(draft: DefinitionInput, stepIndex: number) {
  const result = new Set(Object.values(draft.input_ports).map((port) => port.resource_type))
  for (const step of draft.steps.slice(0, stepIndex)) {
    for (const type of Object.values(step.outputs ?? {})) result.add(type)
  }
  return result
}

function uniqueInputName(suggested: string, ports: DefinitionInput['input_ports']) {
  if (!ports[suggested]) return suggested
  let suffix = 2
  while (ports[`${suggested}_${suffix}`]) suffix += 1
  return `${suggested}_${suffix}`
}

function referenceType(draft: DefinitionInput, stepIndex: number, reference: string) {
  const taskMatch = /^\$task\.([^.]+)$/.exec(reference)
  if (taskMatch) return draft.input_ports[taskMatch[1]]?.resource_type
  const stepMatch = /^\$step\.([^.]+)\.([^.]+)$/.exec(reference)
  if (!stepMatch) return undefined
  const sourceIndex = draft.steps.findIndex((step) => step.id === stepMatch[1])
  if (sourceIndex < 0 || sourceIndex >= stepIndex) return undefined
  return draft.steps[sourceIndex].outputs?.[stepMatch[2]]
}

function collectDefinitionIssues(draft: DefinitionInput): DefinitionIssue[] {
  const issues: DefinitionIssue[] = []
  if (!draft.name.trim()) issues.push({ message: '请填写流程名称' })
  if (!identifierPattern.test(draft.key)) issues.push({ message: '流程编码必须以小写字母开头，只能包含小写字母、数字和下划线' })
  if (draft.steps.length === 0) issues.push({ message: '流程至少需要一个步骤' })
  if (Object.keys(draft.output_bindings ?? {}).length === 0) issues.push({ message: '请至少选择一个流程产出' })

  for (const name of Object.keys(draft.input_ports)) {
    if (!identifierPattern.test(name)) issues.push({ message: `输入资源编码 ${name} 不合法` })
  }

  const seen = new Set<string>()
  draft.steps.forEach((step, stepIndex) => {
    if (!identifierPattern.test(step.id)) issues.push({ stepId: step.id, message: `步骤编码 ${step.id} 不合法` })
    if (seen.has(step.id)) issues.push({ stepId: step.id, message: `步骤编码 ${step.id} 重复` })
    seen.add(step.id)
    if (!step.name.trim()) issues.push({ stepId: step.id, message: `步骤 ${step.id} 缺少名称` })

    const block = workflowBlockForStep(step)
    if (!block) {
      issues.push({ stepId: step.id, message: `步骤「${step.name}」不匹配已注册的 Executor 端口合同` })
      return
    }
    for (const port of block.inputs) {
      const reference = step.inputs?.[port.name] ?? ''
      if (!reference) {
        issues.push({ stepId: step.id, message: `步骤「${step.name}」的“${port.label}”尚未连接数据来源` })
      } else if (referenceType(draft, stepIndex, reference) !== port.resourceType) {
        issues.push({ stepId: step.id, message: `步骤「${step.name}」的“${port.label}”来源不存在、类型不匹配或位于当前步骤之后` })
      }
    }
    if (step.kind === 'llm.extract' && !step.config?.extraction_profile_id) issues.push({ stepId: step.id, message: `步骤「${step.name}」尚未选择抽取规则` })
    if (step.kind === 'retrieval.build' && !step.config?.retrieval_profile_id) issues.push({ stepId: step.id, message: `步骤「${step.name}」尚未选择检索策略` })
    if (step.kind === 'agent.analyze' && !step.config?.analysis_profile_id) issues.push({ stepId: step.id, message: `步骤「${step.name}」尚未选择分析规则` })
    if (step.kind === 'data.query_derive' && (
      !step.config?.pipeline_key || !step.config?.title_field ||
      !(step.config?.definition_fields as string[] | undefined)?.length
    )) issues.push({ stepId: step.id, message: `步骤「${step.name}」的增量派生规则不完整` })
    if ((step.kind === 'artifact.render' || step.kind === 'graph.build') && !step.config?.name) {
      issues.push({ stepId: step.id, message: `步骤「${step.name}」缺少产出名称` })
    }
  })

  for (const [name, output] of Object.entries(draft.output_ports ?? {})) {
    const reference = draft.output_bindings?.[name] ?? ''
    const match = /^\$step\.([^.]+)\.([^.]+)$/.exec(reference)
    const step = match ? draft.steps.find((item) => item.id === match[1]) : undefined
    if (!step || step.outputs?.[match?.[2] ?? ''] !== output.resource_type) {
      issues.push({ message: `流程产出「${output.description || name}」的绑定已失效` })
    }
  }
  return issues
}

export default function V2DefinitionNew() {
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const { message } = App.useApp()
  const [initialLocalDraft] = useState(() => {
    const id = searchParams.get('draft')
    return id ? getLocalWorkflowDraft(id) : undefined
  })
  const [draft, setDraft] = useState<DefinitionInput | undefined>(() => initialLocalDraft?.definition)
  const [draftId, setDraftId] = useState<string | undefined>(() => initialLocalDraft?.id)
  const [draftCreatedAt, setDraftCreatedAt] = useState<string | undefined>(() => initialLocalDraft?.created_at)
  const [savedAt, setSavedAt] = useState<string | undefined>(() => initialLocalDraft?.saved_at)
  const [localDrafts, setLocalDrafts] = useState(listLocalWorkflowDrafts)
  const [dirty, setDirty] = useState(false)
  const [saving, setSaving] = useState(false)
  const [inputTarget, setInputTarget] = useState<InputTarget>()
  const [insertRequest, setInsertRequest] = useState<InsertRequest>()
  const [profileCreator, setProfileCreator] = useState<{
    kind: Extract<EmbeddedResourceKind, 'analysis' | 'extraction' | 'retrieval'>
    stepIndex: number
  }>()
  const [inputForm] = Form.useForm<InputPortForm>()
  const extractionProfiles = useQuery({ queryKey: ['v2-extraction-profiles'], queryFn: v2CatalogApi.listExtractionProfiles })
  const retrievalProfiles = useQuery({ queryKey: ['v2-retrieval-profiles'], queryFn: v2CatalogApi.listRetrievalProfiles })
  const analysisProfiles = useQuery({ queryKey: ['v2-analysis-profiles'], queryFn: v2CatalogApi.listAnalysisProfiles })
  const issues = useMemo(() => draft ? collectDefinitionIssues(draft) : [], [draft])

  useEffect(() => {
    const beforeUnload = (event: BeforeUnloadEvent) => {
      if (!dirty) return
      event.preventDefault()
      event.returnValue = ''
    }
    window.addEventListener('beforeunload', beforeUnload)
    return () => window.removeEventListener('beforeunload', beforeUnload)
  }, [dirty])

  const replaceDraft = (next: DefinitionInput, id = newDraftID()) => {
    setDraft(cloneDefinition({ ...next, status: 'draft' }))
    setDraftId(id)
    setDraftCreatedAt(new Date().toISOString())
    setSavedAt(undefined)
    setDirty(true)
  }

  const chooseStarter = (template?: NoCodeTemplateId) => {
    if (!template) {
      replaceDraft(blankDefinition())
      return
    }
    const metadata = NO_CODE_TEMPLATES.find((item) => item.id === template)!
    replaceDraft(createDefinition(template, metadata.name, {}))
  }

  const continueDraft = (saved: LocalWorkflowDraft) => {
    setDraft(cloneDefinition(saved.definition))
    setDraftId(saved.id)
    setDraftCreatedAt(saved.created_at)
    setSavedAt(saved.saved_at)
    setDirty(false)
  }

  const updateDraft = (patch: Partial<DefinitionInput>) => {
    setDirty(true)
    setDraft((current) => current ? { ...current, ...patch } : current)
  }

  const updateStep = (index: number, patch: Partial<V2StepDefinition>) => {
    if (!draft) return
    const steps = draft.steps.map((step, itemIndex) => itemIndex === index ? { ...step, ...patch } : step)
    const changed = steps[index]
    steps[index] = { ...changed, depends_on: referencedStepIds(changed.inputs) }
    updateDraft({ steps })
  }

  const updateStepConfig = (index: number, key: string, value: unknown) => {
    if (!draft) return
    const step = draft.steps[index]
    updateStep(index, { config: { ...(step.config ?? {}), [key]: value } })
  }

  const openInputCreator = (stepIndex: number, port: WorkflowBlockPort) => {
    if (!draft) return
    inputForm.setFieldsValue({
      name: uniqueInputName(port.name, draft.input_ports),
      description: port.label,
      resource_type: port.resourceType,
      required: true,
    })
    setInputTarget({ stepIndex, port })
  }

  const addInputPort = async () => {
    if (!draft || !inputTarget) return
    const values = await inputForm.validateFields()
    if (draft.input_ports[values.name]) {
      message.error('输入资源编码不能重复')
      return
    }
    const reference = `$task.${values.name}`
    setDirty(true)
    setDraft((current) => {
      if (!current) return current
      const steps = current.steps.map((step, index) => {
        if (index !== inputTarget.stepIndex) return step
        const inputs = { ...(step.inputs ?? {}), [inputTarget.port.name]: reference }
        return { ...step, inputs, depends_on: referencedStepIds(inputs) }
      })
      return {
        ...current,
        input_ports: {
          ...current.input_ports,
          [values.name]: {
            resource_type: inputTarget.port.resourceType,
            required: values.required,
            description: values.description.trim(),
          },
        },
        steps,
      }
    })
    setInputTarget(undefined)
    inputForm.resetFields()
    message.success('已创建任务输入并连接到当前步骤')
  }

  const removeInputPort = (name: string) => {
    if (!draft) return
    const reference = `$task.${name}`
    if (draft.steps.some((step) => Object.values(step.inputs ?? {}).includes(reference))) {
      message.warning('该资源已连接到流程步骤，请先调整步骤的数据来源')
      return
    }
    const next = { ...draft.input_ports }
    delete next[name]
    updateDraft({ input_ports: next })
  }

  const insertStep = (block: WorkflowBlock, values: InsertStepValues) => {
    if (!draft || !insertRequest) return
    const inputPorts = { ...draft.input_ports }
    const step = createWorkflowStep(block, uniqueStepId(block.suggestedId, draft.steps))
    step.name = values.name.trim()
    for (const port of block.inputs) {
      const source = values.sources[port.name]
      if (source === CREATE_TASK_INPUT) {
        const input = values.new_inputs?.[port.name]
        if (!input || inputPorts[input.name]) {
          message.error(`请检查「${port.label}」的新建任务输入`)
          return
        }
        inputPorts[input.name] = {
          resource_type: port.resourceType,
          required: true,
          description: input.description.trim(),
        }
        step.inputs![port.name] = `$task.${input.name}`
      } else {
        step.inputs![port.name] = source
      }
    }
    step.depends_on = referencedStepIds(step.inputs)
    const steps = [...draft.steps]
    steps.splice(insertRequest.index, 0, step)
    updateDraft({ input_ports: inputPorts, steps })
    setInsertRequest(undefined)
    message.success(`已在第 ${insertRequest.index + 1} 个位置插入「${step.name}」`)
  }

  const removeStep = (stepId: string) => {
    if (!draft) return
    const prefix = `$step.${stepId}.`
    const steps = draft.steps.filter((step) => step.id !== stepId).map((step) => {
      const inputs = Object.fromEntries(Object.entries(step.inputs ?? {}).map(([name, reference]) => [
        name, reference.startsWith(prefix) ? '' : reference,
      ]))
      return { ...step, inputs, depends_on: referencedStepIds(inputs) }
    })
    const outputBindings = Object.fromEntries(Object.entries(draft.output_bindings ?? {})
      .filter(([, reference]) => !reference.startsWith(prefix)))
    const outputPorts = Object.fromEntries(Object.entries(draft.output_ports ?? {})
      .filter(([name]) => Object.hasOwn(outputBindings, name)))
    updateDraft({ steps, output_bindings: outputBindings, output_ports: outputPorts })
  }

  const exposedPort = (reference: string) => Object.entries(draft?.output_bindings ?? {})
    .find(([, binding]) => binding === reference)?.[0]

  const toggleOutput = (step: V2StepDefinition, port: string, resourceType: string, checked: boolean) => {
    if (!draft) return
    const reference = `$step.${step.id}.${port}`
    const outputPorts = { ...(draft.output_ports ?? {}) }
    const outputBindings = { ...(draft.output_bindings ?? {}) }
    const current = exposedPort(reference)
    if (!checked && current) {
      delete outputPorts[current]
      delete outputBindings[current]
    } else if (checked && !current) {
      let name = port
      if (outputPorts[name]) name = `${step.id}_${port}`
      outputPorts[name] = { resource_type: resourceType, description: `${step.name}的${RESOURCE_TYPE_LABEL[resourceType] ?? port}` }
      outputBindings[name] = reference
    }
    updateDraft({ output_ports: outputPorts, output_bindings: outputBindings })
  }

  const temporarySave = () => {
    if (!draft) return false
    const id = draftId ?? newDraftID()
    const normalized = {
      ...draft,
      status: 'draft' as const,
      steps: draft.steps.map((step) => ({ ...step, depends_on: referencedStepIds(step.inputs) })),
    }
    try {
      const saved = saveLocalWorkflowDraft(id, normalized, draftCreatedAt)
      setDraft(saved.definition)
      setDraftId(id)
      setDraftCreatedAt(saved.created_at)
      setSavedAt(saved.saved_at)
      setDirty(false)
      setLocalDrafts(listLocalWorkflowDrafts())
      message.success('流程已暂存到当前浏览器，可稍后继续编辑')
      return true
    } catch (error) {
      message.error(`暂存失败：${(error as Error).message}`)
      return false
    }
  }

  const publish = async () => {
    if (!draft) return
    const normalizedSteps = draft.steps.map((step) => ({ ...step, depends_on: referencedStepIds(step.inputs) }))
    const payload = {
      ...draft,
      name: draft.name.trim(),
      description: draft.description?.trim(),
      status: 'active' as const,
      steps: normalizedSteps,
    }
    const currentIssues = collectDefinitionIssues(payload)
    if (currentIssues.length) {
      message.error(`${currentIssues[0].message}${currentIssues.length > 1 ? `（还有 ${currentIssues.length - 1} 项待处理）` : ''}`)
      return
    }
    setSaving(true)
    try {
      const saved = await v2CatalogApi.createDefinition(payload)
      if (draftId) removeLocalWorkflowDraft(draftId)
      message.success(`流程「${saved.definition.name}」已发布，可从流程列表创建任务`)
      navigate('/definitions')
    } catch (error) {
      message.error((error as Error).message)
    } finally {
      setSaving(false)
    }
  }

  const leaveEditor = () => {
    if (!dirty) {
      setDraft(undefined)
      return
    }
    Modal.confirm({
      title: '当前修改尚未暂存',
      content: '先暂存到当前浏览器，下次可从“继续编辑”恢复。',
      okText: '暂存并返回',
      cancelText: '继续编辑',
      onOk: () => {
        if (temporarySave()) setDraft(undefined)
      },
    })
  }

  const discardLocalDraft = (id: string) => {
    removeLocalWorkflowDraft(id)
    setLocalDrafts(listLocalWorkflowDrafts())
    message.success('已删除本机暂存')
  }

  if (!draft) {
    return (
      <Space direction="vertical" size={18} style={{ width: '100%' }}>
        <Card>
          <Space align="start">
            <Button type="text" icon={<ArrowLeftOutlined />} onClick={() => navigate('/definitions')} />
            <div>
              <Title level={3} style={{ margin: 0 }}>创建流程定义</Title>
              <Paragraph type="secondary" style={{ marginBottom: 0 }}>从空白自由编排，或用模板作为可修改的起点。</Paragraph>
            </div>
          </Space>
        </Card>

        {localDrafts.length > 0 && <Card title={<Space><EditOutlined /><span>继续编辑</span><Tag color="gold">本机暂存 {localDrafts.length}</Tag></Space>}>
          <Row gutter={[12, 12]}>
            {localDrafts.map((saved) => <Col xs={24} lg={12} key={saved.id}>
              <Card size="small" title={saved.definition.name || '未命名流程'} extra={<Text type="secondary">{new Date(saved.saved_at).toLocaleString('zh-CN')}</Text>}>
                <Space direction="vertical" style={{ width: '100%' }}>
                  <Text type="secondary">{saved.definition.steps.length} 个步骤 · <Text code>{saved.definition.key}</Text></Text>
                  <Space>
                    <Button type="primary" size="small" icon={<EditOutlined />} onClick={() => continueDraft(saved)}>继续编辑</Button>
                    <Popconfirm title="删除这份本机暂存？" onConfirm={() => discardLocalDraft(saved.id)}>
                      <Button size="small" danger>删除</Button>
                    </Popconfirm>
                  </Space>
                </Space>
              </Card>
            </Col>)}
          </Row>
        </Card>}

        <Card title="选择编排起点">
          <Row gutter={[16, 16]}>
            <Col xs={24} md={12} xl={8}>
              <Card hoverable onClick={() => chooseStarter()} style={{ height: '100%', borderColor: '#1677ff' }}>
                <Space direction="vertical">
                  <Tag color="blue">自由编排</Tag>
                  <Text strong style={{ fontSize: 17 }}>从空白流程开始</Text>
                  <Text type="secondary">添加步骤时再选择或创建它所需的输入资源。</Text>
                </Space>
              </Card>
            </Col>
            {NO_CODE_TEMPLATES.map((item) => (
              <Col xs={24} md={12} xl={8} key={item.id}>
                <Card hoverable onClick={() => chooseStarter(item.id)} style={{ height: '100%' }}>
                  <Space direction="vertical">
                    <Tag color={item.tone}>可编辑模板</Tag>
                    <Text strong style={{ fontSize: 17 }}>{item.name}</Text>
                    <Text type="secondary">{item.description}</Text>
                  </Space>
                </Card>
              </Col>
            ))}
          </Row>
        </Card>
      </Space>
    )
  }

  return (
    <Space direction="vertical" size={18} style={{ width: '100%' }}>
      <Card>
        <Space align="start" style={{ width: '100%', justifyContent: 'space-between' }}>
          <Space align="start">
            <Button type="text" icon={<ArrowLeftOutlined />} onClick={leaveEditor} />
            <div>
              <Space wrap>
                <Title level={3} style={{ margin: 0 }}>流程编排器</Title>
                {dirty ? <Tag color="orange">有未暂存修改</Tag> : savedAt ? <Tag color="green">已暂存 {new Date(savedAt).toLocaleTimeString('zh-CN')}</Tag> : null}
              </Space>
              <Paragraph type="secondary" style={{ marginBottom: 0 }}>步骤的数据连接自动生成依赖；任务会在创建时冻结这份流程快照。</Paragraph>
            </div>
          </Space>
          <Space>
            <Button size="large" icon={<SaveOutlined />} onClick={temporarySave}>暂存</Button>
            <Button type="primary" size="large" icon={<CheckCircleOutlined />} loading={saving} onClick={publish}>发布流程</Button>
          </Space>
        </Space>
      </Card>

      <Alert
        type={issues.length ? 'warning' : 'success'}
        showIcon
        icon={issues.length ? <WarningOutlined /> : <CheckCircleOutlined />}
        message={issues.length ? `发布前还有 ${issues.length} 项需处理` : '流程连接与配置已就绪'}
        description={issues.length ? <Space direction="vertical" size={2}>{issues.slice(0, 3).map((issue, index) => <Text key={`${issue.stepId}-${index}`}>{index + 1}. {issue.message}</Text>)}{issues.length > 3 && <Text type="secondary">还有 {issues.length - 3} 项，对应步骤内已标出。</Text>}</Space> : '现在可以发布，或先暂存后稍后继续。'}
      />

      <Card title="1. 流程信息">
        <Row gutter={[16, 16]}>
          <Col xs={24} lg={12}>
            <Text strong>流程名称</Text>
            <Input value={draft.name} maxLength={120} onChange={(event) => updateDraft({ name: event.target.value })} style={{ marginTop: 8 }} />
          </Col>
          <Col xs={24} lg={12}>
            <Text strong>流程编码</Text>
            <Input value={draft.key} onChange={(event) => updateDraft({ key: event.target.value })} style={{ marginTop: 8 }} />
            <Text type="secondary" style={{ fontSize: 12 }}>用于审计与 API 定位，发布后不可修改。</Text>
          </Col>
          <Col span={24}>
            <Text strong>业务说明</Text>
            <Input.TextArea value={draft.description} rows={2} maxLength={500} onChange={(event) => updateDraft({ description: event.target.value })} style={{ marginTop: 8 }} />
          </Col>
        </Row>
      </Card>

      <Card
        title="2. 执行步骤与数据连接"
        extra={<Button type="primary" icon={<PlusOutlined />} onClick={() => setInsertRequest({ index: draft.steps.length })}>添加到末尾</Button>}
      >
        <Alert
          type="info"
          showIcon
          message="在任意连接点插入步骤"
          description="编排器只推荐当前能合法衔接的 Executor。外部资源可随步骤一起创建任务输入；中间数据必须来自必要的上游步骤。"
          style={{ marginBottom: 12 }}
        />

        {draft.steps.length === 0 ? <Empty description="从第一个步骤开始，所需任务输入会在添加过程中创建">
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setInsertRequest({ index: 0 })}>添加第一个步骤</Button>
        </Empty> : <>
          <InsertPoint label="在最前面插入" onClick={() => setInsertRequest({ index: 0 })} />
          <Space direction="vertical" size={0} style={{ width: '100%' }}>
            {draft.steps.map((step, index) => {
              const block = workflowBlockForStep(step)
              const stepIssues = issues.filter((issue) => issue.stepId === step.id)
              return <div key={step.id}>
                <Card
                  size="small"
                  style={{ borderColor: stepIssues.length ? '#faad14' : undefined }}
                  title={<Space wrap><Tag color={stepIssues.length ? 'orange' : 'blue'}>{index + 1}</Tag><Input value={step.name} maxLength={80} onChange={(event) => updateStep(index, { name: event.target.value })} style={{ width: 260 }} /><Tag>{block?.label ?? step.kind}</Tag>{stepIssues.length ? <Tag color="warning">待完善 {stepIssues.length}</Tag> : <Tag color="success">已连接</Tag>}</Space>}
                  extra={<Popconfirm title="删除该步骤？" description="下游连接会被清空，需要重新选择。" onConfirm={() => removeStep(step.id)}><Button type="text" danger icon={<DeleteOutlined />} /></Popconfirm>}
                >
                  <Paragraph type="secondary">{block?.description}</Paragraph>
                  <Row gutter={[16, 12]}>
                    {block?.inputs.map((port) => {
                      const options = sourceOptions(draft, index, port.resourceType)
                      const selected = step.inputs?.[port.name]
                      const suggested = producerBlocks(port.resourceType).filter((item) => item.id !== block?.id).map((item) => item.label).join('、')
                      return <Col xs={24} lg={12} key={port.name}>
                        <Space size={6} wrap><Text strong>{port.label}</Text><Tag>{RESOURCE_TYPE_LABEL[port.resourceType]}</Tag></Space>
                        <div style={{ display: 'flex', gap: 8, marginTop: 6 }}>
                          <Select
                            value={selected || undefined}
                            options={options}
                            placeholder={options.length ? '选择数据来源' : '尚无匹配来源'}
                            style={{ flex: 1 }}
                            status={!selected ? 'error' : undefined}
                            onChange={(value) => updateStep(index, { inputs: { ...(step.inputs ?? {}), [port.name]: value } })}
                          />
                          {canBindTaskInput(port.resourceType) && <Tooltip title="为每次任务创建一个需要绑定的输入槽位">
                            <Button icon={<PlusOutlined />} onClick={() => openInputCreator(index, port)}>新建输入</Button>
                          </Tooltip>}
                        </div>
                        {!selected && !canBindTaskInput(port.resourceType) && <Alert
                          type="error"
                          showIcon
                          style={{ marginTop: 8 }}
                          message={`缺少上游${RESOURCE_TYPE_LABEL[port.resourceType] ?? port.resourceType}`}
                          description={<Space wrap>{suggested && <Text type="secondary">建议先添加：{suggested}</Text>}<Button size="small" onClick={() => setInsertRequest({ index, resourceType: port.resourceType })}>补上游步骤</Button></Space>}
                        />}
                      </Col>
                    })}
                  </Row>
                  <StepConfigEditor
                    step={step}
                    extractionOptions={(extractionProfiles.data?.extraction_profiles ?? []).map((item) => ({ value: item.id, label: item.name }))}
                    retrievalOptions={(retrievalProfiles.data?.retrieval_profiles ?? []).map((item) => ({ value: item.id, label: item.name }))}
                    analysisOptions={(analysisProfiles.data?.analysis_profiles ?? []).map((item) => ({ value: item.id, label: item.name }))}
                    onChange={(key, value) => updateStepConfig(index, key, value)}
                    onCreate={(kind) => setProfileCreator({ kind, stepIndex: index })}
                  />
                  {stepIssues.length > 0 && <Alert type="warning" showIcon style={{ marginTop: 14 }} message={stepIssues[0].message} description={stepIssues.length > 1 ? `该步骤还有 ${stepIssues.length - 1} 项待完善` : undefined} />}
                  <Divider style={{ marginBlock: 16 }} />
                  <Space wrap>
                    <Text type="secondary">步骤产出：</Text>
                    {Object.entries(step.outputs ?? {}).map(([port, type]) => <Tag color="cyan" key={port}>{port} · {RESOURCE_TYPE_LABEL[type] ?? type}</Tag>)}
                    {!!step.depends_on?.length && <Text type="secondary">等待：{step.depends_on.map((id) => draft.steps.find((item) => item.id === id)?.name ?? id).join('、')}</Text>}
                  </Space>
                </Card>
                <InsertPoint label={index === draft.steps.length - 1 ? '继续添加步骤' : `在第 ${index + 1} 与 ${index + 2} 步之间插入`} onClick={() => setInsertRequest({ index: index + 1 })} />
              </div>
            })}
          </Space>
        </>}

        <Divider />
        <Card size="small" type="inner" title={<Space><span>任务输入汇总</span><Tag>{Object.keys(draft.input_ports).length}</Tag></Space>}>
          <Paragraph type="secondary">这些输入由步骤按需创建，业务人员会在每次创建任务时选择实际资源。</Paragraph>
          {Object.keys(draft.input_ports).length === 0 ? <Text type="secondary">尚无；添加需要外部资源的步骤时可直接创建。</Text> : <Space wrap>
            {Object.entries(draft.input_ports).map(([name, port]) => <Tag key={name} closable onClose={(event) => { event.preventDefault(); removeInputPort(name) }} color="geekblue">{port.description || name} · {RESOURCE_TYPE_LABEL[port.resource_type] ?? port.resource_type}</Tag>)}
          </Space>}
        </Card>
      </Card>

      <Card title="3. 对外提供的流程产出">
        <Paragraph type="secondary">勾选任务完成后需要保留给业务人员或后续流程使用的结果。中间数据无需全部暴露。</Paragraph>
        <Space direction="vertical" style={{ width: '100%' }}>
          {draft.steps.flatMap((step) => Object.entries(step.outputs ?? {}).map(([port, type]) => {
            const reference = `$step.${step.id}.${port}`
            const exposed = exposedPort(reference)
            return <Checkbox key={reference} checked={Boolean(exposed)} onChange={(event) => toggleOutput(step, port, type, event.target.checked)}>
              <Text strong>{step.name}</Text> · {RESOURCE_TYPE_LABEL[type] ?? type}{exposed && <Text type="secondary">（流程产出编码：{exposed}）</Text>}
            </Checkbox>
          }))}
          {draft.steps.length === 0 && <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="添加步骤后可选择流程产出" />}
        </Space>
      </Card>

      <Card>
        <Space style={{ width: '100%', justifyContent: 'space-between' }}>
          <Text type="secondary">可随时暂存不完整流程；只有连接和配置全部合法时才能发布。</Text>
          <Space>
            <Button size="large" icon={<SaveOutlined />} onClick={temporarySave}>暂存</Button>
            <Button type="primary" size="large" icon={<CheckCircleOutlined />} loading={saving} onClick={publish}>发布流程</Button>
          </Space>
        </Space>
      </Card>

      <StepInsertModal draft={draft} request={insertRequest} onCancel={() => setInsertRequest(undefined)} onInsert={insertStep} />

      <EmbeddedResourceCreate
        kind={profileCreator?.kind}
        onCancel={() => setProfileCreator(undefined)}
        onCreated={(resource) => {
          if (!profileCreator) return
          const configKey = {
            analysis: 'analysis_profile_id',
            extraction: 'extraction_profile_id',
            retrieval: 'retrieval_profile_id',
          }[profileCreator.kind]
          updateStepConfig(profileCreator.stepIndex, configKey, resource.id)
        }}
      />

      <Modal
        title={inputTarget ? `为「${draft.steps[inputTarget.stepIndex]?.name ?? '当前步骤'}」新建任务输入` : '新建任务输入'}
        open={Boolean(inputTarget)}
        onCancel={() => setInputTarget(undefined)}
        onOk={() => void addInputPort()}
        okText="创建并连接"
      >
        <Alert type="info" showIcon message="这里只定义资源槽位" description="发布流程后，每次创建任务时再为该槽位选择实际数据。" style={{ marginBottom: 16 }} />
        <Form form={inputForm} layout="vertical" initialValues={{ required: true }}>
          <Form.Item name="description" label="业务名称" rules={[{ required: true, message: '请输入业务名称' }]}><Input /></Form.Item>
          <Form.Item name="name" label="资源编码" rules={[{ required: true }, { pattern: identifierPattern, message: '使用 snake_case，例如 knowledge' }]}><Input /></Form.Item>
          <Form.Item label="资源类型"><Tag>{inputTarget ? RESOURCE_TYPE_LABEL[inputTarget.port.resourceType] : ''}</Tag></Form.Item>
          <Form.Item name="required" valuePropName="checked"><Checkbox>创建任务时必须绑定</Checkbox></Form.Item>
        </Form>
      </Modal>
    </Space>
  )
}

function InsertPoint({ label, onClick }: { label: string; onClick: () => void }) {
  return <div style={{ display: 'flex', alignItems: 'center', gap: 10, paddingBlock: 9 }}>
    <div style={{ height: 1, flex: 1, background: '#d9e2ec' }} />
    <Button size="small" type="dashed" icon={<PlusOutlined />} onClick={onClick}>{label}</Button>
    <div style={{ height: 1, flex: 1, background: '#d9e2ec' }} />
  </div>
}

function StepInsertModal({
  draft, request, onCancel, onInsert,
}: {
  draft: DefinitionInput
  request?: InsertRequest
  onCancel: () => void
  onInsert: (block: WorkflowBlock, values: InsertStepValues) => void
}) {
  const [selected, setSelected] = useState<WorkflowBlock>()
  const [showBlocked, setShowBlocked] = useState(false)
  const [form] = Form.useForm<InsertStepValues>()
  const index = request?.index ?? 0
  const resourceTypes = useMemo(() => availableResourceTypes(draft, index), [draft, index])
  const assessed = useMemo(() => WORKFLOW_BLOCKS.map((block) => ({
    block,
    availability: workflowBlockAvailability(block, resourceTypes),
    producesRequested: Boolean(request?.resourceType && block.outputs.some((port) => port.resourceType === request.resourceType)),
  })).sort((a, b) => {
    if (a.producesRequested !== b.producesRequested) return a.producesRequested ? -1 : 1
    if (a.availability.canAdd !== b.availability.canAdd) return a.availability.canAdd ? -1 : 1
    return a.availability.taskInputPorts.length - b.availability.taskInputPorts.length
  }), [request?.resourceType, resourceTypes])

  useEffect(() => {
    setSelected(undefined)
    setShowBlocked(false)
  }, [request?.index, request?.resourceType])

  const choose = (block: WorkflowBlock) => {
    const usedNames: DefinitionInput['input_ports'] = { ...draft.input_ports }
    const sources: Record<string, string> = {}
    const newInputs: NonNullable<InsertStepValues['new_inputs']> = {}
    const usedReferences = new Set<string>()
    for (const port of block.inputs) {
      const options = sourceOptions(draft, index, port.resourceType)
      const preferred = options.find((option) => !usedReferences.has(option.value)) ?? options[0]
      if (preferred) {
        sources[port.name] = preferred.value
        usedReferences.add(preferred.value)
      } else {
        sources[port.name] = CREATE_TASK_INPUT
        const name = uniqueInputName(port.name, usedNames)
        usedNames[name] = { resource_type: port.resourceType }
        newInputs[port.name] = { name, description: port.label }
      }
    }
    form.setFieldsValue({ name: block.label, sources, new_inputs: newInputs })
    setSelected(block)
  }

  const validateNewInputName = (portName: string) => async (_: unknown, value: string) => {
    if (!identifierPattern.test(value ?? '')) throw new Error('使用 snake_case')
    if (draft.input_ports[value]) throw new Error('资源编码已存在')
    const names = (form.getFieldValue('new_inputs') ?? {}) as NonNullable<InsertStepValues['new_inputs']>
    if (Object.entries(names).some(([key, item]) => key !== portName && item?.name === value)) throw new Error('资源编码不能重复')
  }

  const submit = async () => {
    const values = await form.validateFields()
    if (selected) onInsert(selected, values)
  }

  return <Modal
    width={920}
    open={Boolean(request)}
    title={selected ? `配置「${selected.label}」的数据来源` : `在第 ${index + 1} 个位置插入步骤`}
    onCancel={onCancel}
    footer={null}
    destroyOnHidden
  >
    {selected ? <Form form={form} layout="vertical">
      <Alert type="info" showIcon message="随步骤一起完成资源连接" description="选择已有的任务输入/上游产出，或直接创建一个新的任务输入槽位。" style={{ marginBottom: 16 }} />
      <Form.Item name="name" label="步骤名称" rules={[{ required: true, whitespace: true }]}><Input maxLength={80} /></Form.Item>
      <Row gutter={[16, 12]}>
        {selected.inputs.map((port) => {
          const options = sourceOptions(draft, index, port.resourceType)
          const selectOptions = [
            ...options,
            ...(canBindTaskInput(port.resourceType) ? [{ value: CREATE_TASK_INPUT, label: `＋ 新建${RESOURCE_TYPE_LABEL[port.resourceType] ?? port.resourceType}任务输入` }] : []),
          ]
          return <Col xs={24} md={12} key={port.name}>
            <Card size="small" title={<Space>{port.label}<Tag>{RESOURCE_TYPE_LABEL[port.resourceType]}</Tag></Space>}>
              <Form.Item name={['sources', port.name]} rules={[{ required: true, message: '请选择数据来源' }]}>
                <Select options={selectOptions} />
              </Form.Item>
              <Form.Item noStyle shouldUpdate={(previous, current) => previous.sources?.[port.name] !== current.sources?.[port.name]}>
                {({ getFieldValue }) => getFieldValue(['sources', port.name]) === CREATE_TASK_INPUT ? <>
                  <Form.Item name={['new_inputs', port.name, 'description']} label="任务创建页显示名称" rules={[{ required: true, whitespace: true }]}><Input /></Form.Item>
                  <Form.Item name={['new_inputs', port.name, 'name']} label="资源编码" rules={[{ required: true }, { validator: validateNewInputName(port.name) }]}><Input /></Form.Item>
                </> : null}
              </Form.Item>
            </Card>
          </Col>
        })}
      </Row>
      <Divider />
      <Space style={{ width: '100%', justifyContent: 'space-between' }}>
        <Button onClick={() => setSelected(undefined)}>返回选择步骤</Button>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => void submit()}>插入并完成连接</Button>
      </Space>
    </Form> : <>
      {request?.resourceType && <Alert
        type="warning"
        showIcon
        message={`当前缺少${RESOURCE_TYPE_LABEL[request.resourceType] ?? request.resourceType}`}
        description={`已优先排列能产出该资源的步骤：${producerBlocks(request.resourceType).map((block) => block.label).join('、') || '暂无已注册 Executor'}`}
        style={{ marginBottom: 16 }}
      />}
      <Space style={{ width: '100%', justifyContent: 'space-between', marginBottom: 12 }}>
        <Text type="secondary">默认隐藏当前不合法的步骤，避免创建断链。</Text>
        <Checkbox checked={showBlocked} onChange={(event) => setShowBlocked(event.target.checked)}>显示暂不可用步骤</Checkbox>
      </Space>
      <Row gutter={[12, 12]}>
        {assessed.filter((item) => showBlocked || item.availability.canAdd).map(({ block, availability, producesRequested }) => {
          const direct = availability.connectedPorts.length === block.inputs.length
          const missingLabels = availability.missingUpstreamPorts.map((port) => RESOURCE_TYPE_LABEL[port.resourceType] ?? port.resourceType)
          const suggestions = [...new Set(availability.missingUpstreamPorts.flatMap((port) => producerBlocks(port.resourceType).filter((item) => item.id !== block.id).map((item) => item.label)))]
          return <Col xs={24} md={12} key={block.id}>
            <Card
              size="small"
              hoverable={availability.canAdd}
              onClick={() => availability.canAdd && choose(block)}
              style={{ height: '100%', opacity: availability.canAdd ? 1 : 0.58, borderColor: producesRequested ? '#1677ff' : direct ? '#95de64' : undefined }}
              title={<Space wrap><Text strong>{block.label}</Text>{producesRequested && <Tag color="blue">推荐上游</Tag>}{direct && <Tag color="success">可直接衔接</Tag>}{availability.canAdd && !direct && <Tag color="gold">需新建任务输入</Tag>}</Space>}
            >
              <Paragraph type="secondary" style={{ minHeight: 44 }}>{block.description}</Paragraph>
              <Space wrap size={[4, 4]}>{block.inputs.map((port) => <Tag key={`input-${port.name}`}>{RESOURCE_TYPE_LABEL[port.resourceType] ?? port.resourceType}</Tag>)}<Text type="secondary">→</Text>{block.outputs.map((port) => <Tag color="cyan" key={`output-${port.name}`}>{RESOURCE_TYPE_LABEL[port.resourceType] ?? port.resourceType}</Tag>)}</Space>
              {!availability.canAdd && <Alert type="error" showIcon style={{ marginTop: 10 }} message={`缺少上游：${missingLabels.join('、')}`} description={suggestions.length ? `建议先添加：${suggestions.join('、')}` : '暂无可产出该资源的 Executor'} />}
            </Card>
          </Col>
        })}
      </Row>
    </>}
  </Modal>
}

function StepConfigEditor({
  step, extractionOptions, retrievalOptions, analysisOptions, onChange, onCreate,
}: {
  step: V2StepDefinition
  extractionOptions: Array<{ value: string; label: string }>
  retrievalOptions: Array<{ value: string; label: string }>
  analysisOptions: Array<{ value: string; label: string }>
  onChange: (key: string, value: unknown) => void
  onCreate: (kind: Extract<EmbeddedResourceKind, 'analysis' | 'extraction' | 'retrieval'>) => void
}) {
  const config = step.config ?? {}
  if (step.kind === 'llm.extract') return <ConfigResourcePicker label="抽取规则" value={config.extraction_profile_id as string} options={extractionOptions} onChange={(value) => onChange('extraction_profile_id', value)} onCreate={() => onCreate('extraction')} />
  if (step.kind === 'retrieval.build') return <ConfigResourcePicker label="索引规则" value={config.retrieval_profile_id as string} options={retrievalOptions} onChange={(value) => onChange('retrieval_profile_id', value)} onCreate={() => onCreate('retrieval')} />
  if (step.kind === 'agent.analyze') return <ConfigResourcePicker label="分析规则" value={config.analysis_profile_id as string} options={analysisOptions} onChange={(value) => onChange('analysis_profile_id', value)} onCreate={() => onCreate('analysis')} />
  if (step.kind === 'data.analysis_publish') return <ConfigRow label="记录所在字段"><Input value={config.records_path as string} onChange={(event) => onChange('records_path', event.target.value)} placeholder="例如 records 或 nodes" /></ConfigRow>
  if (step.kind === 'artifact.render') return <Row gutter={12} style={{ marginTop: 14 }}>
    <Col span={8}><ConfigField label="制品名称"><Input value={config.name as string} onChange={(event) => onChange('name', event.target.value)} /></ConfigField></Col>
    <Col span={6}><ConfigField label="制品格式"><Select value={config.kind as string} options={[{ value: 'markdown', label: 'Markdown 文档' }, { value: 'json', label: 'JSON 数据' }]} onChange={(value) => onChange('kind', value)} /></ConfigField></Col>
    <Col span={10}><ConfigField label="内容所在字段"><Input value={config.content_path as string} onChange={(event) => onChange('content_path', event.target.value)} placeholder="例如 report" /></ConfigField></Col>
  </Row>
  if (step.kind === 'graph.build') return <ConfigRow label="图谱名称"><Input value={config.name as string} onChange={(event) => onChange('name', event.target.value)} /></ConfigRow>
  if (step.kind === 'data.query_derive') return <Row gutter={[12, 12]} style={{ marginTop: 14 }}>
    <Col xs={24} md={8}><ConfigField label="派生流程编码"><Input value={config.pipeline_key as string} onChange={(event) => onChange('pipeline_key', event.target.value)} placeholder="product_query_v1" /></ConfigField></Col>
    <Col xs={24} md={8}><ConfigField label="标题字段"><Input value={config.title_field as string} onChange={(event) => onChange('title_field', event.target.value)} /></ConfigField></Col>
    <Col xs={24} md={8}><ConfigField label="定义字段"><Select mode="tags" value={config.definition_fields as string[]} onChange={(value) => onChange('definition_fields', value)} /></ConfigField></Col>
    <Col xs={24} md={8}><ConfigField label="别名字段"><Select mode="tags" value={config.alias_fields as string[]} onChange={(value) => onChange('alias_fields', value)} /></ConfigField></Col>
    <Col xs={24} md={8}><ConfigField label="关键词字段"><Select mode="tags" value={config.keyword_fields as string[]} onChange={(value) => onChange('keyword_fields', value)} /></ConfigField></Col>
    <Col xs={24} md={8}><ConfigField label="语义单元字段（可选）"><Input value={config.semantic_units_field as string} onChange={(event) => onChange('semantic_units_field', event.target.value)} /></ConfigField></Col>
    <Col xs={24} md={8}><ConfigField label="语义单元唯一键（可选）"><Input value={config.unit_key_field as string} onChange={(event) => onChange('unit_key_field', event.target.value)} /></ConfigField></Col>
  </Row>
  return null
}

function ConfigResourcePicker({
  label, value, options, onChange, onCreate,
}: {
  label: string
  value?: string
  options: Array<{ value: string; label: string }>
  onChange: (value: string) => void
  onCreate: () => void
}) {
  return <ConfigRow label={label}>
    <Space.Compact style={{ width: '100%' }}>
      <Select
        value={value || undefined}
        options={options}
        onChange={onChange}
        placeholder={`选择已有${label}`}
        showSearch
        optionFilterProp="label"
        style={{ width: 'calc(100% - 104px)' }}
      />
      <Button icon={<PlusOutlined />} onClick={onCreate}>就地创建</Button>
    </Space.Compact>
  </ConfigRow>
}

function ConfigRow({ label, children }: { label: string; children: ReactNode }) {
  return <div style={{ marginTop: 14, maxWidth: 520 }}><ConfigField label={label}>{children}</ConfigField></div>
}

function ConfigField({ label, children }: { label: string; children: ReactNode }) {
  return <Space direction="vertical" size={5} style={{ width: '100%' }}><Text strong>{label}</Text>{children}</Space>
}
