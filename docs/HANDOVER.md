# ReqFlow 交接文档

> 面向下一个接手开发的同学。本文只回答四件事：**项目现在是什么、怎么跑起来、改代码必须知道的上下文、接手后干什么**。
> 产品定位、方向性决策与「重复人工任务 → AI 驱动 Task」的抽离范式见 [PRODUCT.md](./PRODUCT.md)；技术实现细节以本文件为准。

> **2026-09-02 交付状态（v1.0.0）**：阶段 A~F 全部完成后已收敛为 v1 稳定版交付。产品主线固化为两条内置流程——**数据清洗入库**与**建立检索索引**（索引在数据集上隐式发起）；自由流程编排界面已隐藏（代码保留）。数字大脑为只读顾问，内置三个 Skill。新增**开发契约文档 [CONTRACTS.md](./CONTRACTS.md)**（架构路线、两条流程的实现契约、新流程/新工具开发规范），改代码前先读它。旧的《DATA_PIPELINE_V2_PLAN.md》已删除（全部落地，git 历史可溯）。

---

## 1. 项目是什么（30 秒版）

- **目标形态**：把非标准产品文档清洗为基础 Dataset，再增量派生查询 Dataset 和 BM25 + Vector Retrieval Snapshot，最后由 Bug 分析、规格书生成、知识图谱等业务 Task 消费。
- **V2 已落地**：不可变 JSON Schema、Dataset Batch 追加、`commit_seq`、provenance/identity、TaskDefinition DAG、定义快照和资源端口；Stage B 后端已补齐 Executor Registry、Step 输出绑定、Scheduler、PostgreSQL Worker/lease/checkpoint/retry、任意位置 Human Gate、两阶段暂停和任务输出固化。
- **V2 API 已闭环**：`/api/v2` 已开放 Asset/Schema/Profile/Dataset/Batch、TaskDefinition/Task、Retrieval/Analysis/Artifact/Catalog/Archive；Worker 已注册清洗、增量派生、检索构建、通用分析、分析发布、制品渲染和图谱构建 Executor。
- **Stage C 后端已闭环**：内容寻址解析、Schema 驱动抽取、确定性转换/校验、不可变人工审核资源、幂等原子发布、完整 provenance 和 attempt fencing 已打通真实 HTTP → PostgreSQL → Worker 集成测试。
- **输入绑定已收口**：Task API 可用 `resource_id` 或 Dataset `resource_alias` 定位输入；应用层验证资源存在性，Alias 只在创建时解析一次，`dataset_boundary` 自动固化 `through_seq`，Retrieval Snapshot 自动固化 `source_seq`。
- **V2 前端已完成切换**：一级路由为数字大脑 `/agent`（默认首页）、数据管理 `/datasets`、任务管理 `/tasks`、归档管理 `/archives`；流程设计器 `/definitions*` 在 v1 隐藏（路由重定向到 /tasks，页面文件保留但不可达）。数据集、检索、制品和归档均只使用 V2 API。
- **流程与任务边界已收口**：模板仅生成可编辑 Definition 起点；发布流程只创建 `TaskDefinition`，不创建 Task；Task 只能从 active Definition 派生，创建时冻结 Definition Snapshot、Resource Binding/Boundary 和 StepRun。
- **Stage D 已闭环**：`data.query_derive` 按 Base Dataset Boundary + PipelineCursor 增量读取，确定性展开语义单元并生成标准 Query Item；Query Batch、Dataset 位点、Outbox 和 Cursor 在同一事务提交，失败不推进 Cursor。
- **Stage E 已闭环**：不可变 RetrievalProfile、`retrieval.build` 状态机、OpenSearch BM25、pgvector Chunk、查询级加权 RRF/召回数、SiliconFlow rerank 和 KnowledgeScope Agent 工具已经落地。
- **检索分数语义（2026-09-01 起）**：RRF 融合分保留原始量纲（≈1/(k+rank)，衡量双路共识；不归一化——历史版本除以"理论满分"会把分数压进 0.5..1.0 窄带，阈值形同虚设），也不参与阈值过滤；**用户可见的最终分数 = rerank 分**（bge-reranker 校准相关性），`score_threshold` 只过滤 rerank 后的最终分；reranker 未配置时优雅降级为纯融合（策略回显 `rerank_enabled=false`，阈值自动归零）。前端默认 rerank 开、阈值 0.3（实测相关命中 ≈0.4..0.9、噪声 ≤0.06；0.5 会误杀"预装条件"类中等相关查询）。
- **Stage F 已闭环**：不可变 AnalysisProfile/AnalysisResult/Artifact，通用分析、资源审核、分析发布、制品渲染和图谱 Manifest Executor 已落地；五种无代码模板由 Profile + TaskDefinition 组合，不存在业务专用 Runner。
- **可复用底座**：LLM Provider、Agent Loop、工具调用、会话序列化、`ask_human`、SSE persist-then-publish 和前端重连机制继续复用，但要通过 V2 Executor/Orchestrator 接入。
- **下一步**：上线前门禁（认证授权、容量/故障恢复测试、Legacy 删除与迁移压平）。新增流程/新工具的开发规范见 [CONTRACTS.md](./CONTRACTS.md)。
- **仓库**：`/Users/xxxg/demo/ReqFlow`（主分支由 AI 开发占用）；v1 交付分支 `release/v1.0.0`（worktree `/Users/xxxg/demo/ReqFlow-v1.0.0`），8080 常驻服务跑该分支二进制，PID 记录在 worktree `logs/reqflow-v1.0.0.pid`。操作前始终先看 `git status --short`，不要回退不属于当前任务的修改。

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
| `docs/CONTRACTS.md` | **v1 开发契约与规范（新代码必读）**：架构/技术路线、两条固定流程的实现契约、新流程与新工具开发清单、种子契约 |
| `internal/app/orchestrator/builtin_definitions.go` | 两条固定流程种子（data_clean_import / retrieval_index），按 (workspace,key) 幂等 |
| `internal/app/platformagent/tools.go` | 数字大脑 4 个只读工具（platform_guide / list_workflows / list_tasks / query_data）；query_data 默认 rerank+阈值 0.3+权重 0.4/0.6 |
| `internal/app/platformagent/builtin_skills.go` | 三个内置 Skill 种子（platform-guide / query-analysis / create-skill），启动 upsert 刷新 |
| `cmd/reqflow/starter_kit.go` | 交付种子：DH1 示例知识库四件套（结构/抽取规则/索引规则/空数据集），按名称幂等 |
| `web/src/pages/v2/DatasetIndexDrawer.tsx` | 数据集索引抽屉：建索引任务、快照列表/筛选/落后提醒/删除；内嵌规则预览 |
| `web/src/pages/v2/{RetrievalProfileDetailModal,ExtractionProfileDetailModal,SchemaDetailModal}.tsx` | 规则/结构的预览+删除弹窗（后端 409 防护文案透出） |
| `web/src/pages/v2/datasetPurpose.ts` | 用途枚举与 `DATASET_PURPOSE_FIXED='query'`（v1 焊死为搜索与问答） |
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
| `internal/app/agent/{loop,run}.go` | 所有流程 Agent 共用的循环、显式终止、可恢复 RunState、用量统计、trace/checkpoint 节流和通用 `agent_runs` 协议 |
| `internal/app/extraction/` | Profile 用例、稳定分块、抽取领域工具/状态、原文证据校验和 `document.extract` Executor |
| `internal/app/analysis/` | KnowledgeScope 工具、`submit_analysis_result` Schema 校验完成工具、分析领域状态和 `knowledge.analyze` Executor |
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
| `internal/infra/database/migrations/0014_extraction_drafts.*.sql` | `document.extract` 输出 Manifest、稳定 ExtractionUnit 与可追溯 RecordDraft |
| `internal/infra/database/migrations/0015_transform_validation_manifests.*.sql` | `data.transform`/`data.validate` 不可变 Manifest、记录 Diff、问题和分类结果 |
| `internal/infra/database/migrations/0016_approved_record_sets.*.sql` | 不可变 ApprovedRecordSet、全量审核决定、最终字段与 provenance |
| `internal/infra/repository/pipeline_integration_test.go` | PostgreSQL 真机验证 Batch、Outbox、Task 快照和资源绑定 |
| `web/src/pages/v2/V2DefinitionNew.tsx` | （v1 隐藏）独立流程编排器：空白或模板起点、类型化数据连接、Executor 专用配置、Definition 输出与发布 |
| `web/src/pages/v2/workflowBlocks.ts` | 前端可编排 Executor 目录、输入输出资源类型、默认配置与业务说明；必须与后端 Registry 合同同步 |
| `web/src/pages/v2/V2Definitions.tsx` | （v1 隐藏）流程定义目录；展示 active Definition 并提供“创建任务”入口 |
| `web/src/pages/v2/NoCodeTaskNew.tsx` | 独立任务派生页：选择 active Definition，按端口绑定本次资源，仅创建或创建并运行 |
| `web/src/pages/v2/V2Tasks.tsx` | V2 任务运行目录；展示来源流程、状态和运行操作 |
| `web/src/pages/v2/SchemaFieldEditor.tsx` | Schema 可视化字段编辑器；面向非技术用户，不把 JSON 作为主界面 |
| `web/src/pages/v2/V2Metadata.tsx` | V2 Schema/Profile 目录与可视化创建入口 |

### 3.3 元数据系统不变量

旧任务运行时已删除；以下约束仅描述仍在使用的元数据管理服务。

> 原《元数据模块设计》（docs/METADATA.md）与《分波执行计划》（docs/METADATA_PLAN.md）已于 2026-08 合并删除，长效决策全部并入本节；历史代码注释中的「METADATA §N」编号指该文档，git 历史可溯。开发范式（定义即数据 / 半元数据驱动）见 PRODUCT §4 决策二。

- **分层真相源（seed → override → effective 三态）**：`app/registry.go taskTypeDefinitions()` 是 code seed（随二进制发布的出厂默认）；`metadata_registry` 表是 DB 覆盖——每 `(kind,key)` 取最大 version 行，该行 enabled 才生效；运行时读 `TaskTypes()/TaskTypeOf()/effectiveSchemaOf()` 的合并视图。**红线：元数据永远进程内供给，绝不经 HTTP 取**。写路径与加载器同进程收口：每次写后 `Reload` 整体刷新缓存，单测钉住「写后立即读」防失效遗漏。
- **合并层结构**：`metadataOverrides` 四张 map——schemas（按数据集类型）/ profiles、workflows（按任务类型）/ extDefs（向导扩展类型聚合定义）。map 只整体替换、绝不原地修改（读侧持旧引用即持旧快照，无竞态）；测试结束必须 `setMetadataOverrides(nil,nil,nil,nil)` 清进程级全局态。
- **锚行双语义（M4 沉淀）**：kind=workflow 行 payload=`{dataset_type, workflow}`，兼作向导注册类型的「锚行」。对 seed 类型 `enabled=false` 表示覆盖关闭（回退 seed）；对向导扩展类型它是**发布开关**——disabled = 整型草稿，装载器跳过（运行时不可见、建任务被拒）。同一列两种语义，改动前先分清身份（`customTaskType()` 是 source 徽标/回退按钮/启停权限的判定枢纽）。
- **数据集类型所有权不变量**：一个数据集类型只能被一个任务类型占用；向导注册必须新建 ds_type 且不得与任何既有定义冲突。ds_type 是任务间衔接的身份键（筛选 SQL、向量集合、item_key 都派生于它），共写一个结果集会打穿闭环——此约束是 M4 现场定案的产品级红线。**M5 补注**：该不变量收敛于**模板层**（模板与任务类型的一一对应）；实例层的绑定不受类型约束——任务创建即绑定任意数据集（`Create(ctx, typ, title, datasetID)`），字段异构由数据集自身 schema 承接，写入门的类型匹配校验已删除。
- **V2 字段合同**：流程节点只引用不可变 `DatasetSchemaDefinition`；抽取合同通过 `ExtractionProfile.TargetSchemaID` 固化，分析合同通过 `AnalysisProfile.OutputSchema` 固化。不存在运行时覆盖 Schema 或按旧任务类型回退模板的分支。
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

### 3.4 V2 不变量（新增代码必须遵守）

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
| `record_draft_sets/extraction_units/record_drafts` | `document.extract` 的 Manifest、稳定模型调用单元和带 Asset/Block/quote 来源的候选记录 |
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

### 4.3 API 速查

V2 API 已挂在 `/api/v2`：Asset/Schema/Profile/Dataset/Batch、TaskDefinition/Task、Retrieval/Analysis/Artifact/Catalog/Archive 等产品能力均已开放。Handler 只调用对应 V2 app service，不回调 Legacy TaskManager，也不双写；完整合同以路由实现（`internal/infra/httpgin/server.go`）和 [CONTRACTS.md](./CONTRACTS.md) 为准。

v1 新增要点：`DELETE /api/v2/schemas|extraction-profiles|retrieval-profiles|retrieval-snapshots/:id`（引用防护 409，见 CONTRACTS §2.4）；创建 Task 支持 `step_configs`（运行期规则注入，allowlist 限 document.extract / retrieval.build）；归档查询仍返回 tasks+datasets，但前端仅消费数据集。

旧 `/api/tasks` 端点已删除；任务执行只通过 `/api/v2`。

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

**v1.0.0 收敛期新增（2026-09-02）**：
- **模板/定义前端合同必须成对改**：发布步骤缺 `dataset_boundary` 输出而前端 workflowBlocks 要求它 → fork 模板报「不匹配 Executor 端口合同」。改流程合同先跑 workflowBlocks 同步检查。
- **GORM 列名≠结构字段名**：retrieval_profiles 的 jsonb 列是 `lexical_config/vector_config/fusion_config`，裸 SQL 按 `lexical` 写会 500。查库核对 `\d 表名` 再动手。
- **`CreateExecutionInput` 的 JSONB 归一化**：任务快照落库前会重新序列化（键序/空白规范）——单测断言快照 JSON 字面量时必须比对 canonical 形态，不能比对原始输入字符串。
- **数字大脑工具错误要可自愈**：query_data 在「有 query 无 dataset」时报错必须携带 `name(ID)` 候选清单（唯一候选直接自动锁定）——否则模型会空参死循环重试（上线初期真实事故）。
- **antd 5 Select/Modal 自动化测试**：下拉 option 渲染在 portal，role locator 常报 hidden；用 `evaluate` 内 `dispatchEvent(mousedown/mouseup)+click` 点 `.ant-select-item-option` 最稳。Popconfirm 确认按钮同理。
- **Space.Compact 内嵌 `Form.Item noStyle` + `Select style={{flex:1}}`**：多按钮紧凑行的可靠写法；不要用 `calc(100% - Npx)` 手算宽度（按钮数量一变就破）。
- **种子与共享库**：开发/交付共用一个 PG 实例时，种子必须按业务键幂等（同名跳过），否则每次重启翻倍；v1 全部种子已按此实现并有冷启动+二次启动验证。
- **删除防护统一 409**：哨兵错误（`ErrProfileInUse`/`ErrSchemaInUse`）在 app 层定义，handler `errors.Is` 映射；错误文案必须给出「引用了多少、去哪解除」。

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

## 5. 历史里程碑与当前现场

### 5.1 历史里程碑（全部 ✅，细节看 git 历史与各阶段验收记录）

1. **Stage B**：Executor Registry、Step 输出绑定、Scheduler、PostgreSQL Worker/lease/checkpoint/retry、任意位置 Human Gate、两阶段暂停。
2. **Stage B+ API**：`/api/v2` 基础资源与任务读模型；SSE 直播数据库快照 diff，Broker 只做通知。
3. **Stage C**：数据清洗纵向切片——`ParsedDocumentSet → RecordDraftSet → TransformedRecordSet → ValidationResultSet → ApprovedRecordSet → DatasetBatch` 全部一等资源，真实浏览器验收通过。
4. **Stage D**：`data.query_derive` 增量派生 + PipelineCursor CAS 推进。
5. **Stage E**：OpenSearch BM25 + pgvector + 加权 RRF + rerank；KnowledgeScope Agent 工具。
6. **Stage F**：五种无代码模板、自由编排、流程/任务分离、Schema/Profile 可视化表单。

### 5.2 v1.0.0 收敛交付（2026-09-02，当前现场）

- **流程收敛**：流程设计器/目录隐藏，固定两条内置流程（`data_clean_import` 六步、`retrieval_index` 单步）由启动种子创建；抽取/索引规则不在定义里写死，经 `step_configs` 在任务创建时注入（运行级校验，allowlist 见 `definition_service.go`）。
- **数据集为中心**：创建数据集统一用途 `query`；字段结构/抽取规则/索引规则支持预览与带防护删除；数据集「索引」抽屉提供建索引、快照筛选、落后提醒与删除。
- **数字大脑只读化**：4 个只读工具（platform_guide/list_workflows/list_tasks/query_data），无任何写工具；三个内置 Skill（platform-guide/query-analysis/create-skill）启动 upsert；query_data 携带推荐检索参数（rerank 默认开、阈值 0.3、权重 0.4/0.6、top 8）。
- **归档收敛**：归档页仅数据集。
- **交付链路**：`make start` 一键启动；种子=两流程+DH1 示例知识库四件套+三 Skill，全部幂等（冷启动与二次启动已实测）；验收环境用临时库（`REQFLOW_DATABASE_DSN`/`REQFLOW_SERVER_PORT` 覆盖）做过完整业务流验收。

### 5.3 常见误区（持续有效）

- 不假设工作区干净；每次接手先看 `git status --short`，不要用 reset/checkout 回退他人修改。主分支 `main` 由另一 AI 开发占用，v1 工作只在 `release/v1.0.0` worktree 进行。
- 迁移压平（删除 Legacy 表后重写 `0001`）是上线前动作，不要围绕旧 `datasets.schema/schema_version`、`task_steps` 或 `metadata_registry` 设计新外键和新功能。
- `DatasetService.CommitBatch` 当前逐条 INSERT，正确性优先；出现瓶颈再改参数化批量 SQL，必须保持同一事务与 seq 顺序。
- `resource_id` 是通用 UUID，无数据库级多态外键；资源类型和存在性由 app service 校验。
- 向量维度 `vector(1024)` 是硬约束，换 embedding 模型/维度必须设计重建作业与 Snapshot 切换。
- 全仓 `make test` 应保持全绿；改前端合同后 `pnpm build`（含 tsc）必须过。

## 6. 运维备忘

- **DB**：`docker compose up -d`；Compose 卷名为 `reqflow_pgdata`；连接 `postgres://reqflow:reqflow@127.0.0.1:5432/reqflow`；本机无 `psql`，用 `docker compose exec -T postgres psql -U reqflow -d reqflow -c "…"`。
- **迁移执行**：启动时自动跑（`database.auto_migrate: true`），SQL 编译进二进制，`schema_migrations` 记录已执行版本。新文件放 `internal/infra/database/migrations/NNNN_*.up.sql`，必须同时提供 `.down.sql`。
- **V2 开发期重置**：当已在本地执行的 `0012_pipeline_v2_foundation` 发生破坏性修改时，先停服，然后删除**明确的 ReqFlow 开发数据库/数据卷**并重启全量迁移。不要只删 `schema_migrations` 某行后在半旧 Schema 上重放；当前无生产数据，不做回填和兼容。
- **迁移压平**：V2 API、Worker 和首条清洗链路切流后，删除 Legacy 表及应用代码，将最终数据库形状重写为新 `0001_v2_init`；不向未上线的历史迁移支付兼容成本。
- **索引现状**：OpenSearch 已入 Compose（`reqflow-opensearch`，单节点免认证，仅限内网/本机）；物理 BM25 索引按（数据集, 规则）在首次索引任务时惰性创建。换 embedding 模型/维度前先补 Retrieval 重建作业和 Snapshot 双写切换（`vector(1024)` 硬约束）。
- **一键启动与种子**：`make start`（依赖容器 → 健康等待 → 构建 → 运行）；种子清单与幂等契约见 [CONTRACTS.md §5](./CONTRACTS.md)。验收/演示用临时库：`REQFLOW_DATABASE_DSN` + `REQFLOW_SERVER_PORT` 环境变量覆盖，用完即删。
- **SSE 客户端**：任务详情通过落库 checkpoint 快照 + 通知恢复；`agent_runs` 是可重连的事实视图，流式 delta 只负责缩短可见延迟。
- **日志**：stdout（text/json 可配）；启动告警集中在头几行；SSE 断开/重连可通过 HTTP events 请求的 `cost_ms` 观察。
- **发布**：项目尚未上线。切流前只验证 `make build` 产物和干净数据库启动；不需要设计 Legacy 滚动升级或数据迁移通道。

## 7. 联系与上游

- 产品定义 / 方向性决策 / 路线图：`docs/PRODUCT.md` + 本文件
- pi 源码参考副本：`/Users/weighingzhang/demo/pi`（改 LLM 层/loop/工具时对照；消息模型、双协议适配器、agent loop 与过程工具的移植映射见 §4.6）
- MinerU API：mineru.net 精准解析 v4（限制单文件 ≤200MB、≤200 页）
