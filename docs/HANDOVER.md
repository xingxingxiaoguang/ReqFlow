# ReqFlow 交接文档

> 面向下一个接手开发的同学。本文只回答四件事：**项目现在是什么、怎么跑起来、改代码必须知道的上下文、接手后干什么**。
> 产品定位、方向性决策与「重复人工任务 → AI 驱动 Task」的抽离范式见 [PRODUCT.md](./PRODUCT.md)；技术实现细节以本文件为准。

> **2026-08-30 交接状态**：异构数据管线 V2 的阶段 A、B、C、D、E、F 已完成。当前系统以“流程定义 → 派生任务 → 运行任务”为产品主线，五种模板只是可编辑流程起点，同时支持空白编排；不保留旧数据库、旧 API、旧前端或旧任务流程兼容。完整设计和阶段验收见 [DATA_PIPELINE_V2_PLAN.md](./DATA_PIPELINE_V2_PLAN.md)。本文中标为“Legacy”的内容只用于识别待删除代码，不是后续扩展入口。

---

## 1. 项目是什么（30 秒版）

- **目标形态**：把非标准产品文档清洗为基础 Dataset，再增量派生查询 Dataset 和 BM25 + Vector Retrieval Snapshot，最后由 Bug 分析、规格书生成、知识图谱等业务 Task 消费。
- **V2 已落地**：不可变 JSON Schema、Dataset Batch 追加、`commit_seq`、provenance/identity、TaskDefinition DAG、定义快照和资源端口；Stage B 后端已补齐 Executor Registry、Step 输出绑定、Scheduler、PostgreSQL Worker/lease/checkpoint/retry、任意位置 Human Gate、两阶段暂停和任务输出固化。
- **V2 API 已闭环**：`/api/v2` 已开放 Asset/Schema/Profile/Dataset/Batch、TaskDefinition/Task、Retrieval/Analysis/Artifact/Catalog/Archive；Worker 已注册清洗、增量派生、检索构建、通用分析、分析发布、制品渲染和图谱构建 Executor。
- **Stage C 后端已闭环**：内容寻址解析、Schema 驱动抽取、确定性转换/校验、不可变人工审核资源、幂等原子发布、完整 provenance 和 attempt fencing 已打通真实 HTTP → PostgreSQL → Worker 集成测试。
- **输入绑定已收口**：Task API 可用 `resource_id` 或 Dataset `resource_alias` 定位输入；应用层验证资源存在性，Alias 只在创建时解析一次，`dataset_boundary` 自动固化 `through_seq`，Retrieval Snapshot 自动固化 `source_seq`。
- **V2 前端已完成切换**：`/definitions` 负责流程目录，`/definitions/new` 负责空白/模板起点编排，`/tasks/new?definition_id=...` 负责从已发布流程派生任务，`/tasks` 负责运行目录；数据集、元数据、检索、制品和归档也均只使用 V2 API。
- **流程与任务边界已收口**：模板仅生成可编辑 Definition 起点；发布流程只创建 `TaskDefinition`，不创建 Task；Task 只能从 active Definition 派生，创建时冻结 Definition Snapshot、Resource Binding/Boundary 和 StepRun。
- **Stage D 已闭环**：`data.query_derive` 按 Base Dataset Boundary + PipelineCursor 增量读取，确定性展开语义单元并生成标准 Query Item；Query Batch、Dataset 位点、Outbox 和 Cursor 在同一事务提交，失败不推进 Cursor。
- **Stage E 已闭环**：不可变 RetrievalProfile、`retrieval.build` 状态机、OpenSearch BM25、pgvector Chunk、查询级加权 RRF/阈值/召回数、SiliconFlow rerank 和 KnowledgeScope Agent 工具已经落地。
- **Stage F 已闭环**：不可变 AnalysisProfile/AnalysisResult/Artifact，通用分析、资源审核、分析发布、制品渲染和图谱 Manifest Executor 已落地；五种无代码模板由 Profile + TaskDefinition 组合，不存在业务专用 Runner。
- **可复用底座**：LLM Provider、Agent Loop、工具调用、会话序列化、`ask_human`、SSE persist-then-publish 和前端重连机制继续复用，但要通过 V2 Executor/Orchestrator 接入。
- **下一步**：进入上线前门禁，补业务标注检索评测集、部署 OpenSearch、执行容量/故障恢复测试，并持续删除未被 V2 路由引用的 Legacy 代码。
- **仓库**：`/Users/xxxg/demo/ReqFlow`。操作前始终先看 `git status --short`，不要回退不属于当前任务的修改。

## 2. 怎么接手：跑起来

```bash
cd /Users/xxxg/demo/ReqFlow

# 0) 一次性初始化（启用 pre-commit 密钥护栏）
make setup

# 1) 数据库（Docker，PG16 + pgvector；镜像拉取见 §4.5 环境坑）
docker compose up -d

# 2) 配置（首启会自动生成模板；或手动 cp config.example.yaml config.yaml）
#    至少填 llm.api_key（需要语义查重则填 embedding.api_key）
vim config.yaml

# 3) 开发模式（两个终端）
make dev        # 后端 :8080（直接 go run；热重载未配，改代码需重启）
make frontend   # 前端 Vite :5173，/api 代理到 :8080

# 4) 发布验证
make build      # → bin/reqflow（约 29MB 单二进制，前端已 embed）
./bin/reqflow   # 同目录放 config.yaml 即可跑
```

**质量门禁**：`make test` = `go vet` + `go test` + 架构围栏（`scripts/arch-check.sh`）+ 密钥扫描（`scripts/secret-check.sh`）；当前全仓基线应保持全绿。V2 定向验证命令：

```bash
go vet ./internal/app/orchestrator ./internal/app/pipeline ./internal/domain/... ./internal/infra/repository ./internal/port
go test ./internal/app/orchestrator ./internal/app/pipeline ./internal/domain/... ./internal/infra/repository ./internal/port
go test -tags integration ./internal/infra/repository \
  -run 'TestIntegration(PipelineAppendBatches|TaskDefinitionAndResourceBindings|V2SourceParseAndLLMExtract)' -count=1 -v
go test -tags integration ./internal/infra/database -run TestIntegrationFreshMigration -count=1 -v
```

## 3. 怎么接手：架构地图

### 3.1 分层与依赖铁律（最重要）

四层架构，**依赖只允许向内**，业务层永远不知道 infra 的存在：

```
cmd/reqflow      组装点：读配置 → 构造 infra 实现 → 注入 app 用例 → 挂 httpgin。
                 全项目唯一知道所有具体实现的地方（cmd/reqflow/main.go）

internal/infra   外层一体（基建 + 三方客户端 + 仓储 + HTTP 路由）
internal/app     用例编排（task/analyze/dataset/match/workflow/parse/settings/overview + bug 占位）
internal/port    出站接口契约（repo/llm/embedding/parser 四个文件）
internal/domain  实体模型 + 纯领域逻辑（零三方依赖，仅标准库）
```

**依赖白名单**（arch-check.sh 强制，越界直接 fail）：

- `cmd → 全部`
- `infra/httpgin → app`（且**只能** import app——不摸 port/domain/其它 infra，HTTP 层的入参出参全部用 app 层 DTO，如 `app.DraftInput`、`app.AnalyzeDelta`）
- `infra/{repository,llm,embedding,parser} → port, domain, infra/{config,log,database,crypto}`
- `app → port, domain`；`port → domain`；`domain → 仅标准库`（`go list -deps` 检查）

**为什么这么分**：infra 与 adapter 合并为一层是明确决策（不会大规模换基建，拆开只剩目录成本）；「httpgin 只准调 app」保住 handler 不直摸仓储；接口定义在 port 层（集中契约，好导航），因此「app 不 import infra」主要靠 arch lint 而非编译器——**这是刻意的，改架构时保持 lint 规则同步更新**。

### 3.2 代码地图

#### V2 新代码（后续开发入口）

| 位置 | 职责与当前状态 |
|---|---|
| `docs/DATA_PIPELINE_V2_PLAN.md` | V2 架构、数据模型、API 草案、阶段验收和完成定义；实现取舍以此为准 |
| `internal/domain/model/asset.go` | Asset、AssetSet、ParsedDocument、DocumentBlock，以及一次 Step 输出的 ParsedDocumentSet Manifest |
| `internal/domain/model/dataset_pipeline.go` | DatasetSchemaDefinition、DatasetBatch、Alias、provenance、PipelineCursor，以及 ExtractionProfile/Unit/RecordDraft Manifest |
| `internal/domain/model/resource.go` | ResourceType、Task Port、TaskResourceBinding、Dataset/Retrieval/ParsedDocuments/RecordDrafts Boundary |
| `internal/domain/model/task_definition.go` | V2 StepKind、TaskDefinition、StepDefinition、StepRun；StepRun 用 `step_id + ordinal` 定位，不按 kind 定位 |
| `internal/domain/model/retrieval.go` | RetrievalProfile、Snapshot、Chunk、运行时检索策略和带排名证据的命中模型 |
| `internal/app/retrieval/` | Profile 用例、Snapshot 构建状态机、加权 RRF HybridSearchService、`retrieval.build` Executor 和 KnowledgeScope Agent 工具 |
| `internal/infra/{opensearch,embedding/reranker.go}` | OpenSearch BM25 适配器与复用 embedding 凭证的 SiliconFlow rerank 适配器 |
| `internal/infra/repository/retrieval_repo.go` | Profile/Snapshot/pgvector Chunk、attempt fencing 和 Agent 工具审计持久化 |
| `internal/domain/logic/dataset_contract.go` | JSON Schema 受控子集、UI Schema、Item 校验、key_fields、item_key、fingerprint、commit_seq 纯函数 |
| `internal/domain/logic/task_definition.go` | DAG、端口引用、资源类型、Executor Kind、拓扑顺序和定义快照哈希校验 |
| `internal/app/pipeline/dataset_service.go` | 创建不可变 Schema/Dataset，创建和提交追加型 Batch；提交前完成字段校验和稳定排序 |
| `internal/app/pipeline/{asset_service,source_parse_executor}.go` | Asset/AssetSet 用例、逐文件结构化解析、缓存恢复、checkpoint/progress 与首个机器 Executor |
| `internal/app/pipeline/{extraction_service,llm_extract_executor}.go` | Profile 用例、稳定分块、严格 LLM 抽取、原文证据校验、逐单元恢复和 `llm.extract` Executor |
| `internal/domain/logic/record_cleaning.go` | 受控归一化/校验 DSL、确定性类型编码、字段 Diff 与业务规则纯函数 |
| `internal/app/pipeline/{cleaning_service,cleaning_executors}.go` | TransformedRecordSet/ValidationResultSet 用例、逐记录恢复和 `data.transform`/`data.validate` Executor |
| `internal/app/pipeline/{review_service,review_api}.go` | 从 ValidationResultSet 生成不可变 ApprovedRecordSet；审核决定全量覆盖、编辑重校验、重试幂等 |
| `internal/app/pipeline/{publish_service,publish_executor}.go` | `data.publish` 只消费 ApprovedRecordSet，幂等创建 Batch 并复用原子提交事务 |
| `internal/app/orchestrator/definition_service.go` | 创建 TaskDefinition；创建 Task 时固化定义快照、输入资源和 StepRun |
| `internal/app/orchestrator/{executor,scheduler,worker,runtime_service}.go` | Stage B 执行内核：Registry、资源解析、ready-set、lease Worker、暂停/恢复/重试和人工 Gate |
| `internal/app/orchestrator/api.go` | V2 TaskDefinition/Task 入站 DTO 与通用任务快照；HTTP 不直接依赖 domain |
| `internal/app/pipeline/api.go` | V2 Schema/Dataset/Batch 入站 DTO 与增量 Item 读模型 |
| `internal/port/pipeline_repo.go` | 不可变 Schema 与追加型 Dataset 的仓储契约 |
| `internal/port/{asset,parser}.go` | BlobStore/Asset/ParsedDocument 仓储边界与 Reader-based 结构化 Parser 契约 |
| `internal/port/extraction.go` | ExtractionProfile、RecordDraftSet、ExtractionUnit 与 producer fencing 仓储契约 |
| `internal/port/{cleaning,review}.go` | 转换、校验、人工审核 Manifest 与记录级仓储边界 |
| `internal/port/orchestrator_repo.go` | TaskDefinition、TaskResourceBinding、StepRun 仓储契约 |
| `internal/infra/repository/pipeline_repo.go` | Batch 原子提交：锁 Dataset、查 key 冲突、分配连续 seq、写 Item/Batch/Dataset/Outbox |
| `internal/infra/{blobstore,parser}` | 内容寻址本地 Blob 实现；Markdown/DOCX/PDF 结构化区块解析 |
| `internal/infra/repository/asset_repo.go` | Asset/AssetSet/ParsedDocument/Manifest 持久化和 producer attempt fencing |
| `internal/infra/repository/extraction_repo.go` | Profile/RecordDraft 持久化、逐单元恢复和 Step lease + producer attempt 双重 fencing |
| `internal/infra/repository/cleaning_repo.go` | 转换/校验 Manifest、固定 Dataset through_seq、逐记录幂等和 producer fencing |
| `internal/infra/repository/review_repo.go` | ApprovedRecordSet/逐条审核决定持久化、StepRun 幂等键和 Gate 状态防线 |
| `internal/infra/repository/orchestrator_repo.go` | TaskDefinition JSONB 快照、Task 输入绑定和 StepRun 的事务写入 |
| `internal/infra/httpgin/handler_v2_*.go` | `/api/v2` 最小独立入口；Task SSE 从数据库快照 diff，不以进程 Broker 为事实源 |
| `internal/infra/database/migrations/0012_pipeline_v2_foundation.*.sql` | V2 分阶段开发迁移；Legacy 删除后压平为新 `0001` |
| `internal/infra/database/migrations/0013_asset_parse_manifests.*.sql` | `source.parse` 多文件输出 Manifest 与逐文件状态 |
| `internal/infra/database/migrations/0014_extraction_drafts.*.sql` | `llm.extract` 输出 Manifest、稳定 ExtractionUnit 与可追溯 RecordDraft |
| `internal/infra/database/migrations/0015_transform_validation_manifests.*.sql` | `data.transform`/`data.validate` 不可变 Manifest、记录 Diff、问题和分类结果 |
| `internal/infra/database/migrations/0016_approved_record_sets.*.sql` | 不可变 ApprovedRecordSet、全量审核决定、最终字段与 provenance |
| `internal/infra/repository/pipeline_integration_test.go` | PostgreSQL 真机验证 Batch、Outbox、Task 快照和资源绑定 |
| `web/src/pages/v2/V2DefinitionNew.tsx` | 独立流程编排器：空白或模板起点、类型化数据连接、Executor 专用配置、Definition 输出与发布 |
| `web/src/pages/v2/workflowBlocks.ts` | 前端可编排 Executor 目录、输入输出资源类型、默认配置与业务说明；必须与后端 Registry 合同同步 |
| `web/src/pages/v2/V2Definitions.tsx` | 流程定义目录；展示 active Definition 并提供“创建任务”入口 |
| `web/src/pages/v2/NoCodeTaskNew.tsx` | 独立任务派生页：选择 active Definition，按端口绑定本次资源，仅创建或创建并运行 |
| `web/src/pages/v2/V2Tasks.tsx` | V2 任务运行目录；展示来源流程、状态和运行操作 |
| `web/src/pages/v2/SchemaFieldEditor.tsx` | Schema 可视化字段编辑器；面向非技术用户，不把 JSON 作为主界面 |
| `web/src/pages/v2/V2Metadata.tsx` | V2 Schema/Profile 目录与可视化创建入口 |

#### Legacy 代码地图（理解和拆除用）

下方代码已退出产品路由，仅用于识别和拆除。除 LLM/Agent/SSE 等明确复用部件外，不要继续扩展其 Schema 编辑、固定 StepKind、单文件输入或单数据集绑定设计。

```
cmd/reqflow/
  main.go            组装点：配置→DB→迁移→infra→app→http；启动时 taskMgr.Recover
                     （服务重启把卡在 running 的任务/步骤标为 paused 可手动继续）
  static_embed.go    //go:build embed：go:embed dist，SPA 直出（注意：不用 http.FileServer，见 §4.5）
  static_dev.go      //go:build !embed：开发模式空实现（前端由 Vite 提供）

internal/domain/
  model/model.go     Dataset/DatasetItem/Task/TaskStep/TaskItem（草稿字段袋：Fields 为
                     schema 类型化字段 JSON 文本，与 DatasetItem.Fields 同构；Values()
                     为读侧统一解析入口）+ 工作流元数据（Workflow/WorkflowStep/
                     StepDependency/StepKind）+ 状态常量
  logic/             全部纯函数 + 单元测试（改这里不需要起任何服务）
    normalize.go       归一化精确匹配用（全角→半角含 U+3000、小写、空白压缩）
    similarity.go      余弦距离 0-2 → 分数 0-1
    lenientjson.go     LLM 宽松 JSON 恢复三级降级（剥围栏→截取[...]→修复截断数组）
    draft.go           ⭐ NormalizeValues：LLM 输出 → schema 字段袋归一化——默认值
                       （Default 含 {current_time}）/枚举越界回落/数字宽松解析/清洗声明
                       （Clean）全部来自 FieldSpec，代码零字段知识（M2 消灭 B1 双源）
    schema.go          ItemKeyOf（条目主键）/ FingerprintOf（指纹 = 字段值 + 向量相关
                       schema 摘要盐——InKey/InVector/截断变更不再跳过重嵌，M3 已知
                       缺陷修复）/ ValidateValues / VectorDocOf（向量文档组装）/
                       VectorBodyLimit（写入/查询/指纹三方共用）；TitleFieldOf 标题口径
    schema_compat.go   ⭐ 兼容规则引擎（M3 写守卫核心）：CheckSchemaCompat 按 METADATA
                       §4.4 规则表逐条判定（✅/⚠️/❌）；ValidateSchemaShape 形状硬校验
                       （字段 key 白名单 snake_case——key 会拼进过滤 SQL，注入面收口）；
                       ValidateProfileText + {{ 模板注入告警
    workflow.go        工作流形状校验 + 兼容引擎（M4）：ValidateWorkflowShape（kind 封闭集/
                       seq 连续/步骤名唯一——注入面与编排语法收口）/ CheckWorkflowCompat
                       （按步骤名对齐；gate_removed/output_missing 等全 ✅⚠️ 无 ❌——快照
                       天然隔离存量任务）/ IsValidIdentifier 标识符白名单

internal/port/
  repo.go            DatasetRepo/TaskRepo/MetadataRepo（M3：registry/audit 仓储契约，
                     effective = 每 (kind,key) 最大 version 且 enabled）+ 向量 DTO
  llm.go             LLMClient（Stream/Complete/Ping）+ pi 式消息模型（Context/Message/内容块/
                     ToolSpec 全可 JSON 序列化——Context 即会话，暂停检查点与 refine 以它为单位；
                     Context.TaskSchema 为任务产出 schema 快照——Resume 重放按执行时
                     schema，元数据热编辑不影响进行中任务）+ 流事件协议
  embedding.go       Embedder（Available() 驱动降级）
  parser.go          DocParser

internal/app/        用例层；全部依赖构造注入，进度用回调上报
  registry.go        ⭐ 任务类型聚合注册表（唯一注册点）：工作流 + 产出数据集类型 +
                     schema + 装配 profile 一处声明（TaskTypeOf/TaskTypes）；旧查找
                     入口（WorkflowOf/AnalyzeProfileOf/写入计划）均为薄委托，一致性
                     有单测钉住；新增任务类型 = 加一条聚合定义（PRODUCT §4 决策二）；
                     M3 起 seed 之上叠加 DB override 合并层（metadataOverrides，整体
                     替换无竞态）——TaskTypeOf 返回 effective 视图，运行时仍进程内
                     调用不经 HTTP；effectiveSchemaOf 是按数据集类型工作的读侧
                     （match/query）统一入口
  workflow.go        工作流定义（半元数据驱动）：步骤链 + 依赖声明（StepKind:
                     parse/human/analyze/dataset）；创建任务时快照进 tasks.workflow
                     （任务自描述，不受定义演进影响）；查找入口委托聚合注册表
  metadata.go        ⭐ 元数据目录用例（M1 只读部分）：Catalog（总览 + M4 向导草稿组）/ TaskTypeView
                     （聚合视图，含分构件 source + custom/draft 徽标字段）/
                     PromptPreview（三段提示词实时渲染，复用运行时渲染器与工具集
                     构造——预览即装配的精确复现）
  metadata_edit.go   ⭐ 元数据受控编辑（M3 写路径）：Reload（seed→override→effective
                     装载，写后整体刷新）/ UpdateSchema（形状校验→兼容引擎→❌拦截
                     ⚠️confirm_risky→版本递增+审计）/ UpdateProfile / Reset*（enabled=
                     false 最新版回退 seed，版本历史保留）/ History / Export / Import
                     （逐项同一守卫；M4 起新类型按向导注册为草稿）/ UpdateWorkflow /
                     ResetWorkflow / SetWorkflowStatus（启停翻锚行）/ Reload 装 kind=workflow +
                     向导扩展类型（锚行 enabled 且三构件齐备才装载）；effective 版本 =
                     seed.Version + 注册表版本（扩展类型无 seed 基线则仅行版本）
  metadata_wizard.go ⭐ 新任务类型向导（M4）：RegisterTaskType 整体校验→三行落库
                     （schema/profile 生效态就绪、workflow 锚行 disabled=草稿）→即时
                     提示词预览；lookupDraftRows/WizardDraftView 草稿组合视图；
                     composeExtensionDefinition 构件组装（Write 绑定 DefaultWriteSpec）
  task.go            ⭐ TaskManager 任务门面：CRUD/编辑/触发/暂停/继续/完成/Recover +
                     运行登记表（每任务单写者 goroutine）+ 数据集浏览透传——httpgin 唯一任务入口
  runner.go          ⭐ 步骤执行器小接口（parse/analyze/dataset 服务结构即满足，测试注入假实现）
                     + 按 StepKind 分发执行 + 元数据驱动的门推进（advanceGate）/
                     触发校验（canTriggerStep）/ 暂停恢复；状态机转换先落库后发 Broker；
                     触发时**同步预置任务级状态**（见 §3.3）；token 事件 150ms 节流合并（见 §4.2）；
                     错误分类（ctx 取消→paused / 其他→门内重试或终态）
  broker.go          ⭐ 进程内事件扇出（非阻塞发布，订阅/退订锁内串行）——SSE 可重接的基础
  analyze.go         流式分析编排（agent 模式：文档经工具阅读 → write_work_items 分批产出 →
                     必要时 ask_human 问人；sink 空则降级单发直调：prompt 渲染→流式→宽松
                     恢复→非流式回退）→ 产出 AnalyzeOutcome（明细+会话 JSON+存档路径，不落库
                     ——持久化移交 TaskManager）+ Resume（从 agent_context 检查点重放 sink 续跑）
  analyze_profile.go 任务类型 → agent 装配描述（聚合注册表的 agent 侧构件；AnalyzeProfileOf
                     委托注册表）：指令头（{field_spec} 占位由产出 schema 渲染）+
                     写入工具绑定 + 单发示例。提示词零固定模板：字段规范段从 schema
                     生成（FieldSpec.Prompt 同源），新增任务类型 = 聚合注册表加一条，装配零改动
  dialog.go          ⭐ DialogHub 人工交互桥：ask_human 工具阻塞登记 → SSE dialog 事件 →
                     HTTP Answer 投递；pending 随 SSE 快照下发（刷新恢复弹窗）；ctx 取消
                     即空回答收束（任务暂停检查点语义不变）
  dataset.go         ⭐ DatasetWriter 数据集生成用例：草稿 → 向量化（分批）→ 幂等写入数据集；
                     任务产物的落点，任务间衔接的载体；含 DraftSaveInput 草稿 DTO
                     （{id,fields}——schema 字段袋，形状由任务类型产出 schema 驱动）
  prompt.go          ⭐ 提示词动态装配渲染器（零固定模板）：renderFieldSpecSection（schema →
                     字段规范段，类型/枚举值域/必填自动标注 + FieldSpec.Prompt 提取说明）/
                     renderAnalyzeHead（profile.Role 的 {field_spec} 占位替换）/ renderClassicOutputFormat
                     （单发契约 + 示例：profile.Example 覆盖或 schema 骨架生成）/ renderAgentSystem
                     （+ DocumentedTool 工具指南）/ renderDocManifest（agent 首轮文档清单 + 首步指引）
  match.go           查重（两层匹配：归一化精确 + 向量语义，语料 = 需求数据集；入参为
                     schema 字段袋，标题/向量文档口径由 requirement schema 驱动）
  parse.go settings.go overview.go
  bug/doc.go         Legacy Bug 域设计存档；只提取业务字段与人工确认需求，禁止
                     再接入旧 TaskManager（新任务实现顺序见 §5.4）
  tools/             ⭐ agent 过程工具（pi 工具模式，按运行构造，tools.BuildForRun）：read.go
                     （read_document 行号分页+续读提示+超长行硬拆）/ search.go（search_document
                     正则/字面量 grep 式输出+可行动截断提示）/ write.go（写入工具+DraftSink：
                     WriteSpec={Name,Schema} 绑定任务产出 schema；草稿为字段袋 map，key =
                     ItemKeyOf（与数据集条目身份同一口径）；归一化走 logic.NormalizeValues，
                     同 key 覆盖增量产出、replace_all 整体重写、逐条校验即时回执；ReplayFrom
                     按同一 WriteSpec 从会话重放）/ ask.go（ask_human 经 HumanAsker 阻塞问人，
                     options 候选单选）；splitLines 全包共享，行号口径一致
  agent/loop.go      pi 式 agent loop 骨架（Tool 接口 + 自然终止 + MaxIterations 安全阀 +
                     length 截断整批 fail + ToolOutput 的 output/details 拆分）；ctx 取消即
                     干净中止并返回已积累 Context——任务暂停检查点的载体

internal/infra/
  config/config.go   YAML+env 覆盖（反射走 env tag）、Validate（dsn 硬校验/其余降级 warns）、
                     FilledSecrets/CheckExampleLeak（安全自检）；example.yaml 内嵌（首启生成模板）
  database.go         GORM 连接（重试）+ 手写迁移器（内嵌 SQL，schema_migrations 表，幂等）
  migrations/         0001_init（projects/work_items 等，已被 0005 剪除）/ 0003_tasks（任务三表）
                       / 0004_workflow（tasks.workflow 列）/ 0005_datasets（datasets/
                       dataset_items 建表 + 任务衔接列 + DROP 平台语料表）/ 0006_dataset_generic
                       （数据集通用底座：item_key/fingerprint/元数据列）/ 0007_archive（归档表）
                       / 0008_task_items_fields（task_items 推倒重建为字段袋：fields TEXT JSON，
                       与 dataset_items.fields 决策一致）/ 0009_metadata_registry + 0010_metadata_audit
                       （M3 元数据覆盖层与审计，payload 同为 TEXT JSON）；研发阶段无数据搬迁，改表直接推倒重建
  repository/        仓储实现（GORM + pgvector；dataset_repo 的 Raw SQL 向量检索注意
                      **显式列映射**——嵌套结构体 Scan 会丢 fields 列，踩过坑；
                      metadata_repo 的 LatestEntries 用 DISTINCT ON 取每 (kind,key) 最大版）
  llm/               双协议适配器（均移植自 pi，偏离清单见 §4.6）：client.go 工厂按 provider 分发
                     openai.go——OpenAI 兼容 /chat/completions（reasoning 三字段防重复、
                       tool_calls 流式增量聚合、缺 finish_reason 时推断）
                     anthropic.go——Anthropic Messages 协议（SSE 状态机、thinking 签名回放、
                       连续 toolResult 合并为单条 user 消息）
  embedding/         OpenAI 兼容 /embeddings（批量、按 index 归位）
  parser/            parser.go（分发+docx 标准库 zip+XML）mineru.go（四步云端解析）xlsx.go（行级解析，第二波用）
  httpgin/           server.go（路由表）sse.go heartbeat.go handler_tasks.go（任务/工作流/数据集端点）
                     handler_misc.go handler_match.go handler_metadata.go（元数据目录 3 端点）
                     handler_metadata_edit.go（M3 受控编辑 + M4 工作流/向导端点；守卫拦截
                     409 时判定明细随 data 载荷带回前端：workflows/:type 的 check/PUT/DELETE/
                     status 启停 + POST task-types 向导注册）
  database/migrations 见上

web/                 React 18 + AntD5 + ProLayout + TanStack Query + react-router
  src/hooks/useTaskEvents.ts ⭐ 任务 SSE 订阅：snapshot 整包写缓存 + task/step/items 补丁，
                     token/progress/tool_trace/dialog 只进页面本地态（不落缓存）；dialog 为
                     阻塞事件——pending 随 snapshot 恢复（刷新/重连弹窗不丢）、按 call_id 幂等；
                     **断线 3s 自动重连（重连重收快照）；snapshot 帧带 data 包装，与实时事件形状
                     统一**；卸载即退订
  src/api/sse.ts             GET/POST SSE 解析器（fetch + ReadableStream）；**单帧异常只丢该帧不断流**
  src/api/v2/                V2 snake_case DTO 与 Task/Schema/Dataset/Validation/Approved API；
                             与 Legacy PascalCase 类型隔离，不建立过渡兼容映射
  src/hooks/useV2TaskEvents.ts V2 GET SSE：完整 Snapshot 写入 `['v2-task', id]` 缓存，
                             断线 3 秒重连；事件只加速收敛，数据库快照仍是事实源
  src/pages/v2/              当前全部产品页面：Definition 编排与目录、从流程派生 Task、任务运行、
                             Dataset/Metadata/Retrieval/Artifact/Archive；V2TaskDetail 按定义快照
                             渲染步骤与资源，ReviewWorkspace 按 Schema 渲染审核与证据面板
  src/api/tasks.ts           任务 API 封装（创建/列表/详情/编辑/暂停/继续/完成/步骤触发/草稿保存/数据集浏览）
  src/pages/Tasks.tsx        任务列表（状态筛选 + 生命周期操作）
  src/pages/Datasets.tsx     数据集浏览（结果集 + 条目明细 + 来源任务追溯）
  src/pages/Metadata.tsx     元数据目录：任务类型聚合视图（步骤链/工作流编辑/字段合同/
                             装配描述）+ 提示词预览（可填额外要求实时渲染）+ 导出按钮 +
                             「新建类型」入口 + 待启用草稿组（详情页一键启用），/metadata 路由
  src/pages/MetadataWizard.tsx 新任务类型向导（M4）：类型标识/数据集类型 → 步骤链编排
                             （StepsEditor 增删移位）→ 字段合同轻量编辑 → 指令头填写 →
                             提交注册为草稿，右栏即时判定 + 三段提示词预览；?edit= 回填草稿
  src/pages/MetadataEditors.tsx ⭐ M3 受控编辑器：SchemaEditor（字段合同编辑：行点击
                             弹窗改 Label/枚举/属性/提取说明 → 保存自动 check → 判定
                             弹窗 ❌标红/⚠️勾选确认/影响面数据集表 → 保存生效）+
                             ProfileEditor（指令头/示例）+ WorkflowEditor（步骤链编排：
                             StepModal 改名/kind/依赖、上下移、check 保存流）+
                             HistoryDrawer（版本历史，kind 含 workflow；点选两版 LCS 行 diff）；
                             409 守卫拦截不抛错——判定明细随响应 data 带回（api.putDetail）
  src/pages/tasks/           详情页 TaskDetail（头部+步骤时间线+按阶段工作区；analyze 步骤
                     标签按 settings.llm.agentMode 如实显示）+
                     panels/（ConfirmParsePanel / AnalysisPane（双区实时滚动+工具轨迹+人工
                     交互 Modal：候选单选或自由文本，可关闭保留重开入口防丢）/ MatchImportPanel）
                     + TaskNew（工作流预览，步骤标签同上）
  其余页面：Overview/Settings(Bugs 占位)；Settings 含「分析模式」展示（agent 工具驱动/单轮直调）
```

### 3.3 Legacy 任务系统不变量（拆除 task/runner/broker 前必读）

> 本节描述当前仍可运行的旧路径。V2 不继续使用进程内 goroutine 作为执行事实源，但 SSE 的 persist-then-publish、快照恢复、token 节流和 Agent Context 检查点应保留。

- **执行脱离 HTTP**：步骤跑在 `TaskManager.spawn` 的 goroutine 里，持有独立可取消 ctx（`context.WithoutCancel` 派生 persistCtx 供收尾落库）；触发端点 fire-and-forget，进度走 `/events` 订阅。
- **触发同步预置（fire-and-forget 竞态根除）**：`triggerStep` 在 spawn 前**同步**把任务置为 `running + 目标步骤序号` 并落库 + 发布 task 事件——202 返回时 DB 已是新状态，客户端任意时刻的 GET 都拿不到旧状态；goroutine 内 beginStep 落库的是相同值（幂等）。spawn 被拒（并发触发）时回滚预置。
- **每任务单写者**：任务/步骤的 DB 写入只发生在步骤 goroutine 内（`running` 登记表保证）；生命周期操作（Pause 等）先取消、等 `<-done`、再重读 DB 定夺（取消落地前已自然完成 → 报「任务已完成，无法暂停」，不覆盖终态）。
- **persist-then-publish**：所有状态变更先落库再发 Broker；Broker 非阻塞发布（通道满丢帧，快照兜底）；SSE 端点**先订阅再回放快照**（消除竞态窗口），客户端断开只退订。
- **SSE 帧形状统一**：所有事件（含 snapshot）负载统一 `{"task_id","data":…}` 包装，前端统一 `payload = data?.data` 解包——**新增事件类型必须带 data 键**（形状不一致会杀流，踩过坑）。前端单帧解析/回调异常只丢该帧；断线 3s 自动重连重收快照。
- **token 节流合并**：token 事件逐 token 一帧会打爆 broker 64 缓冲（慢消费者丢帧→推理/正文面板空白），`execAnalyzeStep` 内按 150ms 窗口合并成批帧（阶段切换先 flush 旧段），分析结束兜底 flush。改 token 发布方式时保持节流语义。
- **暂停语义**：analyze=取消 loop ctx → 已积累 `port.Context` 序列化进 `agent_context` → paused；继续=回放 Context 调 `Analyze.Resume`。dataset=向量化分批间取消 → paused（building 数据集保留）；继续=复用同一数据集幂等重建条目。parse=取消重跑（幂等）。
- **步骤失败不杀任务**：requirement_import 步骤失败 → 回到对应人工门（awaiting）可重试；任务可手动完成（awaiting 态）进入终态。
- **人工交互（dialog）是阻塞事件**：ask_human 工具经 DialogHub 登记 pending 后阻塞等待 `POST /tasks/:id/dialog` 应答（loop 顺序执行 → 每任务至多一个 pending）。可靠性走快照而不是瞬时事件：pending 随 SSE snapshot 下发（刷新/重连恢复弹窗），Broker 丢帧也能恢复；ctx 取消（暂停）以 IsError 回执收束——loop 先追加回执再退出，会话保持合法，续跑后模型可重新发问。

### 3.4 Legacy 元数据系统不变量（拆除 metadata/registry 前必读）

> 原《元数据模块设计》（docs/METADATA.md）与《分波执行计划》（docs/METADATA_PLAN.md）已于 2026-08 合并删除，长效决策全部并入本节；历史代码注释中的「METADATA §N」编号指该文档，git 历史可溯。开发范式（定义即数据 / 半元数据驱动）见 PRODUCT §4 决策二。

- **分层真相源（seed → override → effective 三态）**：`app/registry.go taskTypeDefinitions()` 是 code seed（随二进制发布的出厂默认）；`metadata_registry` 表是 DB 覆盖——每 `(kind,key)` 取最大 version 行，该行 enabled 才生效；运行时读 `TaskTypes()/TaskTypeOf()/effectiveSchemaOf()` 的合并视图。**红线：元数据永远进程内供给，绝不经 HTTP 取**。写路径与加载器同进程收口：每次写后 `Reload` 整体刷新缓存，单测钉住「写后立即读」防失效遗漏。
- **合并层结构**：`metadataOverrides` 四张 map——schemas（按数据集类型）/ profiles、workflows（按任务类型）/ extDefs（向导扩展类型聚合定义）。map 只整体替换、绝不原地修改（读侧持旧引用即持旧快照，无竞态）；测试结束必须 `setMetadataOverrides(nil,nil,nil,nil)` 清进程级全局态。
- **锚行双语义（M4 沉淀）**：kind=workflow 行 payload=`{dataset_type, workflow}`，兼作向导注册类型的「锚行」。对 seed 类型 `enabled=false` 表示覆盖关闭（回退 seed）；对向导扩展类型它是**发布开关**——disabled = 整型草稿，装载器跳过（运行时不可见、建任务被拒）。同一列两种语义，改动前先分清身份（`customTaskType()` 是 source 徽标/回退按钮/启停权限的判定枢纽）。
- **数据集类型所有权不变量**：一个数据集类型只能被一个任务类型占用；向导注册必须新建 ds_type 且不得与任何既有定义冲突。ds_type 是任务间衔接的身份键（筛选 SQL、向量集合、item_key 都派生于它），共写一个结果集会打穿闭环——此约束是 M4 现场定案的产品级红线。**M5 补注**：该不变量收敛于**模板层**（模板与任务类型的一一对应）；实例层的绑定不受类型约束——任务创建即绑定任意数据集（`Create(ctx, typ, title, datasetID)`），字段异构由数据集自身 schema 承接，写入门的类型匹配校验已删除。
- **字段定义归属数据集实例（M5 核心反转）**：`datasets.schema`（JSONB）是字段定义的**唯一真相源**——任务绑定数据集后，分析提示词（`AnalyzeInput.SchemaJSON` → `profileWithInputSchema`）、写入校验（`datasetWritePlanFor` 从目标数据集行解析）、门内表格、查询过滤全部按数据集自身的 schema 执行；类型级注册（registry 的 schema 覆盖层）**降级为「数据集类型模板」**，仅在 `DatasetAdminService.CreateDataset` 新建数据集时带出初始定义（可自定义）。数据集字段受控编辑走 `dataset_admin.go`（形状校验 → CheckSchemaCompat → ❌/⚠️ → `UpdateDatasetSchema` 落库 + 审计 kind=dataset_schema key=数据集ID）——同类型的不同数据集从此可各自演进。
- **动态索引随 schema（M5）**：FTS（`FieldSpec.FTS` → 表达式 GIN `to_tsvector(cfg, fields->>'k')`）与筛选（`Filterable` → 表达式 btree）索引是数据集 schema 的**派生物、非迁移资产**——`DatasetIndexer.SyncIndexes` 在创建/受控编辑时 diff 建删、归档时 `DropIndexes` 回收、恢复时重建；索引带 `dataset_id` 部分谓词只覆盖本数据集，确定性命名（sha256 前 12 位）支撑按名 diff。**表达式必须与查询侧逐字一致才命中索引**（`planIndexes` 纯函数 + 单测钉住形状）；`fts.ts_config` 变更会重建 FTS 索引。条目字段袋已升级原生 JSONB（`fields->>'k'` 免 cast）。中文分词需 zhparser/pg_jieba 扩展（`fts.ts_config` 配置）。
- **快照四件套（热编辑安全性的来源；M5 起 schema 载荷入列）**：① 任务创建时把工作流定义快照进 `tasks.workflow`（存量任务自描述，ParseWorkflow 只在快照缺失时回退注册表）；② 数据集创建时把字段定义**固化到 `datasets.schema`**（`schema_version` 记实例编辑计数）；③ agent 会话检查点带 `port.Context.TaskSchema`（Resume 重放按执行时 schema 组装 WriteSpec）；④ 写入策略声明随 `tasks.input` 持久化。因此受控编辑只影响后续写入；工作流兼容引擎也据此全 ✅/⚠️ 无 ❌ 硬拦截。
- **StepKind 封闭集（决策二红线）**：parse/human/analyze/dataset 四种，执行器永远是有类型的 Go 代码。向导只能编排既有 kind（`logic.ValidateWorkflowShape` 白名单拒绝未知值）。「新增任务类型」的正确路径 = 向导注册定义 + 复用/新增 kind 执行器；禁止 `if type ==` 分叉。
- **兼容规则引擎（check dry-run 与保存共用的唯一判定口径）**：
  schema 规则表——新增可选字段 ✅ / 新增必填 ⚠️（仅新写入生效）/ 删除字段·改类型·InKey 变更 · 枚举收窄 · 给自由字段加枚举 ❌ / 枚举扩值 ✅ / Label/Prompt 文案 ✅ / InVector 变更 ⚠️ 需重嵌（`FingerprintOf` 已纳入向量相关 schema 摘要盐——InKey/InVector/截断变更不再跳过重嵌；`logic.VectorBodyLimit=500` 写入/查询/指纹三方共用）；enum→string 特判按放宽处理。
  工作流规则表——按**步骤名**对齐（位置增删不产生连锁误报）：step_added/order_changed ✅；step_removed/kind_changed/gate_removed（移除人工门）/output_missing/multi_analyze ⚠️。
  保存流程恒为：形状校验 → 兼容判定 → ❌ 拦截（409 failWith 携判定明细）→ ⚠️ 必须显式 `confirm_risky` → 版本递增落库 → 审计必记 → Reload。
- **安全护栏四道（写路径）**：① 提示词注入面收口——字段 key 与任务/数据集类型标识过 `logic.IsValidIdentifier`（snake_case 白名单；key 会拼进过滤 SQL `fieldCondSQL`，形状层是唯一防线）、Role/Prompt 有长度上限（MaxRoleLen 等）、文本含 `{{` 序列告警不拦截；② 写守卫见上；③ 审计（metadata_audit）失败只记日志不回滚业务写；④ 回退通道——Reset 追加 enabled=false 行并存回退目标载荷（版本历史保留），自定义类型无内置基线只能停用不能 Reset。
- **回退双轨制（M4 起「版本行只增不改」有官方例外）**：内容变更（update/reset）恒追加新版本行；启停（SetWorkflowStatus）走 `repo.UpdateLatestEnabled` 就地翻转最新锚行标志——发布翻转不是内容变更，无需新版本。
- **导出导入（DX 语义）**：effective 视图导出为 **JSON**（此前文档曾误写 YAML，以本条为准）；导入逐项复用 Update* 同一守卫，单项失败不中断；三件齐备的全新类型按向导注册为**草稿**（不直接生效，人工验证后启用）。
- **新增 kind 的修改点清单（无编译手段防漏，四处手工同步）**：① Reload 的 switch（`metadata_edit.go`）；② History kind 白名单；③ 前端 HistoryDrawer 标题映射；④ Catalog/导出结构。现支持 dataset_schema / analyze_profile / workflow 三种。
- **幽灵场景**：seed/custom 身份是启动时的动态判定——若未来给某个曾是向导注册的类型补了 seed（代码演进撞名），身份自动翻转：版本基线切换成 seed.Version+rowVersion、Reset 按钮出现、锚行语义从发布开关变成覆盖开关。给既有类型写 seed 前先查库同名行。
- **测试接入双轨**：单元 golden 用 `extraTaskTypes` 代码缝注册玩具类型（生产恒空）；真机/E2E 用向导 API 注册。

### 3.5 V2 不变量（新增代码必须遵守）

#### Schema 和 Dataset

- `DatasetSchemaDefinition` 创建后不可修改。结构变化 = 新 Schema + 新 Dataset；不再保留兼容性 dry-run、实例 Schema 版本或原地更新入口。
- JSON Schema 只描述结构和校验；提示词、归一化规则和检索字段必须分别进入 ExtractionProfile、RetrievalProfile。
- Dataset 是长期容器，Schema 和 `key_fields` 创建后固定。日常增量由不可变 DatasetBatch 完成，不是每次任务复制整个 Dataset。
- 当前正式写入语义只有 `APPEND`。已提交 Batch/Item 不 UPDATE/DELETE；已有 `item_key` 冲突时整批回滚。
- `commit_seq` 在单个 Dataset 内连续递增。Task 和 PipelineCursor 使用 `dataset_id + through_seq` 固化读取边界。
- Batch 提交事务必须同时完成 Item、Batch、Dataset.current_seq/item_count 和 Outbox；索引、LLM 和其他外部调用严禁放进事务。
- Item 必须同时具备规范化 fields、稳定 item_key、fingerprint、batch_id、commit_seq 和 provenance。

#### Task 和 Workflow

- `TaskDefinition`（流程定义）与 `Task`（执行实例）必须分离，二者是 1:N；不得重新引入“模板页面同时创建流程和任务”的混合实体。
- 发布 Definition 和创建 Task 是两个独立动作：发布不得自动运行，创建 Task 不得隐式创建或修改 Definition。
- 模板只能生成可编辑 Definition 草案/UI 初始状态；必须同时支持从空白手动编排。
- Task 只能从 `active` Definition 派生。Definition 目录必须提供“创建任务”入口，Task 目录必须展示来源流程。
- TaskDefinition 是端口化 DAG；Step 身份是稳定 `step_id`，`ordinal` 只负责展示顺序，Executor 分发键是 `kind`。禁止重新引入“按 kind 找第一个步骤”。
- 同一个 Kind 可以重复出现。Step 只能读取 `$task.<port>` 或其依赖祖先的 `$step.<step_id>.<port>`。
- 前端数据连接只能选择资源类型匹配的任务端口或上游步骤输出，并据此推导 `depends_on`；业务用户不直接编辑 DAG JSON。
- 编排器只能组合 Registry 已注册的 Executor Kind，并使用对应的类型化配置表单；不得允许任意脚本、任意表达式或未知 Executor Config。
- Task 创建时必须固化完整 definition snapshot 和所有输入 ResourceBinding；Alias 只在创建任务时解析一次。
- 下游读取追加型 Dataset 时必须绑定 `through_seq`；查询知识时必须绑定具体 RetrievalSnapshot。
- StepRun 是执行状态唯一真相源。后续 Worker 使用数据库 lease/checkpoint；Broker/SSE 只通知 UI，不承担状态。
- 人工 Gate 可以放在任意步骤后。Agent 只写草稿，正式 Batch/Artifact 发布继续经过显式审批。

#### 检索

- Query Dataset 是数据，RetrievalSnapshot 是派生服务资源。禁止把物理索引状态写回 Dataset Schema。
- BM25 和 Vector 必须覆盖相同 `source_seq` 并通过计数校验后才能激活 Snapshot。
- 词法后端使用 OpenSearch BM25，向量后端使用 pgvector，应用层用可调权重的 RRF 融合；不得用 PostgreSQL `ts_rank` 冒充 BM25。
- 首期 Dataset 只追加，因此固定 `source_seq` 可以稳定读取。未来若增加 UPSERT/DELETE，必须同时设计版本化搜索文档或双索引切换。

#### 分阶段迁移

- `0012_pipeline_v2_foundation` 当前叠加在旧迁移上，只为让 V2 每批改动可测试；它不是兼容承诺。
- V2 API/Worker/首条清洗链路完成并切流后，删除 Legacy migration、表、handler、前端和测试，再压平为新 `0001_v2_init`。
- 不允许为了保持旧测试或旧页面继续工作而给 V2 模型增加双写、回填或适配分支。

## 4. 执行所必须的上下文

### 4.1 数据模型

**V2 新表和扩展列**：

| 表 | 要点 |
|----|------|
| `dataset_schemas` | 不可变 JSON Schema + UI Schema + schema_hash；没有更新接口 |
| `datasets` 扩展 | `workspace_id/purpose/schema_id/key_fields/current_seq`；Schema 和主键字段创建后固定 |
| `dataset_batches` | staging→committed 的原子追加单元；`from_seq/to_seq/payload_hash` 支撑位点与幂等 |
| `dataset_items` 扩展 | JSONB fields + `batch_id/commit_seq/provenance`；`(dataset_id,item_key)` 和 `(dataset_id,commit_seq)` 唯一 |
| `task_definitions` | 端口化 DAG 定义 JSONB + definition_hash；任务创建时再复制一份快照 |
| `tasks` 扩展 | `workspace_id/definition_id/definition_snapshot`；旧 workflow/input/output 列待切流后删除 |
| `task_resource_bindings` | Task 输入/输出逻辑端口到具体资源的绑定，boundary 固化 through_seq 或 Snapshot |
| `step_runs` | `step_id/ordinal/kind/status/attempt/checkpoint/progress/lease_*`；V2 执行事实源 |
| `assets/asset_sets/asset_set_members` | 原始文件及一次业务输入文件集合 |
| `parsed_documents/document_blocks` | 按解析器版本缓存结构化文档，Block 是 provenance 的稳定引用点 |
| `extraction_profiles` | 与 Schema 分离的抽取、归一化和业务校验配置 |
| `record_draft_sets/extraction_units/record_drafts` | `llm.extract` 的 Manifest、稳定模型调用单元和带 Asset/Block/quote 来源的候选记录 |
| `retrieval_profiles/snapshots/chunks` | 检索合同、覆盖位点、多 Chunk embedding、OpenSearch 文档身份和 Snapshot 构建状态 |
| `pipeline_cursors` | 基础 Dataset → 查询 Dataset 增量消费位点 |
| `artifacts` | Markdown/DOCX/PDF/Graph Manifest 等非 Dataset 产物 |
| `outbox_events` | 与 Batch 同事务写入的异步事件出口 |

**Legacy 表**：`task_steps/task_items/metadata_registry/metadata_audit/archived_*` 以及 `datasets.schema/schema_version/type`、`dataset_items.embedding` 等旧列只被待删除 Legacy 代码引用；纯 V2 页面不依赖这些表，删除时不做数据迁移。

**向量维度当前仍是硬约束**：V2 `retrieval_chunks.embedding` 暂定 `vector(1024)`。首期固定一个平台 embedding 模型；多维模型只能在检索层按维度分表/分区，不能把不同维度混进同一个 HNSW 索引。

### 4.2 核心流程

**V2 已落地的数据写入流程**：

```text
Create immutable Schema
  → Create Dataset(schema_id + key_fields)
  → Create staging Batch
  → DatasetService 校验/规范化 fields
  → 生成 item_key + fingerprint
  → 按 item_key 稳定排序
  → PipelineRepo 事务锁 Dataset
  → 检查已有 key 冲突
  → 分配连续 commit_seq
  → 写 Items + Batch + Dataset 汇总 + Outbox
  → committed
```

同一 Batch 同一 payload 重试直接返回已提交结果；payload 不同则拒绝。第二个 Batch 从上一个 `current_seq + 1` 继续。对应测试见 `internal/app/pipeline/dataset_service_test.go` 和 `internal/infra/repository/pipeline_integration_test.go`。

**V2 已落地的流程定义与任务派生流程**：

```text
/definitions/new
  → 从空白编排，或载入一个可编辑模板起点
  → 选择 Executor，按资源类型连接输入/输出并自动推导 depends_on
  → 显式选择 Definition 输出
  → Validate TaskDefinition DAG/Ports/Refs/Executor Config
  → 发布 active TaskDefinition + immutable definition snapshot/hash
  → 不创建、不运行 Task

/definitions 或 /tasks/new?definition_id=<id>
  → 选择 active TaskDefinition
  → 按 Definition 输入端口绑定本次具体资源
  → 校验必填端口、资源存在性和类型
  → Dataset Alias 单次解析，Dataset/Retrieval Boundary 固化
  → 固化 Task.definition_snapshot
  → 原子写 TaskResourceBinding + 每个 step_id 的 StepRun
  → 用户选择仅创建，或创建后显式 start
```

**Legacy 需求导入主链路（已退出产品路由，仅供拆除对照）**：

```
前端 /tasks/new → POST /api/tasks {type:requirement_import,title} 创建任务（播种 4 步骤）
  → POST /api/tasks/:id/parse (multipart) fire-and-forget：文件存 upload_dir；
    触发时同步预置任务 running+步骤1（见 §3.3），步骤 goroutine（TaskManager.spawn，
    独立可取消 ctx）内调 ParseService.Run：
    txt/md 直读；docx=zip 内 word/document.xml 逐 <w:p> 拼接 <w:t>（标准库）；
    pdf=MinerU 四步：申请预签名链接→PUT 裸字节（不带 Content-Type！）→轮询(5s/10min，
      进度→步骤 detail 落库+Broker 扇出)→下载 zip 取 full.md
    解析成功 → input.parsed_text 落库 → 步骤1 succeeded → 步骤2 awaiting → 任务 awaiting
    （上传暂存文件随即清理；暂停/失败可重试——原文件路径在 input 中）
→ 前端订阅 POST /api/tasks/:id/events（SSE：先订阅再快照回放 + 实时 step/task/items 事件；
  断线 3s 自动重连重收快照）
  → 详情页工作区进入「确认解析」面板：预览/编辑全文 + 额外要求 → 保存（PATCH）→ 开始分析
→ POST /api/tasks/:id/analyze fire-and-forget（触发同步预置 running+步骤3）
  app/analyze 按 TaskType 解析 AnalyzeProfile（指令头/产出 schema/写入绑定/单发示例）：
  【agent 模式（llm.agent_mode，主路径）】
  → SystemPrompt 动态装配 = profile 指令头（{field_spec} ← schema 渲染）+ 额外要求
    + 工具指南（DocumentedTool 同源）；首轮 user 消息 = 文档清单（文件名/行数/字数 +
    「必须先调 read_document」首步指引），原文不进上下文
  → agent.Loop（迭代上限 llm.agent_max_iterations，默认 32）：模型经 read_document
    （行号分页+续读提示）/ search_document（正则/字面量）自主阅读原文；
    write_work_items 分批产出草稿（WriteSpec 校验：schema 必填/枚举/数值，即时回执
    accepted/updated/rejected，模型修正重交；同 key 覆盖可修订）；ask_human 经 DialogHub
    阻塞问人（SSE dialog 事件 + HTTP 应答）
  → 终稿契约：产出 = DraftSink 累积（不再是「末条消息是 JSON 数组」）；末条消息只需简短总结
  → token 增量在 runner 内 150ms 节流合并后经 Broker 透传前端双区滚动（token 不落库——
    瞬时流；工具轨迹在步骤 data 落库，重放走快照）
  → sink 收束（sinkTail）：sink 空（含模型只聊天不写）→ 降级单发直调；loop 中断但已有
    产出 → 保留部分结果 + 告警进度；成功 → DraftSink.Items() 即草稿明细
  → 原文存档（demand_dir，SourcePath）→ 产出 AnalyzeOutcome（明细+会话 JSON+存档路径，
    不落库——持久化移交 TaskManager）
  → 步骤3 succeeded → 步骤4 awaiting → 任务 awaiting（生成数据集门）
  → 暂停：取消 ctx → loop 返回已积累 Context → agent_context 落库（检查点）→ 任务 paused
  → 继续：反序列化 Context → DraftSink.ReplayFrom 重放会话中全部写入调用重建草稿
    （会话即事实源，确定性重放）→ 续跑 loop
  → 分析失败：步骤3 failed → 任务回确认解析门（awaiting）可修正重试（清会话检查点）
  【单发直调（默认模式，也是 agent 降级目标）】
  → 一条 user 消息 = 指令头 + 单发输出契约（profile.Example 富示例或 schema 骨架）
    + 额外要求 + 文档全文 → llm.Stream 流式（thinking/answer 两相位）
  → 解析降级链: json.Unmarshal 标准解析 → logic.ExtractJSONArrayLenient（剥围栏→截取→修复
    截断）→ 流彻底失败时 llm.Complete 非流式重调一次（同一 prompt）；流中断但已有部分输出时
    优先宽松恢复部分结果
  → logic.NormalizeDrafts 白名单归一化 → 同上收束
→ 生成数据集门: 自动 POST /api/match/duplicates（语料 = 已有需求数据集，精确层 + 语义层）
  → 行内查重徽标 → 编辑行（title/priority/type/hours/assignee）→ 保存草稿（POST /tasks/:id/items）
  → 点生成: POST /api/tasks/:id/dataset {dataset_name} fire-and-forget：runner 创建 building 数据集
    → DatasetWriter 分批向量化 → ReplaceDatasetItems 幂等写入（断点续跑/失败重试复用同一数据集）
    → 数据集 ready + 任务 OutputDatasetID 回填 → 任务终态 succeeded
  → 手动完成: POST /api/tasks/:id/complete（awaiting 态把当前门步骤标 succeeded → 终态）
```

**查重（两层，`app/match.go`，语料 = 需求数据集）**：
1. **精确层**：`logic.NormalizeForExactMatch`（全角→半角、U+3000、小写、空白压缩）对全部需求数据集条目标题建索引，命中 score=1。理由：标题是「准标识符」，向量对稀有 token 不敏感；
2. **语义层**：仅对未命中项，批量 embedding（50/批）→ `SearchSimilarDatasetItems`（pgvector `<=>` 余弦距离）→ `logic.DistanceToScore`（1-d/2）→ 阈值 0.75（`match.duplicate_threshold` 可配）。embedding 未配置时精确层照跑（降级）。语义层向量文档格式与 DatasetWriter 一致（`Title: …\nDescription: …`，描述截 500 字）。

**数据集生成（`app/dataset.go` DatasetWriter）**：草稿 → 分批向量化（`embedding.batch_size`）→ `ReplaceDatasetItems` 事务重建（未发布数据集断点续跑 = 幂等重写）→ 数据集 ready。任务终态时 `output_dataset_id` 回填——**任务与任务通过数据集衔接**（bug 分析等后续任务以需求数据集为输入）。

### 4.3 API 速查

V2 API 已挂在 `/api/v2`：Asset/Schema/Profile/Dataset/Batch、TaskDefinition/Task、Retrieval/Analysis/Artifact/Catalog/Archive 等产品能力均已开放。Handler 只调用对应 V2 app service，不回调 Legacy TaskManager，也不双写；完整合同以路由实现和 [V2 方案 §11](./DATA_PIPELINE_V2_PLAN.md#11-api-v2-合同) 为准。

以下均为 Legacy `/api` 端点，只用于拆除和历史行为对照，当前产品页面不再调用：

| 端点 | 类型 | 说明 |
|------|------|------|
| `/tasks` `/tasks/:id` | JSON | 创建 {type,title} / 详情 {task,steps,items}（task.Workflow = 工作流定义快照） |
| `/tasks?status=&type=&limit=` | JSON | 列表 |
| `/workflows` | JSON | 任务类型目录（工作流元数据：步骤链 + 每步依赖声明），创建入口展示用 |
| `/tasks/:id` | JSON | PATCH 编辑 {title?,parsed_text?,special_requirements?}（awaiting/paused 可改） |
| `/tasks/:id/items` | JSON | 批量保存门内草稿 {items:[{id?,fields:<字段袋>}]}（形状由任务类型产出 schema 驱动；items 回读时 Fields 为 JSON 文本） |
| `/tasks/:id/parse` | multipart | fire-and-forget 上传解析（存 upload_dir，立即返回 {task_id}） |
| `/tasks/:id/analyze` | JSON | fire-and-forget AI 分析（暂停恢复走 AgentContext 检查点） |
| `/tasks/:id/dataset` | JSON | fire-and-forget 生成数据集 {mode: create\|merge\|upsert\|replace, dataset_id?, dataset_name?}（断点续跑幂等重建；预览走 /dataset/preview 分桶不落库） |
| `/tasks/:id/dialog` | JSON | 人工回答 agent 的提问 {call_id, answer}（ask_human 阻塞等待的出口；无 pending 或 call_id 不匹配 409） |
| `/tasks/:id/pause` `/resume` `/complete` | JSON | 生命周期：暂停（取消步骤 ctx）/ 继续（按暂停步骤重触发）/ 手动完成（awaiting→终态） |
| `/tasks/:id/events` | SSE | **快照回放 + 实时**：snapshot（含 dialog pending 恢复）/ task / step / items / progress / token{delta,phase}（150ms 合并帧）/ tool_trace{phase,call_id,name,args?,details?,is_error?} / dialog{phase:ask\|close, call_id, question?, options?, reason?} / error + 5s ping 心跳；断开只退订，任务照跑 |
| `/datasets` `/datasets/:id` | JSON | 数据集浏览（结果集 + 条目明细 + 来源任务追溯） |
| `/match/duplicates` | JSON | {items:[字段袋]} → {results:[{index,match|null}]}（语料 = 需求数据集，标题按 schema 标题字段口径） |
| `/metadata` `/metadata/task-types/:type` | JSON | 元数据目录：总览（任务类型 + 向导草稿组 + source/custom/draft）/ 聚合视图（`?include_draft=true` 可读草稿）——前端「元数据」tab 数据源 |
| `/metadata/render/preview` | JSON | {task_type, special_requirements?} → 三段提示词实时渲染（与运行时装配同一函数） |
| `/metadata/schemas/:type/check` | JSON | 兼容性 dry-run {schema} → 判定明细 + 存量数据集影响面（不落库） |
| `/metadata/schemas/:type` `PUT`/`DELETE` | JSON | schema 受控保存 {schema, confirm_risky?, summary?}（❌ 拦截/⚠️ 需确认，409 携带判定明细）/ 回退到内置（版本历史保留） |
| `/metadata/profiles/:type` `PUT`/`DELETE` | JSON | 指令头/示例编辑 {role, example, summary?} / 回退到内置 |
| `/metadata/workflows/:type/check` `POST` · `/workflows/:type` `PUT`/`DELETE` | JSON | 工作流 dry-run / 受控保存（⚠️ confirm_risky，409 携带判定）/ 回退内置——热编辑仅影响新任务 |
| `/metadata/workflows/:type/status` `PUT` | JSON | 启用/停用向导注册的任务类型 {enabled}（就地翻转锚行 enabled；内置类型拒绝） |
| `/metadata/task-types` `POST` | JSON | 新任务类型向导注册：{type, dataset_type, workflow, schema, role, example?, summary?} 整体校验 → 三行落库（锚行 disabled=草稿）+ 即时提示词预览；重提同名草稿版本续链 |
| `/metadata/history/:kind/:key` | JSON | 版本历史（新→旧，含载荷原文；kind = dataset_schema\|analyze_profile\|workflow） |
| `/metadata/export` `/metadata/import` | JSON | effective 视图导出 / 导入（逐项同一守卫，单项失败不中断） |
| `/overview` | JSON | 概览（datasets/datasetItems/tasks + recentTasks/recentDatasets） |
| `/settings` `/settings/test-llm` | JSON | 脱敏视图/连通测试 |
| `/health` | JSON | 存活 |

SSE 事件负载的权威定义在 `infra/httpgin/handler_tasks.go` 与 `web/src/api/types.ts`——**两端同步改**；事件负载统一 `{"task_id","data":…}` 包装（含 snapshot，见 §3.3）。

### 4.4 密钥安全（四道防线，改安全逻辑必读）

1. `.gitignore`：`config.yaml` / `config.*.yaml` 全变体（example 除外）
2. `.githooks/pre-commit`（`make setup` 启用，本机已配）：真实配置文件名直接拦 + 暂存内容扫描；误报逃生 `git commit --no-verify`
3. `scripts/secret-check.sh`：敏感字段非空值 / 带密码 DSN 两类模式；白名单=环境变量名、点分代码标识符、占位符、用户名=密码的本地 DSN；`make test` 必跑
4. 启动自检：`config.CheckExampleLeak` 发现 example 模板被填真实密钥 → ERROR 告警提示轮换；`Config.FilledSecrets` 只打名单不打值

**密钥真泄漏了怎么办**：先平台轮换，再清 git 历史（filter-repo），不要只删文件。

### 4.5 踩坑记录（长期有效的坑，改相关代码前扫一眼）

**环境（本机）**：
- Go 代理已配 `goproxy.cn`；Docker 是 **Docker Desktop**，拉镜像走 `docker.m.daocloud.io/pgvector/pgvector:pg16` 后打回标准 tag
- 会话启动链可能残留 `GOROOT`（旧版本）→ vet/build 报 "package cmp is not in std"；`~/.zshrc` 已有 `unset GOROOT` 兜底，脚本/CI 场景用 `env -u GOROOT` 前缀
- 本机 `grep` 被 shell 函数包装为 **ugrep**，与 BSD grep 行为有差异（调试时 `which -a grep`）；无本机 `psql`/`timeout`——查库用 `docker compose exec -T postgres psql -U reqflow -d reqflow -c "…"`

**代码级**：
- **Go `\s` 不含全角空格 U+3000**（JS 才含）→ `normalize.go` 显式转换，有单测
- **`net/http` FileServer 会把 `/index.html` 请求 301 到 `./`** → `static_embed.go` 手写 `c.Data` 直出，别改回 FileFromFS
- **macOS bash 3.2**：`$()` 内嵌套 `case` 的 `)` 解析报错；`$VAR` 后紧跟全角字符会把高位字节当变量名一部分 → 一律写 `${VAR}`
- GORM `Create(&emptySlice)` 会报错 → 仓储层全部 `len==0` 提前 return；**UUID 列必须仓储层显式赋 `uuid.NewString()`**（GORM Create 带空串写入导致 `invalid input syntax for type uuid`）
- pgvector 参数化查询：`pgvector.NewVector([]float32)` 实现 Valuer，Raw SQL 直接当参数传
- LLM SSE 单行可能极大 → scanner.Buffer 扩到 8MB（`llm/client.go`）
- MinerU 四步解析：**PUT 预签名必须裸字节不带 Content-Type**（会 SignatureDoesNotMatch）
- 工具 Spec 的 Parameters 是 JSON Schema 字符串——手拼容易漏 `"properties":{` 的开括号，非法 JSON 会连带会话序列化与 LLM 请求一起挂；tools_test 有 `json.Valid` 校验防再犯

### 4.6 LLM 层与 pi 的传承（改 loop/协议时保持同步）

port/llm.go 消息模型、infra/llm 双适配器、app/agent loop 与过程工具均移植自 **pi**（https://github.com/earendil-works/pi，MIT License, Copyright (c) 2025 Mario Zechner，源码参考副本在 `/Users/weighingzhang/demo/pi`）。各文件头注有对应源文件映射。移植要点：

| ReqFlow 位置 | pi 出处 | 移植要点 |
|---|---|---|
| `port/llm.go` | `packages/ai/src/types.ts` | Context{SystemPrompt,Messages,Tools} 全量可序列化；Message 三角色 + text/thinking/toolCall 内容块；StopReason 六态；流事件协议 |
| `infra/llm/openai.go` | `api/openai-completions.ts` | reasoning_content/reasoning/reasoning_text 三字段取首个非空（防重复返回）；tool_calls 按 index 聚合；assistant 回放为纯字符串（块结构会被部分端点镜像导致递归嵌套）；空 content+空 tool_calls 的 assistant 跳过 |
| `infra/llm/anthropic.go` | `api/anthropic-messages.ts` | SSE 事件状态机；thinking 块签名回放（扩展思考+工具调用场景 API 强校验）；连续 toolResult 合并为单条 user 消息的 tool_result 块 |
| `app/agent/loop.go` | `packages/agent/src/agent-loop.ts` | 自然终止（无工具调用即停）；**length 截断的工具调用一律不执行、整批错误回执让模型重发**；error/aborted 短路保留已积累消息；ToolOutput 的 Output(LLM)/Details(UI) 拆分；terminate 语义 |
| `app/tools/read.go` | `packages/coding-agent/src/core/tools/read.ts` | 行号分页（与检索行号咬合）；截断附「用 offset=N 继续读取」行动性提示；单行超限硬拆 + 检索指引（我们无 shell 兜底） |
| `app/tools/search.go` | `packages/coding-agent/src/core/tools/grep.ts` | grep 式 `行号:内容` 纯文本输出；literal 转义 / ignore_case / context 行；命中超限「limit 翻倍或收窄」、行超长「用 read 看整行」的可行动提示 |
| `agent.DocumentedTool` + prompt 装配 | pi 的 promptSnippet/promptGuidelines + agent-session 系统提示组装 | 系统提示词的工具指南从实际工具集组装——工具增删提示词自动跟随（防漂移的结构性解法） |

**有意偏离 pi**（改 loop/协议时保持同步）：
1. 回调事件替代 async iterator；中止走 `ctx`（Go 惯例）
2. `Loop.MaxIterations` 安全阀——loop 层默认 8，分析 agent 模式经 `llm.agent_max_iterations` 提到默认 32（分批阅读大文档需多轮，50k 字 ≈ 10+ 读取轮）；pi 刻意无上限，生产导入必须兜底
3. 缺 finish_reason 的兼容端点按「有无工具调用」**推断**终止（pi 对声明支持 finish_reason 的端点直接报错；我们面向杂牌兼容端点从宽）
4. 不移植：模型注册表/厂商 compat 矩阵/steering 消息队列/beforeToolCall 钩子/prepareNextTurn 换模型/并行工具执行/deferred 响应——需要时按 pi 源码对应段落补

## 5. 接手后干什么（按优先级）

### 5.1 ✅ Orchestrator V2 阶段 B 后端已完成

目标：让 StepRun 从“已落库”变成“可被持久化 Worker 调度、恢复和完成”，但暂时不接具体清洗业务。

已完成：

1. `internal/app/orchestrator` 已定义 `StepExecutor`、`StepRunContext`、`StepResult`、CheckpointWriter 和 ProgressReporter；Registry 按 Kind 唯一注册，`human.review` 是内建 Gate，禁止注册成 Worker Executor。
2. 新增 `step_resource_bindings`，保存每个 StepRun 的输出端口资源。下游 `$step.<step_id>.<port>` 必须从这里解析，不能塞回 `step_runs.progress`。
3. 扩展 `port.OrchestratorRepo`：领取 queued Step、续 lease、保存 checkpoint、完成/失败 Step、写 Step 输出、回收过期 lease。
4. PostgreSQL 领取使用 `FOR UPDATE SKIP LOCKED`。`lease_owner + lease_until` 是并发所有权，更新必须带 owner 条件，防止过期 Worker 覆盖新 Worker 状态。
5. Scheduler 根据 definition snapshot 和 StepRun 状态计算 ready 集合：依赖全 succeeded 才能 queued；`human.review` 进入 awaiting，不交给普通 Worker。
6. Step 成功后在一个事务内写输出绑定并更新 StepRun；随后调度下游。所有 Step 成功后，按 `output_bindings` 固化 Task 输出端口并结束 Task。
7. pause 通过 `pausing` 过渡态取消执行并保留有效 lease 写最后 checkpoint；resume 重新计算输入哈希后排队。Worker 崩溃由 lease 到期恢复，不使用 Legacy `TaskManager.running` 作为事实源。

通用 Task Detail 已使用稳定快照形状接入；V2 Task 目录查询与 Legacy 物理表按 `definition_id` 隔离，生命周期写入仍只经过 RuntimeService。

验收：

- 同一 Step 不会被两个有效 lease 同时完成。
- Worker 进程中断后可以从 checkpoint 恢复。
- 同一个 Kind 的两个 Step 分别执行，输出不串。
- Human Review 可以位于任意 DAG 位置。
- Broker 丢事件或服务重启不影响数据库里的最终状态。

### 5.2 ✅ V2 基础 API 与任务读模型已完成

已挂 `/api/v2` 并由 PostgreSQL + `httptest` 集成测试打通：

```text
POST /schemas
GET  /schemas/:id
POST /datasets
GET  /datasets/:id
POST /datasets/:id/batches
POST /batches/:id/commit
POST /task-definitions
POST /tasks
GET  /tasks?workspace_id=&status=&limit=
POST /tasks/:id/start|pause|resume
GET  /tasks/:id
GET  /tasks/:id/events
GET  /pipeline-cursors?pipeline_key=&source_dataset_id=&target_dataset_id=
GET  /datasets/:id/items?after_seq=&through_seq=
```

要求：

- Handler 只调用 V2 app service，不调用 Legacy TaskManager 或 DatasetAdminService。
- Schema/Profile 没有 PUT/PATCH；当前可视化创建器通过新资源承载结构或合同变化，不做原地兼容编辑。
- 创建 Task 时 Dataset Alias 必须解析为具体 DatasetID，并固化 through_seq。
- SSE 使用统一 `{task_id,data}` 帧形状，但 V2 直接每秒对数据库快照做 diff；这样跨进程 Worker、Broker 丢帧和重连都不影响恢复。后续若为降延迟加 Broker，只能作为唤醒信号，不能替代数据库快照。
- V2 页面已经独立跑通并完成切流；继续删除无路由引用的 Legacy 页面、Handler 和模型，不做新旧双写。

### 5.3 ✅ 第三个里程碑：产品规格清洗纵向切片

目标流程：

```text
AssetSet → source.parse → llm.extract → data.transform → data.validate
→ human.review → data.publish → DatasetBatch
```

实现重点：

- `BlobStore` port + 本地文件实现，Asset 按 SHA-256 去重。
- Parser port 返回 ParsedDocument/DocumentBlock，不再返回整篇 string。
- 每个 Asset 独立状态和 checkpoint，单文件失败不拖垮整批。
- 抽取工具参数由目标 JSON Schema 生成；候选记录必须带 Block 引用和原文证据。
- 单位、日期、枚举、布尔和 item_key 使用确定性代码处理，不让 LLM 决定最终编码。
- 审核 UI 展示置信度、校验结果、重复/冲突和 provenance。
- `data.publish` 直接调用现有 V2 DatasetService/Batch 仓储，不重写提交事务。

完成标准：第二次任务能向同一 Dataset 追加 Batch，且所有新 Item 都能追溯到 Asset/Block。

纵向切片已经完整闭环：`ParsedDocumentSet → RecordDraftSet → TransformedRecordSet → ValidationResultSet → ApprovedRecordSet → DatasetBatch` 均为一等资源。审核 API 只接收逐条 approve/edit/exclude 决定，服务端生成资源并记录修改、排除、审核人和确认依据；`data.publish` 只消费 ApprovedRecordSet，按 StepRun 幂等创建 Batch，并在同一提交事务前验证当前 attempt。

前端已完成 `/tasks` Task 目录、通用详情和审核工作台。ValidationResultSet API 聚合候选原值、字段置信度、转换 Diff、问题与来源锚点；前端按不可变 Schema 渲染类型化编辑控件并提交全量决定。2026-08-30 的真实浏览器验收完成了“编辑冲突记录 + 排除重复记录 → ApprovedRecordSet → Worker 原子发布 → SSE 收敛终态”，且发布 Item 保留完整审核 provenance。

### 5.4 ✅ 第四个里程碑：Query Dataset 增量处理

已落地 `data.query_derive` V2 Executor：输入是任务创建时固化的 Base Dataset
`through_seq` 与目标 Query Dataset，配置从 TaskDefinition 快照读取。每个 Base Item
可以作为一个语义单元，也可以通过 `semantic_units_field + unit_key_field` 一对多展开；
标准输出字段为 `semantic_unit_key/source_item_id/source_fingerprint/title/aliases/definition/keywords/facets/source_refs`。

PipelineCursor 以 `(pipeline_key, source_dataset_id, target_dataset_id)` 唯一定位。
Query Batch 提交事务使用 Cursor 期望位点做 CAS，并在同一事务中完成 Dataset Item、
Batch、Dataset 汇总、Outbox 和 Cursor 推进。PostgreSQL + V2 Worker 集成测试已经覆盖：
第二批不重跑第一批、目标主键冲突时 Cursor 不前移、Query Item 能追溯到 Base Item 和 Asset/Block。

这条链路不依赖 Legacy TaskManager、固定四步 Workflow 或旧任务状态；旧任务流程无需兼容。

### 5.5 ✅ 第五、六个里程碑：混合检索和无代码业务任务

顺序固定：

1. [x] PipelineCursor + 基础 Dataset 增量派生 Query Dataset。
2. [x] RetrievalProfile 校验和 RetrievalSnapshot 构建状态机。
3. [x] OpenSearch BM25、pgvector Chunk、加权 RRF HybridSearchService 和可选 rerank。
4. [x] `list_knowledge_sources/search_knowledge/get_knowledge_item` Agent 工具、KnowledgeScope 和审计。
5. [x] `agent.analyze`、通用资源审核、`data.analysis_publish`、`artifact.render`、`graph.build`。
6. [x] 数据清洗入库、精准 + 语义索引、Bug 分析、产品方案生成、知识图谱构建五种无代码模板。
7. [x] 纯 V2 前端路由、任务目录/详情、Definition、Dataset、元数据、检索、Artifact 和归档恢复。
8. [x] 模板降级为可编辑 Definition 起点，并支持从空白手动组合 Executor、连接类型化数据端口和选择流程输出。
9. [x] 发布流程与创建任务彻底分离；Definition 目录提供创建任务入口，Task 目录展示来源流程。
10. [x] Schema/Profile 改为可视化表单操作，非技术用户无需直接编写 JSON。

业务差异只能进入不可变 Profile、资源绑定和 TaskDefinition；不要从 Legacy `internal/app/bug/doc.go` 恢复 BugRunner，也不要在 Orchestrator 核心状态机增加业务类型分支。

### 5.6 当前现场状态和常见误区

- 不假设工作区干净；每次接手先看 `git status --short`，不要用 reset/checkout 回退他人修改。
- 本机开发库已经执行到 `0018`。如果迁移变化导致本地列不一致，直接重建开发数据库，不为未上线草稿数据追加兼容迁移。
- `0012` 最终会被压平，不要围绕旧 `datasets.schema/schema_version`、`task_steps` 或 `metadata_registry` 设计新外键和新功能。
- `DatasetService.CommitBatch` 当前逐条 INSERT，正确性优先。真实批量压测出现瓶颈后再改成参数化批量 SQL，必须保持同一事务与 seq 顺序。
- `resource_id` 当前是通用 UUID，没有数据库级多态外键；资源类型和存在性应由 app service 校验。
- 本机 `config.yaml` 的 OpenSearch、embedding 和 SiliconFlow rerank 已做真实连通与混合检索验证；配置值属于本地密钥，不得写入仓库。上线环境仍必须把 OpenSearch 和 rerank 连通性纳入健康检查与部署门禁。
- 2026-08-30 无代码自由编排真实验收：从空白发布 Definition `20d9d641-de77-4229-af95-c38efa33bfda`，派生并立即运行 Task `1236c6e2-ceb7-4a10-80dd-94e4916d4c5b`，成功产出 Retrieval Snapshot `63aae631-0884-44dc-8d2f-8b5f037f08ec`；浏览器 console 为 0 error / 0 warning。
- 上述验收固定了产品路径：`/definitions/new` 编排并发布 → `/tasks/new?definition_id=...` 绑定资源并派生 → `/tasks`/详情运行；不要再把三个动作合并回模板页。
- 全仓 `make test`、V2 targeted tests 和 PostgreSQL integration tests 应保持全绿。

### 5.7 Legacy 旧路线记录（只作业务背景，不按此继续开发）

#### 第二波 Bug 链路任务化（旧方案）

以下是 V2 决策前的实现拆分，只用于提取 Bug 字段、人工确认和定级等业务需求。**不得再将 `bug_import` 接入 Legacy TaskManager，也不得继续创建下列 Legacy 专用表。** 原设计存档在 `internal/app/bug/doc.go`，其业务闭环为：**消费需求数据集 → 产出 Bug 数据集**。旧拆分如下：

1. **迁移** `0008_bug.up.sql`（0004-0007 已占用）：`bug_batches`(id,file_name,source_path,status,created_at) / `bug_rows`(id,batch_id,raw_jsonb,编号,标题,描述,复现步骤等归一化字段,analyzed_priority(priority p0-p3),priority_rationale,status) / `bug_matches`(id,bug_row_id,candidate_work_item_id,score,match_type,rank(1-3),human_decision)
2. **port**：`BugRepo` 接口 + DTO
3. **infra/repository**：实现（照抄 task_repo.go 模式：row 结构进 repo.go + 转换器）
4. **app/bug 用例**：`ImportBatch`（`parser.ParseXLSXRows` 已就绪，表头→行映射，去空白/空行跳过/重名列去重已处理）/ `MatchBatch`（**以需求数据集为关联底料**：有编号→`NormalizeForExactMatch` 后与数据集条目标题精确匹配；无编号→复用两层匹配取 **top3**；`task.input_dataset_id` 指定消费哪个需求数据集，缺省查全部） / `ConfirmMatch`（人工确认/否决）/ `Prioritize`（批量 LLM 定级 P0-P3）/ `GenerateDataset`（复用 DatasetWriter 产出 Bug 数据集，关联需求以「关联需求: {标题}」写入字段）。**定级优先复用现有 analyze 步骤的 agent 模式**：注册 BugSchema（字段含 priority/rationale）+ bug 任务的 AnalyzeProfile（指令头换成定级语境，语料 = bug 行集渲染成文本），read/search/write/ask 四工具与提示词装配全部白拿；prompt 要点落在 schema 的 FieldSpec.Prompt（给出理由 rationale、P3 判定保守），不再手写输出契约
5. **任务接入**：`TaskManager` 增加 `bug_import` 任务类型——注册表播种步骤链（Excel 导入→匹配确认门→AI 定级→生成 Bug 数据集）+ 步骤 goroutine 包装（runner.go 同构，StepKind 复用 analyze/dataset/human）；上传/确认/重试语义对齐 requirement_import（失败回门可重试、暂停可续跑、逐条幂等）
6. **前端**：替换 `web/src/pages/Bugs.tsx` 占位 → 任务详情页新增工作区面板（上传→匹配确认表格（top3 候选单选/否决/标无效）→定级面板（可改档）→生成数据集），复用 TaskDetail 步骤时间线 + panels 模式
7. **顺手可做**：embedding 密钥池（多 key 轮询、429 冷却按 Retry-After、401 摘除——只在 `infra/embedding` 内改）；LLM refine 微调会话（分析会话已随任务落库，追加消息重放即得 refine，进程内 map 按任务前缀缓存即可）

#### Agent 工具化演进记录

**红线（产品级，不可松动）**：读操作工具可自主调用；写持久存储（数据集生成 / 条目写入）不得成为 loop 的自主工具——生成数据集仍由人工在任务门内点击触发（PRODUCT §4 决策四）。write_work_items 写的是内存 DraftSink（草稿），落库仍走人工确认，红线未破。

**当前工具集**（四件套的行为细节见 §3.2 代码地图与 §4.2 流程图）：read_document / search_document / write_work_items（+ DraftSink）/ ask_human，按任务类型 profile 经 `tools.BuildForRun` 注入（WriteSpec 绑定产出 schema）；每个工具实现 `agent.DocumentedTool`，提示词自动进系统提示。

**真机验收持续项**（agent 模式已上线，`llm.agent_mode: true` + 真实 api_key）：
- 长文档（50k+ 字）完整跑通——`llm.agent_max_iterations` 默认 32 是否充足，不同模型对续读提示的跟随度
- write_work_items 回执被拒条目是否被稳定修正重交；ask_human 的提问频率是否克制（滥问 = 指令头/guidelines 需调）
- DeepSeek 等推理模型的 reasoning_content 以 thinking 相位展示；工具调用期间前端有明确进度感

**工具化演进方向**（扩展工具前先读 `tools/read.go` 与 `tools/write.go`）：
- 新工具要点：实现 `agent.DocumentedTool`（提示词自动进系统提示）；参数 Schema 保持极简且**内嵌真实限制常量**（描述与行为同源）；Output 优先纯文本（省 token），结构化回执才用紧凑 JSON；Details 返回人读摘要（前端工具轨迹）；所有截断附「怎么继续」的可行动提示（pi 模式）
- pi 的并行工具执行与 beforeToolCall 审批钩子暂不移植，需要时按 pi 源码 `agent-loop.ts` 对应段落补（见 §4.6 不移植清单）

#### Legacy 技术债清单

> 元数据模块专项债（提示词措辞模板化 B4 / 查重策略泛化 B3 / 启用在途检查等）已单独立账：[DEBT.md](./DEBT.md)——本表只列平台级通用债，两处修一销一。


| 项 | 影响 | 处置建议 |
|----|------|---------|
| 单发模式宽松恢复链单测偏薄 | agent 主路径已覆盖（全链路/降级/重放/问人往返），classic 的 lenient 恢复分支不全 | 补 classic 路径用例 |
| repository 层无测试 | schema 回归风险 | testcontainers-go 或对本机 docker PG 跑薄集成测试 |
| 查重阈值 0.75 固定 | 新需求被语义层误判「疑似重复」（实测 0.79 误报） | 按数据集类型/场景可配，或加「确认不是重复」负反馈 |
| 向量固定 1024 维 | 换模型要重建库 | 文档已写死流程；如需多维度考虑按维度分表 |
| refine 微调未做 | 分析结果只能重来 | Legacy 曾计划在 Bug 链路中顺手完成；V2 应改为 Executor checkpoint 上的明确会话续跑能力 |
| 后端无热重载 | 开发改代码要重启 | 可引入 air，非必须 |
| token 增量不落库 | 分析中途重连后思考/正文双区从空开始（工具轨迹从步骤 data 回放，结果以明细为准） | 刻意取舍：防会话膨胀；如确需重放全文再按轮次落库 |
| 上传文件无清理 | 失败/暂停任务的 upload_dir 文件残留 | 终态清理 + 启动扫描兜底 |
| classic 模式续跑重放 | 单发模式暂停后恢复会重放流式调用（同 prompt 重新生成，幂等但耗 token） | 暂停多在 agent 模式（检查点续跑不重放已确认轮次） |

#### Legacy 多客户端上线补全

单实例多客户端当前即支持（架构证据见 PRODUCT §4 决策五）；**上线前**按此顺序补，每项独立可交付：

1. **认证授权**（硬门槛）：登录 + 会话（cookie/session），任务 / 数据集 / 触发端点全部校验；无认证时任何能访问端口的人可看全部任务与数据集、触发所有操作
2. **LLM 并发限流**（稳定性）：信号量限制并发 LLM / embedding 调用 + 排队——多客户端同时触发多任务会并发打上游，上游限流表现为「卡住 / 超时」（已实测撞到过一次慢调用贴近超时上限）；顺带把 `llm.timeout_ms`（当前 300s）在真实网络下评估是否上调
3. **草稿冲突**（质量）：`ReplaceTaskItems` 整批替换无版本控制，两个客户端同时编辑同一任务草稿后写覆盖前写——加版本号乐观锁，冲突显式报错
4. **多实例扩展**（远期，不在当前范围）：Broker 进程内扇出 / running 登记表 / 上传文件本地盘都是单实例绑定——分别换 Redis pub-sub / 分布式锁 / 对象存储

## 6. 运维备忘

- **DB**：`docker compose up -d`；Compose 卷名为 `reqflow_pgdata`；连接 `postgres://reqflow:reqflow@127.0.0.1:5432/reqflow`；本机无 `psql`，用 `docker compose exec -T postgres psql -U reqflow -d reqflow -c "…"`。
- **迁移执行**：启动时自动跑（`database.auto_migrate: true`），SQL 编译进二进制，`schema_migrations` 记录已执行版本。新文件放 `internal/infra/database/migrations/NNNN_*.up.sql`，必须同时提供 `.down.sql`。
- **V2 开发期重置**：当已在本地执行的 `0012_pipeline_v2_foundation` 发生破坏性修改时，先停服，然后删除**明确的 ReqFlow 开发数据库/数据卷**并重启全量迁移。不要只删 `schema_migrations` 某行后在半旧 Schema 上重放；当前无生产数据，不做回填和兼容。
- **迁移压平**：V2 API、Worker 和首条清洗链路切流后，删除 Legacy 表及应用代码，将最终数据库形状重写为新 `0001_v2_init`；不向未上线的历史迁移支付兼容成本。
- **索引现状**：Legacy `dataset_items.embedding` 和 PostgreSQL FTS 仍服务当前页面；V2 Retrieval 只有表和领域形状，OpenSearch 尚未加入 Compose，不存在可运维的混合索引。换 embedding 模型/维度前先补 Retrieval 重建作业和 Snapshot 双写切换。
- **SSE 客户端**：每个 Legacy 任务详情页持有一条长连接；token 事件已节流（~7 帧/秒）。V2 要继续使用落库快照 + 通知的恢复模式。
- **日志**：stdout（text/json 可配）；启动告警集中在头几行；SSE 断开/重连可通过 HTTP events 请求的 `cost_ms` 观察。
- **发布**：项目尚未上线。切流前只验证 `make build` 产物和干净数据库启动；不需要设计 Legacy 滚动升级或数据迁移通道。

## 7. 联系与上游

- 产品定义 / 方向性决策 / 路线图：`docs/PRODUCT.md` + 本文件
- pi 源码参考副本：`/Users/weighingzhang/demo/pi`（改 LLM 层/loop/工具时对照；消息模型、双协议适配器、agent loop 与过程工具的移植映射见 §4.6）
- MinerU API：mineru.net 精准解析 v4（限制单文件 ≤200MB、≤200 页）
