# ReqFlow 异构数据管线 V2 落地方案

> 状态：阶段 A、B、C、D、E、F 已完成；进入上线前质量与部署门禁阶段
> 日期：2026-08-30
> 适用范围：ReqFlow 当前未上线版本
> 关联文档：[产品总纲](./PRODUCT.md) · [交接文档](./HANDOVER.md) · [技术债台账](./DEBT.md)

## 0. 实施状态

截至 2026-08-30，阶段 A 核心底座、阶段 B Orchestrator、阶段 C 产品规格清洗纵向切片、阶段 D Query Dataset 增量派生、阶段 E 混合检索和阶段 F 无代码业务任务已经落地：

- [x] Asset、ParsedDocument、不可变 DatasetSchemaDefinition、DatasetBatch、ResourceBinding、TaskDefinition、StepRun、RetrievalSnapshot 和 Artifact 领域模型。
- [x] JSON Schema 受控子集校验、规范化和稳定哈希。
- [x] Dataset Item 结构校验、主键生成、fingerprint 和连续 commit_seq 计算。
- [x] TaskDefinition DAG、重复 Executor Kind、端口引用和资源类型校验。
- [x] PostgreSQL V2 基础表、Batch 原子提交、Outbox 和增量读取位点。
- [x] TaskDefinition 快照、Task 输入资源绑定和 StepRun 原子创建。
- [x] 领域单测、应用服务单测和 PostgreSQL 集成测试。
- [x] Executor Registry、按 `step_id` 调度、Step 输出资源绑定和任务输出固化。
- [x] PostgreSQL `SKIP LOCKED` Worker、owner fencing、lease 续租/回收、checkpoint、retry 和 `pausing` 两阶段暂停。
- [x] Scheduler ready-set、任意位置 Human Gate、周期性数据库对账和服务启动装配。
- [x] 最小 V2 HTTP API：Schema/Dataset/Batch、TaskDefinition/Task 生命周期、通用 Task Snapshot 和数据库快照 diff SSE。
- [x] Task 输入资源存在性校验、Dataset Alias 单次解析，以及 Dataset/Retrieval 读取边界自动固化。
- [x] Legacy/V2 查询与启动恢复隔离；Legacy `RecoverStuck` 不再触碰 V2 Task。
- [x] 内容寻址本地 BlobStore、按 SHA-256 去重的 Asset、可复用 AssetSet 和独立 V2 上传 API。
- [x] Reader-based 结构化 Parser、ParsedDocument/DocumentBlock 持久化和分页读取 API。
- [x] `source.parse` Executor、逐文件失败 Manifest、成功缓存、checkpoint/progress 和 attempt fencing。
- [x] 不可变 ExtractionProfile、目标 Schema/工作区校验和稳定 ProfileHash。
- [x] `llm.extract` Executor、Schema 驱动工具参数、严格 JSON、原文 quote 校验和候选 RecordDraft 资源。
- [x] 稳定 ExtractionUnit、逐单元状态、partial Manifest、成功单元复用、跨 attempt token 聚合和 producer attempt fencing。
- [x] 受控归一化/校验 DSL：类型、单位、枚举、日期/布尔、数组拆分、派生字段及业务规则，不接受脚本或任意表达式。
- [x] `data.transform` Executor、不可变 TransformedRecordSet、逐记录恢复、字段修改 Diff、问题明细和 producer attempt fencing。
- [x] `data.validate` Executor、固定 Dataset `through_seq` 的 ValidationResultSet、Schema/业务规则、Batch 重复与已有 ItemKey 冲突分类。
- [x] `human.review` 审核用例：全量决定覆盖、服务端生成不可变 ApprovedRecordSet、编辑重校验、审核重试幂等和 provenance 固化。
- [x] `data.publish` Executor：只消费 ApprovedRecordSet，按 StepRun 幂等创建 Batch，提交前 attempt fencing，并复用 Dataset/Item/Outbox 原子事务。
- [x] V2 Task 目录与通用 Task Detail：独立于 Legacy 查询，按定义快照展示步骤名称/Executor、资源端口、生命周期操作和 GET SSE 快照收敛。
- [x] 不可变 AnalysisProfile、AnalysisResult、Artifact，以及 `agent.analyze`、`data.analysis_publish`、`artifact.render`、`graph.build` 四种通用 Executor。
- [x] 数据清洗入库、精准 + 语义索引、Bug 分析、产品方案生成、知识图谱构建五种无代码模板；业务差异仅由资源绑定、Profile 和 TaskDefinition 表达。
- [x] 纯 V2 前端信息架构：任务、定义、数据集、元数据、混合检索、制品和归档均只调用 `/api/v2`，旧内置任务入口已从路由和导航移除。
- [x] Schema 驱动审核工作台：逐条 approve/edit/exclude、类型化编辑、置信度、确定性转换 Diff、校验问题、provenance、不可变审核回放和发布结果展示。
- [x] 前端路由级 code splitting，消除单一 1.5 MB 首载 Chunk。
- [x] `data.query_derive` Executor：固定 Base Dataset `through_seq`，按 PipelineCursor 只读取未消费 `commit_seq`，确定性展开语义单元并生成 aliases/keywords/facets/source_refs。
- [x] Query Dataset Batch 与 PipelineCursor 同事务提交；并发/失败时整批回滚且 Cursor 不前移，重试复用 StepRun 幂等 Batch。
- [x] Query Item 固化 `source_item_id + source_fingerprint`，provenance 同时保留 Base DatasetItem 与原 Asset/Block 链路。
- [x] 不可变 RetrievalProfile、`retrieval.build`、OpenSearch BM25、pgvector Chunk、加权 RRF、SiliconFlow rerank 和 KnowledgeScope Agent 工具。

V2 开发期间暂以 `0012_pipeline_v2_foundation` 叠加现有迁移，目的是让每批代码可以独立构建和验证；旧运行路径切除后按本文第 14 节压平为新的初始迁移。这不是产品兼容层，V2 服务不通过旧 API 或旧模型读写。

## 1. 结论

ReqFlow V2 不再围绕“需求文档导入”这一条固定流程扩展，而是重建为一个版本化配置、增量数据、可编排任务和可检索知识共同组成的数据管线平台。

核心业务链路为：

```text
非标准产品文档
    │
    ▼
AssetSet / ParsedDocument
    │
    ▼
清洗任务
解析 → LLM 抽取 → 确定性归一化 → 校验 → 去重 → 人审 → Batch 提交
    │
    ▼
基础 Dataset（Schema 固定，数据按 Batch 增量追加）
    │
    ▼
数据处理任务
语义单元抽取 → 关键词/别名生成 → 分块 → Query Dataset Batch
    │
    ▼
Retrieval Snapshot
BM25 + Vector + Metadata Filter + RRF
    │
    ├── Bug 分析
    ├── 产品规格说明书
    └── 知识图谱
```

本次重构遵循以下已经确认的取舍：

1. 项目未上线，现有开发数据可以丢弃，不做旧表迁移、旧 API 兼容或 V1/V2 双轨。
2. Dataset Schema 创建后不可编辑；需要改 Schema 时创建新的 Schema 和 Dataset。
3. 不建设完整 Dataset Revision、行级时间旅行、Diff、分支和回滚系统。
4. Dataset 是长期存在的数据容器；每次任务生成不可变的 Dataset Batch，而不是复制出完整新 Dataset。
5. 第一阶段正式数据只支持追加。已提交 Batch 和 Item 不做原地修改或物理删除。
6. 查询数据集是可检查的数据；BM25/向量索引是查询数据集的可重建派生资源，二者不能混为一体。
7. 任务通过有名字的输入/输出端口连接 Asset、Dataset、Retrieval Snapshot 和 Artifact，不再限定一个输入数据集和一个输出数据集。
8. Agent 只读取明确授权的知识范围并生成草稿；正式数据写入和业务发布继续经过人工 Gate。

## 2. 目标与非目标

### 2.1 必须实现的目标

- 支持一次任务输入多份不同格式的产品文档，并允许单文件失败、重试和跳过。
- 用户可以创建不同的结构 Schema，并据此创建基础、查询、分析、图节点、图边等异构 Dataset。
- Schema、抽取规则、检索规则彼此独立，避免字段定义同时承担提示词和索引配置。
- 一个 Dataset 可以通过多个任务 Batch 持续增量增加条目。
- 每个输出条目能够追溯到来源文件、页码、区块、任务、步骤和批次。
- 数据处理任务只处理上次成功位点之后的新数据。
- 对 Dataset 已有字段建立 BM25、向量和过滤索引，并通过 RRF 提供统一混合检索。
- Agent 可以通过受限工具查询数据集知识，并返回可核验的来源引用。
- 工作流允许同一类型步骤重复出现、允许多输入多输出、允许 Gate 放在任意步骤之后。
- 长任务可暂停、重试、续跑；服务重启后执行状态不丢失。

### 2.2 本轮明确不做

- 不兼容现有数据库表、migration、任务 API 或前端任务详情结构。
- 不支持已发布 Schema 原地编辑。
- 不支持已提交 Item 的任意更新、删除和历史时间旅行。
- 不提供任意用户代码、脚本或容器执行能力。
- 不在第一阶段建设分布式调度平台；先使用 PostgreSQL 持久化队列和本地 Worker。
- 不在第一阶段引入图数据库；知识图谱先落为节点 Dataset、边 Dataset 和 Graph Manifest。
- 不支持多个 embedding 维度混杂在同一向量索引；首期固定一个平台级 embedding 模型和维度。
- 不追求第一版就并行执行任意 DAG；数据模型按 DAG 设计，调度先按拓扑顺序串行执行。

## 3. 现有能力的处理方式

### 3.1 保留并重用

- Go 四层依赖方向和 `domain / app / port / infra` 边界。
- OpenAI/Anthropic LLM Provider 适配层。
- Agent Loop、工具调用、会话序列化和 `ask_human` 交互模型。
- SSE 的 persist-then-publish、快照恢复和前端重连机制。
- JSONB 字段袋、`item_key`、`fingerprint` 和按 Schema 动态展示表格的思路。
- 人工 Gate、草稿编辑、校验后发布的产品原则。
- pgvector 基础设施和批量 embedding 能力。

### 3.2 直接删除或替换

- 删除 Dataset Schema 编辑、兼容性检查和 Schema 历史 UI/API。
- 删除任务上的 `InputDatasetID`、`OutputDatasetID`，改为资源绑定表。
- 删除 `task.input` 中单文件路径和整篇 `parsed_text` 的主数据职责。
- 删除固定的 `parse / human / analyze / dataset` 顺序模型。
- 删除通过 `StepKind` 找第一个步骤并执行的 Runner 分发方式。
- 删除任务类型与数据集类型的一对一所有权限制。
- 删除类型级 Schema、Profile、Workflow 聚合为一个元数据对象的模型。
- 删除当前 FTS 与单条目单向量直接耦合的索引实现。
- 删除旧归档表搬运模型；V2 使用状态和保留策略，等有真实合规需求后再扩展冷归档。

保留代码是为了复用经过验证的行为，不代表保留旧接口或旧数据结构。

## 4. 核心领域模型

### 4.1 Asset：原始输入资产

原始文件必须作为不可变资产保存，解析结果不能取代原文件。

```text
Asset
  id
  filename
  mime_type
  size_bytes
  sha256
  blob_uri
  created_at

AssetSet
  id
  name
  created_by
  created_at

AssetSetMember
  asset_set_id
  asset_id
  ordinal
```

约束：

- 相同 SHA-256 的文件只保存一份 Blob，但可以属于多个 AssetSet。
- `blob_uri` 通过 `BlobStore` port 访问。首期实现本地文件系统，后续可替换为 S3/MinIO。
- Asset 不允许覆盖；重新上传同名不同内容得到新的 Asset。
- Task 绑定 AssetSet，而不是把文件路径塞入 Task JSON。

### 4.2 ParsedDocument：结构化解析结果

Parser port 从“返回一段 string”改成“返回结构化文档”。

```text
ParsedDocument
  id
  asset_id
  parser_name
  parser_version
  status
  content_hash
  created_at

DocumentBlock
  id
  parsed_document_id
  ordinal
  block_type       heading / paragraph / table / list / image_caption
  page_no
  section_path
  text
  metadata_json
```

解析器至少保留页码、章节路径、表格单元格和区块顺序。LLM 抽取引用 `block_id`，不引用容易漂移的字符偏移。

### 4.3 DatasetSchema：不可变数据契约

Schema 使用 JSON Schema 的受控子集：

- 根节点必须为 `object`。
- 支持 `string`、`integer`、`number`、`boolean`、`array`、`object`。
- 支持 `required`、`enum`、`format`、`items`、`minimum`、`maximum`、`pattern`。
- 默认 `additionalProperties=false`。
- 平台扩展信息放在独立的 `ui_schema`，例如标签、列宽、表单控件和帮助文案。

```text
DatasetSchema
  id
  name
  description
  json_schema
  ui_schema
  schema_hash
  created_at
```

Schema 没有 PUT/PATCH 接口。修改动作在产品上表现为“复制为新 Schema”。

以下内容禁止放入结构 Schema：

- LLM 抽取提示和示例。
- 清洗步骤说明。
- BM25 字段权重。
- 向量字段、模型和分块策略。

### 4.4 Dataset、Batch 和 Item

```text
Dataset
  id
  name
  purpose          base / query / analysis / graph_node / graph_edge
  schema_id
  key_fields       1..4 个标量业务主键字段
  status           active / sealed / archived
  current_seq
  created_at

DatasetBatch
  id
  dataset_id
  source_task_id
  source_step_run_id
  status           staging / validating / committed / rejected
  item_count
  from_seq
  to_seq
  committed_at

DatasetItem
  id
  dataset_id
  batch_id
  item_key
  fields           JSONB
  fingerprint
  commit_seq
  provenance       JSONB
  created_at
```

关键不变量：

- Dataset 的 `schema_id` 创建后不可改变。
- `committed` Batch 不可改变。
- 已提交 DatasetItem 不允许 UPDATE/DELETE。
- `(dataset_id, item_key)` 唯一；重复 key 在 Batch 校验阶段进入冲突区，不静默覆盖。
- `fingerprint` 是规范化 fields 的稳定哈希，用于识别完全重复内容。
- `commit_seq` 在 Dataset 内单调递增，只有 Batch 原子提交时才分配。
- Batch 要么全部可见，要么全部不可见。

首期增量语义只有 `APPEND`：

```text
任务运行 → staging items → 校验/人审 → 原子提交 Batch → current_seq 前移
```

如果业务需要修正已经提交的数据，首期采用以下两种方式之一：

1. 修正属于新产品版本：使用新的业务 `item_key` 追加新记录。
2. 修正属于历史抽取错误：从原始 Asset 重新构建一个新 Dataset，验收后替换业务入口。

`UPSERT`、Tombstone 和变更事件留到出现真实需求后再设计，避免现在变相建设行级版本系统。

### 4.5 DatasetAlias：可替换的业务入口

Dataset 本身不可换 Schema，但业务入口需要稳定名称。

```text
DatasetAlias
  id
  name
  active_dataset_id
  updated_at
```

例如 `product_specs_current` 指向当前基础 Dataset。创建 Task 时解析 Alias，并把具体 `dataset_id` 写入资源绑定；任务运行期间不跟随 Alias 漂移。

### 4.6 ExtractionProfile：抽取和清洗配置

```text
ExtractionProfile
  id
  name
  target_schema_id
  record_granularity
  system_instruction
  field_guides         JSONB
  examples             JSONB
  normalization_rules  JSONB
  validation_rules     JSONB
  profile_hash
  created_at
```

Profile 创建后不可编辑。修改时复制为新 Profile。字段引用必须在创建时校验确实存在于目标 Schema。

LLM 只产出候选结构；trim、单位转换、日期转换、枚举别名、正则和范围检查由确定性 Transform 执行。

### 4.7 RetrievalProfile 和 RetrievalSnapshot

```text
RetrievalProfile
  id
  name
  dataset_schema_id
  lexical_config       JSONB
  vector_config        JSONB
  filter_fields        JSONB
  fusion_config        JSONB
  profile_hash
  created_at

RetrievalSnapshot
  id
  dataset_id
  retrieval_profile_id
  source_seq
  status               building / validating / active / failed / retired
  lexical_ref
  vector_ref
  lexical_count
  vector_count
  failure_reason
  activated_at
```

RetrievalProfile 同样不可编辑。修改配置产生新的 Profile，并重建索引。

`source_seq` 表示这个快照完整覆盖 Dataset 的哪个提交位点。只有词法和向量两侧都覆盖到同一位点并通过校验，Snapshot 才能 Active。

### 4.8 Artifact：非数据集产物

产品规格说明书、Bug 报告、图谱 Manifest 不应被强行塞入 Dataset。

```text
Artifact
  id
  kind             markdown / docx / pdf / graph_manifest / json
  name
  blob_uri
  content_hash
  source_task_id
  source_step_run_id
  metadata_json
  created_at
```

## 5. 任务与工作流 V2

### 5.1 TaskDefinition

任务定义描述可复用的 SOP，Task 是一次具体执行。

```json
{
  "key": "product_spec_clean",
  "name": "产品规格清洗",
  "input_ports": {
    "documents": {"resource_type": "asset_set", "required": true},
    "target": {"resource_type": "dataset", "required": true}
  },
  "output_ports": {
    "batch": {"resource_type": "dataset_batch"}
  },
  "output_bindings": {
    "batch": "$step.publish_batch.batch"
  },
  "steps": [
    {
      "id": "parse_documents",
      "kind": "source.parse",
      "depends_on": [],
      "inputs": {"assets": "$task.documents"},
      "outputs": {"documents": "parsed_documents"},
      "config": {}
    },
    {
      "id": "extract_records",
      "kind": "llm.extract",
      "depends_on": ["parse_documents"],
      "inputs": {"documents": "$step.parse_documents.documents"},
      "outputs": {"drafts": "record_drafts"},
      "config": {"extraction_profile_id": "..."}
    },
    {
      "id": "transform_records",
      "kind": "data.transform",
      "depends_on": ["extract_records"],
      "inputs": {"drafts": "$step.extract_records.drafts"},
      "outputs": {"records": "transformed_records"},
      "config": {}
    },
    {
      "id": "validate_records",
      "kind": "data.validate",
      "depends_on": ["transform_records"],
      "inputs": {
        "records": "$step.transform_records.records",
        "dataset": "$task.target"
      },
      "outputs": {"validation": "validation_results"},
      "config": {}
    },
    {
      "id": "review_records",
      "kind": "human.review",
      "depends_on": ["validate_records"],
      "inputs": {"validation": "$step.validate_records.validation"},
      "outputs": {"approved": "approved_records"},
      "config": {"allow_edit": true}
    },
    {
      "id": "publish_batch",
      "kind": "data.publish",
      "depends_on": ["review_records"],
      "inputs": {"approved": "$step.review_records.approved"},
      "outputs": {"batch": "dataset_batch"},
      "config": {}
    }
  ]
}
```

定义校验规则：

- Step ID 在定义内唯一且创建后用于稳定定位，不使用数组序号作为身份。
- `depends_on` 必须形成无环图。
- 输入引用必须指向已声明的 Task Port 或前置 Step Output。
- 输出资源类型必须与下游 Input Port 匹配。
- Executor Kind 必须存在于注册表。
- Profile、Schema、Dataset 等引用在启用定义前完成存在性和兼容性校验。

### 5.2 TaskResourceBinding

```text
TaskResourceBinding
  id
  task_id
  port_name
  direction        input / output
  resource_type
  resource_id
  boundary_json
  created_at
```

`boundary_json` 固化任务读取边界。例如 Dataset 输入保存：

```json
{
  "dataset_id": "...",
  "through_seq": 1120
}
```

Retrieval 输入保存：

```json
{
  "retrieval_snapshot_id": "...",
  "source_seq": 1120
}
```

这样 Alias 后续切换、Dataset 后续追加都不会改变已经开始的任务。

### 5.3 StepRun

```text
StepRun
  id
  task_id
  step_id
  ordinal
  kind
  status           pending / queued / running / awaiting / paused / succeeded / failed / skipped
  attempt
  input_hash
  config_hash
  checkpoint       JSONB
  progress         JSONB
  error_code
  error_message
  lease_owner
  lease_until
  started_at
  finished_at
```

StepRun 是执行状态的唯一真相源。Broker/SSE 只负责通知 UI，不承担状态持久化。

### 5.4 Executor 接口

```go
type StepExecutor interface {
    Kind() StepKind
    ValidateDefinition(ctx context.Context, def StepDefinition) error
    Execute(ctx context.Context, run StepRunContext) (StepResult, error)
    Resume(ctx context.Context, run StepRunContext, checkpoint json.RawMessage) (StepResult, error)
}

type StepRunContext struct {
    TaskID      string
    StepRunID   string
    Inputs      map[string]ResourceRef
    Config      json.RawMessage
    InputHash   string
    ConfigHash  string
    IdempotencyKey string // 跨 attempt 稳定：task_id:step_id
    ExecutionKey   string // 含 input/config hash + attempt
    Checkpoint  CheckpointWriter
    Progress    ProgressReporter
}

type StepResult struct {
    Outputs map[string]ResourceRef
    Metrics map[string]any
}
```

Executor 注册表使用 `map[StepKind]StepExecutor`。Runner 按 `step_id` 调度，因此同一个 `llm.extract` 可以在一个流程中出现多次。

首批 Kind：

| Kind | 职责 |
|---|---|
| `source.parse` | Asset → ParsedDocument/Block |
| `llm.extract` | 结构化抽取候选记录 |
| `data.transform` | 确定性归一化和派生字段 |
| `data.validate` | Schema、跨字段和业务规则校验 |
| `data.publish` | 原子提交 DatasetBatch |
| `retrieval.build` | 增量构建 BM25 和向量快照 |
| `agent.analyze` | 使用知识工具完成分析任务 |
| `artifact.render` | 生成 Markdown/DOCX/PDF 等产物 |
| `graph.build` | 生成节点、边和 Graph Manifest |
| `human.review` | 可编辑、可驳回、可继续的人工 Gate |

### 5.5 调度和恢复

首期使用 PostgreSQL 队列：

1. Orchestrator 找出所有依赖已成功的 pending StepRun。
2. 将其更新为 queued。
3. Worker 使用 `FOR UPDATE SKIP LOCKED` 领取任务并设置 lease。
4. 执行期间定期续租并写入 checkpoint/progress。
5. Worker 崩溃且 lease 过期后，StepRun 回到 queued。
6. Executor 根据 checkpoint 幂等恢复。

任务暂停时不删除队列项，只取消运行上下文并保存 checkpoint。任务恢复后重新排队。

实现使用任务级 `pausing` 过渡态：暂停请求先持久化，排队/人工步骤立即转为
`paused`；持有 lease 的本地 Worker 直接取消，远端 Worker 在续租时收到暂停信号，
保存最后 checkpoint 后释放 lease。崩溃 Worker 由过期 lease 回收逻辑收敛到
`paused`，避免“立即清 lease 导致最后检查点无法写入”和“旧 Worker 晚到覆盖”两类竞态。

幂等键至少包含：

```text
task_id + step_id + input_hash + config_hash + attempt
```

外部资源写入必须使用稳定幂等键。例如 Dataset Batch 可使用 `task_id:step_id` 作为唯一来源键，避免重试提交两次。

## 6. 产品规格清洗管线

### 6.1 上传与解析

- 一个 AssetSet 可以包含任意数量文件。
- 上传完成即计算 SHA-256，重复文件复用 Blob。
- 每个 Asset 独立产生解析子状态；一份 PDF 失败不阻塞其他文件完成。
- 解析结果按 Asset 缓存，缓存键为 `asset.sha256 + parser_name + parser_version`。
- 表格保留行列结构；图片首期只保留标题和 OCR 文本，原图仍由 Asset 引用。

### 6.2 LLM 抽取

- 按 DocumentBlock 分段，不把所有文档一次性塞入上下文。
- 抽取工具的参数 Schema 由目标 DatasetSchema 动态生成。
- 每批输出进入 `RecordDraft`，不直接写 Dataset。
- RecordDraft 必须包含字段值、字段置信度、来源 Block 和短原文证据。
- 模型输出无法通过结构校验时允许有限次数修复，超过上限进入人工异常区。

### 6.3 确定性归一化

归一化顺序固定：

```text
空白与 Unicode 标准化
→ 类型转换
→ 单位换算
→ 枚举别名映射
→ 日期/布尔标准化
→ 派生字段
```

LLM 不负责决定最终单位、日期格式和枚举编码。

首期声明式规则只开放可穷举、可校验的操作：`enum_alias`、`boolean_alias`、
`date`、`unit_scale`、`split` 和 `concat`。平台同时按目标 Schema 做递归类型转换；
规则不接受脚本、表达式或任意函数名。每条输出保存字段级 before/after Diff 和问题明细，
并由独立 `TransformedRecordSet` 固化 Profile、Schema 与转换引擎版本。

`item_key` 和 `fingerprint` 在 `data.validate` 生成，而不是在通用转换阶段生成：
`key_fields` 属于目标 Dataset 实例，不属于 ExtractionProfile。这样同一份转换结果可以针对
不同目标 Dataset 做独立校验，不会把实例级身份规则错误固化进抽取合同。

### 6.4 校验与冲突处理

结果分为：

- `valid`：可以提交。
- `warning`：允许人工确认后提交。
- `invalid`：必须修改或排除。
- `duplicate_in_batch`：Batch 内重复。
- `conflict_existing_key`：与 Dataset 已提交 ItemKey 冲突。

首期 `conflict_existing_key` 不自动覆盖，用户只能排除该条或选择重建 Dataset。

业务规则 DSL 首期开放 `required`、`regex`、`range`、`length`、`one_of` 和
`compare`，每条规则明确 `warning/error`。`data.validate` 首次运行时固化目标 Dataset
的 `validated_through_seq`；重试仍在同一边界上重建结果，发布前再对 Dataset 当前状态
做最终冲突校验。

### 6.5 人工审核

审核界面至少展示：

- Schema 驱动的可编辑字段表格。
- 校验错误、警告和重复分组。
- 每字段置信度。
- 来源文件、页码、章节和原文证据。
- 修改前后差异。
- 本 Batch 的新增数、排除数和冲突数。

人工确认后生成不可变的 Approved Records，再交由 `data.publish` 提交。

### 6.6 Batch 原子提交

提交事务：

1. 锁定 Dataset 行。
2. 再次校验 Schema、ItemKey 唯一性和来源 Batch 幂等键。
3. 从 `current_seq + 1` 开始为记录分配连续序号。
4. 批量插入 DatasetItem。
5. 更新 Batch 为 committed，写入 `from_seq/to_seq`。
6. 更新 Dataset `current_seq`。
7. 写入 Outbox 事件 `dataset.batch_committed`。
8. 提交事务。

索引和下游处理从 Outbox 异步触发，不能在这个事务里调用外部服务。

## 7. 查询数据集处理管线

查询数据集仍使用通用 Dataset/Batch/Item 模型，不建立专用业务表。

实施状态：阶段 D 已完成。`data.query_derive` 是纯 V2 Executor，只消费
`source: dataset_boundary` 与 `target: dataset`，输出 `batch: dataset_batch` 和
`cursor: pipeline_cursor`。映射合同固化在 TaskDefinition `config` 中，包含
`pipeline_key/title_field/definition_fields/alias_fields/keyword_fields/facet_fields`；
若 Base Item 含结构化语义单元数组，可用 `semantic_units_field + unit_key_field`
一对多展开。这里不复用 Legacy Task 固定步骤，也不引入可变 TransformProfile 兼容层。

一个查询条目建议包含：

```json
{
  "title": "过温保护",
  "aliases": ["OTP", "高温保护"],
  "definition": "...",
  "keywords": ["温度", "关断", "恢复阈值"],
  "facets": {
    "product_family": "X100",
    "module": "power"
  },
  "source_refs": [
    {
      "dataset_item_id": "...",
      "asset_id": "...",
      "block_id": "...",
      "page_no": 18
    }
  ]
}
```

数据处理 Task 输入绑定：

```text
source dataset_id
source through_seq
target query dataset_id
transform profile
```

为实现增量处理，记录消费位点：

```text
PipelineCursor
  pipeline_key
  source_dataset_id
  target_dataset_id
  processed_through_seq
  last_success_task_id
```

每次只读取：

```sql
commit_seq > processed_through_seq
AND commit_seq <= task_input_through_seq
```

目标 Query Batch 提交成功后才推进 Cursor。中途失败不能推进位点。

实现中 Batch 提交、Dataset `current_seq/item_count`、Outbox 和 Cursor CAS 推进位于
同一个 PostgreSQL 事务。Cursor 仍为可变消费位点，因此 Executor 输出同时携带
`PipelineCursorBoundary`，固化本次 `processed_through_seq/target_batch_id/target_through_seq`，
下游不得反查 Cursor 当前值来重建历史输入。

查询条目必须保留 `source_item_id + source_fingerprint`，这样重复触发时可以判定已经处理过，不重复调用 LLM 和 embedding。

## 8. BM25 + Vector 混合检索

### 8.1 后端边界

领域层只依赖：

```go
type LexicalBackend interface {
    Build(ctx context.Context, req LexicalBuildRequest) error
    Search(ctx context.Context, req LexicalSearchRequest) ([]RankedHit, error)
}

type VectorBackend interface {
    Build(ctx context.Context, req VectorBuildRequest) error
    Search(ctx context.Context, req VectorSearchRequest) ([]RankedHit, error)
}
```

首期默认实现：

- BM25：OpenSearch，按 Dataset + RetrievalProfile 建物理索引。
- Vector：PostgreSQL + pgvector。
- Fusion：应用层 RRF。

当前 PostgreSQL `ts_rank` 可以删除，不作为 BM25 实现继续维护。

### 8.2 RetrievalProfile 示例

```json
{
  "lexical": {
    "fields": {
      "title": 3.0,
      "aliases": 2.0,
      "definition": 1.0,
      "keywords": 2.5
    },
    "analyzer": "product_cn"
  },
  "vector": {
    "fields": ["title", "aliases", "definition"],
    "chunk_size": 500,
    "chunk_overlap": 80,
    "embedding_model": "platform_default"
  },
  "filters": ["product_family", "module"],
  "fusion": {
    "method": "rrf",
    "rank_constant": 60,
    "lexical_candidates": 100,
    "vector_candidates": 100
  }
}
```

Profile 创建时校验所有字段都存在于绑定 Schema，且类型适合对应索引方式。

### 8.3 向量存储

```text
RetrievalChunk
  id
  dataset_id
  dataset_item_id
  retrieval_profile_id
  chunk_no
  chunk_text
  chunk_hash
  source_seq
  embedding_model
  embedding
```

唯一约束：

```text
(dataset_item_id, retrieval_profile_id, chunk_no)
```

新增 Batch 时只处理 `source_seq` 大于 Active Snapshot 位点的 Item。相同 `chunk_hash + embedding_model` 可以复用 embedding 缓存。

### 8.4 构建和激活

```text
读取 Dataset 增量 Item
    ├── 写 OpenSearch BM25 文档
    └── 分块、Embedding、写 pgvector
             │
             ▼
校验 lexical_count / vector_count / source_seq
             │
             ▼
事务性更新 RetrievalSnapshot 为 active
```

因为首期 Dataset 只追加，已经开始的任务可以通过 `source_seq <= pinned_seq` 保持稳定读取。若未来允许更新和删除，必须同时设计版本化搜索文档或双索引切换，不能直接覆盖现有文档。

### 8.5 查询和 RRF

统一请求：

```json
{
  "retrieval_snapshot_id": "...",
  "query": "设备高温后为什么自动关断",
  "filters": {"product_family": ["X100"]},
  "strategy": {
    "mode": "hybrid",
    "lexical_weight": 0.35,
    "semantic_weight": 0.65,
    "score_threshold": 0.12,
    "recall_limit": 100,
    "top_k": 20,
    "rerank_enabled": true,
    "rerank_top_n": 10
  }
}
```

`lexical_weight` 与 `semantic_weight` 是单次查询的运行时比例，服务端会归一化，
不写回不可变 RetrievalProfile。`mode=lexical/semantic` 分别强制只启用精准词法或语义通道。
`recall_limit` 控制每个后端的候选召回规模，`top_k` 控制未重排时的最终返回数；
`score_threshold` 作用于 0..1 的归一化加权 RRF 分数。

`rerank_enabled=true` 时，融合并过阈值的候选会调用与 embedding 相同供应商、复用同一
`base_url/api_key` 的 rerank 接口；首个实现使用 `BAAI/bge-reranker-v2-m3`。
`rerank_top_n` 独立控制重排后的最终返回数量，返回结果同时保留 `fusion_score`、
`rerank_score` 和 lexical/semantic 原始排名证据。

处理过程：

1. 校验 Snapshot 为 active。
2. 校验 filters 只引用 Profile 允许的字段。
3. 并行请求 BM25 和 Vector Backend。
4. 使用 RRF 合并排名，不直接相加原始分数。
5. 以 Dataset Item 聚合多个 Chunk 命中。
6. 返回命中字段、Chunk、排名来源、融合分数和 provenance。

RRF 计算：

```text
score(d) = Σ 1 / (rank_constant + rank_i(d))
```

### 8.6 质量评估

上线任何 RetrievalProfile 前必须通过固定评测集：

- Recall@5、Recall@20。
- MRR 或 nDCG@10。
- 无结果率。
- 引用来源正确率。
- Filter 前后召回完整性。
- BM25、Vector、Hybrid 三组对照。

Profile 的启用不是只看索引构建成功，还要看离线评测是否达到门槛。

## 9. Agent 知识工具

### 9.1 KnowledgeScope

每个 Agent Task 创建时生成不可变的查询权限：

```json
{
  "sources": [
    {
      "name": "product_specs",
      "retrieval_snapshot_id": "...",
      "allowed_fields": ["title", "definition", "keywords", "facets"]
    }
  ],
  "max_top_k": 30,
  "allow_modes": ["hybrid", "lexical", "vector"]
}
```

Agent 无权通过参数传入任意 Dataset ID，也不能绕过 Snapshot 直接扫描数据库。

### 9.2 工具集合

#### `list_knowledge_sources`

返回本任务可使用的知识源、简介、字段和过滤条件。

#### `search_knowledge`

输入查询、知识源名、过滤条件、检索模式和 top_k。返回紧凑摘要、命中 Chunk、匹配字段和引用 ID。

#### `get_knowledge_item`

按引用 ID 获取完整结构化记录和 provenance，避免搜索工具一次把全文塞回模型。

#### `graph_neighbors`

知识图谱任务完成后按节点查询一跳邻居；首期读取边 Dataset，不依赖图数据库。

所有调用记录 task_id、step_run_id、工具参数摘要、Snapshot ID、返回引用 ID、耗时和 token 使用量。

### 9.3 写入边界

- Agent 可以写 `RecordDraft` 和 `ArtifactDraft`。
- Agent 不能调用 `data.publish`。
- Dataset Batch 提交、Artifact 正式发布和外部系统写操作必须经过 Human Review 或显式审批策略。

## 10. 五类业务任务模板

### 10.1 `product_spec_clean`

```text
输入：AssetSet + Target Base Dataset + ExtractionProfile
步骤：parse → extract → transform → validate → human.review → publish
输出：DatasetBatch
```

### 10.2 `semantic_query_build`

```text
输入：Base Dataset Boundary + Target Query Dataset + TransformProfile
步骤：read_increment → extract_semantics → validate → publish_query_batch → build_retrieval
输出：Query DatasetBatch + RetrievalSnapshot
```

### 10.3 `bug_analysis`

```text
输入：Bug Asset/Dataset + Product RetrievalSnapshot
步骤：parse/import bugs → agent.analyze → human.review → publish analysis batch → render report
输出：BugAnalysis DatasetBatch + Report Artifact
```

### 10.4 `product_spec_generate`

```text
输入：RetrievalSnapshot + Document Template + Product Filter
步骤：retrieve facts → build outline → render sections → consistency check → human.review → render artifact
输出：Markdown/DOCX/PDF Artifact
```

### 10.5 `knowledge_graph_build`

```text
输入：Base/Query Dataset Boundary
步骤：entity extraction → relation extraction → entity resolution → human.review → publish nodes/edges → graph manifest
输出：Node DatasetBatch + Edge DatasetBatch + Graph Manifest
```

## 11. API V2 合同

旧 API 不保留兼容，统一放在 `/api/v2`。下列目录中，阶段 B 已实现写路径、
增量 Item 读取和 Task 运行时；列表/详情/clone、Asset/Profile/Retrieval 随后续阶段开放。

### 11.1 Schema 和 Dataset

```text
POST   /api/v2/schemas
GET    /api/v2/schemas
GET    /api/v2/schemas/:id
POST   /api/v2/schemas/:id/clone

POST   /api/v2/datasets
POST   /api/v2/datasets/:id/batches
POST   /api/v2/batches/:id/commit
GET    /api/v2/datasets
GET    /api/v2/datasets/:id
GET    /api/v2/datasets/:id/items
GET    /api/v2/datasets/:id/batches

POST   /api/v2/dataset-aliases
PUT    /api/v2/dataset-aliases/:id/target
```

没有 Schema PUT/PATCH 端点。

### 11.2 Asset

```text
POST   /api/v2/assets
POST   /api/v2/asset-sets
POST   /api/v2/asset-sets/:id/assets
GET    /api/v2/asset-sets/:id
GET    /api/v2/assets/:id/parsed-document
```

### 11.3 Profile

```text
POST   /api/v2/extraction-profiles
POST   /api/v2/extraction-profiles/:id/clone
POST   /api/v2/retrieval-profiles
POST   /api/v2/retrieval-profiles/:id/clone
```

### 11.4 Task

```text
POST   /api/v2/task-definitions
POST   /api/v2/task-definitions/:id/check
POST   /api/v2/tasks
GET    /api/v2/tasks/:id
POST   /api/v2/tasks/:id/start
POST   /api/v2/tasks/:id/pause
POST   /api/v2/tasks/:id/resume
POST   /api/v2/tasks/:id/steps/:step_id/retry
POST   /api/v2/tasks/:id/steps/:step_id/approve
GET    /api/v2/tasks/:id/events
```

### 11.5 Retrieval

```text
POST   /api/v2/retrieval-builds
GET    /api/v2/retrieval-snapshots/:id
POST   /api/v2/retrieval-snapshots/:id/search
POST   /api/v2/retrieval-profiles/:id/evaluate
```

## 12. 前端改造

实施状态：通用 Task Detail 与 Human Review 面板已完成；数据/Schema/Profile 目录和 Retrieval 页面仍按后续阶段推进。V2 前端使用独立 `/v2/tasks` 路由和 snake_case API 类型，不在 Legacy 固定四步页面上叠加兼容逻辑。

### 12.1 信息架构

建议主导航调整为：

```text
任务
数据
  ├── 原始资产
  ├── 数据集
  ├── Schema
  └── Profiles
检索
  ├── Retrieval Profiles
  ├── Snapshots
  └── 评测集
任务模板
设置
```

### 12.2 Schema Builder

- 可视化编辑常见字段类型、必填、枚举、数组和校验规则。
- 高级模式直接编辑 JSON Schema。
- 保存前前后端均校验。
- 保存后只读；“修改”按钮实际执行 Clone。

### 12.3 Dataset 页面

- 展示 Schema、current_seq、Batch 时间线和 Item 数量。
- 按 Batch、来源 Task、commit_seq 和字段过滤。
- Item 详情展示 provenance。
- 不提供已提交 Item 的行内编辑或删除按钮。

### 12.4 通用 Task Detail

- 根据 TaskDefinition 渲染步骤 DAG/时间线。
- 工作区按 Executor Kind 注册面板，不按固定步骤序号硬编码。
- Step 面板使用资源端口读取输入和输出。
- Human Review 面板作为通用组件，支持 Schema 驱动表格和 Artifact 预览。
- SSE 继续只传事件提示，页面状态以服务端 Snapshot 为准。

### 12.5 Retrieval 页面

- Profile 字段选择、权重、分块和过滤配置。
- 构建进度及 lexical/vector 覆盖率。
- BM25/Vector/Hybrid 三列结果对照。
- 测试问题集和 Recall/MRR 指标。

## 13. 后端包结构建议

```text
internal/domain/model/
  asset.go
  dataset_schema.go
  dataset.go
  dataset_batch.go
  task_definition.go
  task_run.go
  resource.go
  retrieval.go
  artifact.go
  lineage.go

internal/app/
  asset/
  dataset/
  orchestrator/
  executor/
    parse/
    extract/
    transform/
    validate/
    publish/
    retrieval/
    agent/
    artifact/
    graph/
    human/
  retrieval/
  knowledge/

internal/port/
  blob_store.go
  parser.go
  task_repo.go
  dataset_repo.go
  lexical_backend.go
  vector_backend.go
  llm.go

internal/infra/
  blob/
  parser/
  repository/
  opensearch/
  embedding/
  worker/
  httpgin/
```

不要求第一批提交就完成所有目录迁移，但新增 V2 代码必须进入目标边界，避免继续扩展旧 `TaskManager`。

## 14. 数据库重建策略

项目未上线，因此采用干净重建：

1. 删除当前 migration 链和旧表兼容代码。
2. 新建完整的 `0001_v2_init`。
3. 后续每个阶段使用正常前向 migration，不再改写已经合入的 V2 migration。
4. 本地、CI、测试环境统一执行 Drop Database/Recreate。
5. 提供最小 Seed：示例 Schema、产品规格清洗 TaskDefinition 和默认 RetrievalProfile。

`0001_v2_init` 至少包含：

```text
assets
asset_sets
asset_set_members
parsed_documents
document_blocks
dataset_schemas
datasets
dataset_aliases
dataset_batches
dataset_items
extraction_profiles
record_draft_sets
extraction_units
record_drafts
retrieval_profiles
retrieval_snapshots
retrieval_chunks
task_definitions
tasks
task_resource_bindings
step_runs
artifacts
pipeline_cursors
outbox_events
```

关键索引：

```text
assets(sha256) UNIQUE
dataset_schemas(schema_hash)
dataset_items(dataset_id, item_key) UNIQUE
dataset_items(dataset_id, commit_seq)
dataset_batches(dataset_id, committed_at DESC)
record_draft_sets(source_step_run_id) UNIQUE
extraction_units(record_draft_set_id, unit_key) UNIQUE
record_drafts(extraction_unit_id, ordinal) UNIQUE
task_resource_bindings(task_id, direction, port_name) UNIQUE
step_runs(task_id, step_id) UNIQUE
step_runs(status, lease_until)
retrieval_snapshots(dataset_id, retrieval_profile_id, status)
retrieval_chunks(dataset_id, retrieval_profile_id, source_seq)
pipeline_cursors(pipeline_key, source_dataset_id, target_dataset_id) UNIQUE
outbox_events(status, available_at)
```

## 15. 配置和运行环境

新增配置分组：

```yaml
blob:
  driver: local
  local_dir: ./data/blobs

worker:
  concurrency: 4
  lease_seconds: 60
  heartbeat_seconds: 20
  max_attempts: 3

opensearch:
  addresses: []
  username: ""
  password: ""
  index_prefix: reqflow

retrieval:
  embedding_model: bge-m3
  embedding_dimensions: 1024
  rrf_rank_constant: 60
```

开发环境 `docker compose` 增加 OpenSearch。OpenSearch 不可用时，检索构建应明确失败，不静默把 PostgreSQL FTS 冒充 BM25。

## 16. 可观测性和成本

每个 StepRun 记录：

- 输入/输出条目数。
- 成功、失败、警告和跳过数。
- LLM 请求次数、输入/输出 token、模型和费用估算。
- embedding 数量、缓存命中率和耗时。
- BM25/Vector 构建覆盖率。
- Agent 工具调用次数和查询延迟。
- 当前处理 Asset ID、Item commit_seq 和 checkpoint。

日志统一携带：

```text
task_id
step_run_id
dataset_id
batch_id
retrieval_snapshot_id
```

任何用户可见的“完成”状态必须能够从数据库事实重建，不能只依赖日志或 SSE 消息。

## 17. 安全边界

- Schema 和 Profile 创建接口限制 JSON 大小、字段数量、嵌套深度和正则复杂度。
- Asset 下载通过受控接口，不直接暴露任意本地路径。
- Dataset 查询必须按 Schema 和 RetrievalProfile 白名单校验字段。
- Agent 工具只接受知识源逻辑名，不接受任意 Dataset/Index ID。
- LLM 输入前执行敏感字段策略和最大长度限制。
- Outbox、Worker 和索引写入都必须有幂等键。
- 外部系统发布、正式 Dataset Batch 提交和 Artifact 发布保留人工审批边界。

用户与项目级 ACL 可以延后实现，但所有表从第一天保留 `workspace_id` 扩展位，避免后续无法隔离数据。

## 18. 测试策略

### 18.1 领域单元测试

- JSON Schema 支持范围和非法定义。
- Schema/Profile 不可变约束。
- Workflow DAG、端口类型和引用校验。
- ItemKey、Fingerprint、归一化稳定性。
- RRF 排名和重复 Chunk 聚合。
- Task 状态机和 Step 依赖推进。

### 18.2 PostgreSQL 集成测试

- Batch 原子提交和连续 commit_seq。
- 两个并发 Batch 不产生重复 seq 或 item_key。
- Worker lease 领取、过期回收和重试。
- Outbox 与 Batch 在同一事务提交。
- pgvector 过滤范围和索引命中。

### 18.3 OpenSearch 集成测试

- 中文 analyzer 和字段权重。
- source_seq 边界过滤。
- 增量文档写入幂等。
- BM25 返回、Filter 和删除测试索引清理。

### 18.4 端到端测试

至少建立一个固定样例：

```text
10 份混合格式产品文档
→ 解析
→ 抽取 50 条基础记录
→ 人审并提交 Batch 1
→ 再追加 2 份文档形成 Batch 2
→ 只增量生成查询数据
→ 只增量构建 BM25/Vector
→ Agent 回答并引用到原文件页码
```

测试同时覆盖暂停、服务重启、Step 重试和相同任务重复触发。

## 19. 分阶段实施计划

### 阶段 A：V2 基线和领域模型

范围：

- 重建 migration。
- 完成 Asset、Schema、Dataset、Batch、Item、TaskDefinition、Task、StepRun、ResourceBinding 基础模型。
- 完成 Repository 和基础 API。
- 删除旧 Schema 编辑、旧 Dataset 写入和旧任务创建入口。

验收：

- 可以创建不可变 Schema 和 Dataset。
- 可以创建 staging Batch 并原子提交。
- 可以向同一 Dataset 连续提交两个追加 Batch。
- 已提交 Item 无更新/删除 API。

### 阶段 B：Orchestrator V2

状态：已完成。后端持久化调度与通用 Task Detail/SSE 读模型均已接入。

范围：

- DAG 定义校验。
- Executor Registry。
- PostgreSQL Worker、lease、checkpoint 和重试。
- Task Resource Binding。
- 通用 SSE Snapshot。
- 通用 Task Detail 骨架。

验收：

- 一个工作流可以出现两个相同 Kind 的 Step。
- 一个 Task 可以绑定两个输入和两个输出。
- Human Review 可以出现在任意步骤后。
- Worker 被终止后任务可从 checkpoint 恢复。

### 阶段 C：产品规格清洗纵向切片

状态：已完成。后端纵向切片、通用 Task Detail 和 Schema 驱动审核工作台已通过真实浏览器端到端验收。

范围：

- [x] 批量 AssetSet 组装与 Asset 上传。
- [x] 结构化 Parser。
- [x] `ParsedDocumentSet` Manifest：逐文件状态、部分成功、缓存恢复和 attempt fencing。
- [x] `source.parse` 注册到 V2 Worker。
- [x] ExtractionProfile。
- [x] LLM 候选抽取、严格结构化响应、Block 原文证据和逐单元恢复。
- [x] 确定性归一化、Schema/业务规则校验和冲突处理。
- [x] 不可变 ApprovedRecordSet、全量人工决定和编辑重校验。
- [x] Dataset Batch 幂等原子发布与发布 attempt fencing。
- [x] Schema 驱动的审核 UI。

验收：

- 混合文档可批量处理。
- 单文件失败不丢失其他文件结果。
- 输出条目 100% 有 Asset/Block 来源。
- 重试不会产生重复 Batch 或 Item。
- 第二次任务能向同一 Dataset 增量追加。

### 阶段 D：查询数据集增量处理

状态：已完成。生产仓储、V2 Worker、HTTP Cursor 读模型和 PostgreSQL 集成验收均已接通。

范围：

- [x] PipelineCursor。
- [x] 语义单元、关键词、别名、facets 和 source_refs 确定性派生。
- [x] Query Dataset Batch。
- [x] 增量处理、并发 CAS 和失败位点保护。

验收：

- [x] Batch 2 运行时不重复处理 Batch 1。
- [x] 目标 Batch 失败时 Cursor 不前移。
- [x] 查询条目可追溯到基础 DatasetItem 和原 Asset Block。

### 阶段 E：混合检索和 Agent 工具

范围：

- [x] RetrievalProfile 创建、读取、目录和不可变 clone；字段类型、analyzer、chunker、embedding model 与 RRF 合同创建时校验。
- [x] OpenSearch BM25：按 Dataset + Profile 建物理索引，按 DatasetItemID 幂等增量写入。
- [x] pgvector Chunk/Embedding：`rune_v1` 分块合同、模型身份和 Item 聚合搜索。
- [x] `retrieval.build` 与 RetrievalSnapshot building → validating → active/failed 状态机，StepRun attempt fencing。
- [x] 加权 RRF HybridSearchService：词法/语义权重、融合阈值、召回数、top_k、Filter 白名单均为查询级参数。
- [x] SiliconFlow rerank：复用 embedding 凭证，支持启停和独立 `rerank_top_n`。
- [x] `list_knowledge_sources/search_knowledge/get_knowledge_item`、KnowledgeScope 逻辑名白名单和持久化调用审计。

验收：

- [x] Snapshot 未完整覆盖时不能 Active；真实 PostgreSQL 集成测试覆盖增量构建和 pgvector Filter 查询。
- [x] Agent 只能查询 KnowledgeScope 内的数据，越权逻辑名拒绝并记审计。
- [x] 每个命中返回 DatasetItem fields、Chunk、排名证据和原 provenance，Asset/Block 引用不丢失。
- [ ] 固定评测集 Hybrid 指标不低于 BM25 和 Vector 中较优者的预定门槛（需要业务标注集，作为上线门禁，不阻塞当前工程阶段）。

### 阶段 F：业务任务

状态：已完成。业务任务统一由不可变资源和 TaskDefinition DAG 装配，不增加业务专用 Runner，也不兼容旧任务流程。

范围：

- [x] `AnalysisProfile` / `AnalysisResult` 领域模型、Repository、Service 与 V2 API。
- [x] `agent.analyze`：以 RetrievalSnapshot + AnalysisProfile 运行受 KnowledgeScope 限制的通用 Agent，并固化模型与 ProfileHash 边界。
- [x] 通用资源人工 Gate：审核端只决定是否放行既有 Step 输出，不能提交或伪造资源 ID。
- [x] `data.analysis_publish`：把通过审核的结构化分析结果按目标 Dataset 当前边界原子发布为 DatasetBatch。
- [x] `artifact.render`：从 AnalysisResult 的固定 JSON 路径生成内容寻址 Markdown/JSON Artifact。
- [x] `graph.build`：引用节点 Batch、关系 Batch 和 AnalysisResult 生成可追溯 Graph Manifest，不复制实体/关系抽取逻辑。
- [x] 五种无代码模板：数据清洗入库、精准 + 语义索引、Bug 分析、产品方案生成、知识图谱构建。
- [x] V2 Catalog 和归档/恢复 API；TaskDefinition、Dataset、Schema/Profile、Artifact 均有 V2 浏览入口。
- [x] 前端路由、布局和操作全部切换为 V2；Legacy 内置任务、数据集、元数据和归档页面不再暴露。

验收：

- [x] 产品方案真实浏览器端到端完成 `agent.analyze → human.review → artifact.render`，下载内容 SHA-256 与 Artifact Boundary 一致。
- [x] Bug 分析通过真实 UI 创建 `agent.analyze → human.review → data.analysis_publish + artifact.render` 四步 DAG。
- [x] 知识图谱通过真实 UI 创建 `agent.analyze → human.review → publish_nodes + publish_edges → graph.build` 五步 DAG，并固化两个目标 Dataset 边界。
- [x] 五种模板均可从同一无代码入口创建，通用 Task Detail 按定义快照展示和驱动操作。
- [x] Task 归档、归档目录展示和恢复通过真实浏览器验收。

架构约束继续保持：只有出现无法表达的通用平台能力时才增加新 Executor Kind，禁止为业务模板复制 Runner 或在核心状态机内增加业务分支。

## 20. 建议的提交拆分

每个提交或 PR 必须保持可测试，推荐顺序：

1. `v2 schema baseline`：新 migration、模型和 Repository。
2. `immutable schema and append batches`：Schema/Dataset/Batch API 与测试。
3. `asset and structured parser`：BlobStore、AssetSet、ParsedDocument。
4. `workflow v2 core`：定义校验、ResourceBinding、StepRun。
5. `persistent worker`：lease、checkpoint、pause/resume、SSE。
6. `cleaning vertical slice`：抽取、归一化、校验、审核、发布。
7. `query dataset pipeline`：Cursor 和增量语义处理。
8. `hybrid retrieval`：OpenSearch、pgvector、RRF 和评测。
9. `agent knowledge tools`：Scope、工具、引用和审计。
10. `no-code business tasks`：分析、审核、发布、制品和图谱的通用业务闭环。

## 21. 完成定义

V2 基础改造完成必须同时满足：

- 新业务任务不需要修改 Task/Runner 核心状态机，只需增加定义、Profile，必要时增加通用 Executor。
- Dataset Schema 创建后不可修改，Dataset 可以通过 Batch 持续追加数据。
- 任意 Dataset Item 可以追溯到 Batch、Task、Step、Asset 和 DocumentBlock。
- 数据处理任务能够从精确 commit_seq 增量消费。
- BM25 和 Vector 覆盖相同 source_seq 后才对外提供 Hybrid 查询。
- Agent 查询受 KnowledgeScope 限制，回答携带可验证来源。
- 服务重启、任务暂停和执行重试不会重复提交 Batch。
- Bug 分析能够使用产品规格 RetrievalSnapshot，产出结构化分析 DatasetBatch 和报告 Artifact；产品方案与知识图谱只通过 Profile + TaskDefinition 复用同一组通用 Executor。

满足这些条件后，再评估 UPSERT/Tombstone、数据历史版本、分布式 Worker、对象存储、图数据库和多租户 ACL；在此之前不提前支付这些复杂度。
