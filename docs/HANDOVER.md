# ReqFlow 交接文档

> 本文只维护当前事实、长期不变量、运行方式和正在执行的工作。已经完成的阶段记录由 Git 历史保存，不在交接文档中维护。

当前权威设计是 [线性工作流重建落地计划](./WORKFLOW_REBUILD_PLAN.md)。`PRODUCT.md` 仍包含旧 `TaskDefinition/DAG/Profile` 口径，完成新系统切换前不得将其作为工作流实现依据。

## 1. 当前状态

ReqFlow 正在把旧的 `TaskDefinition + StepDefinition + depends_on + Profile` 编排整体替换为新的线性工作流系统。

已经落地：

- 新领域包 `internal/domain/workflow`：Capability、显式 Connection、WorkflowDraft、内联 RuleBundle、不可变 WorkflowRevision 和 DesignSession；
- 单链拓扑、端口资源类型、必填输入、规则证据、高风险人工确认和发布验收校验；
- 连接推导执行顺序和自包含 Revision 内容哈希；
- Agent 可选、无模型直接手动编辑、模型故障切换手动、人工问题挂起/恢复和 Command Proposal 生命周期；
- 新应用包 `internal/app/workflow`：完整 Draft Command、乐观并发、Preview/Acceptance/Publication、DesignSession；
- 新 Workflow API 和纵向编辑器、运行目录与运行详情页；
- 类型化模型故障、Provider fallback、熔断和 `needs_human`；
- WorkflowRun/NodeRun 线性运行时、lease/checkpoint、attempt fencing、重试、暂停恢复和人工完成协议。

尚未完成：

- 确定性样本画像、规则差异视图和基于真实 Capability 的 dry-run；
- 基于画像、样本和证据的完整 Rule Synthesis 工具集与 trace UI；
- 将业务资源 Manifest 的生产者从旧 StepRun 统一切换到 NodeRun，并启用自动 Capability Executor；
- 新入口切换以及旧 Definition/Profile/Orchestrator/API/页面/表的整体删除。

旧 `TaskDefinition`、独立 Profile、DAG Orchestrator 和 `/api/v2` 相关代码目前仍承载原有界面和运行路径，只是待删除实现，不是新系统依赖，也不得继续扩展。新旧之间不建立适配器、双写、导入或兼容层。

项目尚未上线，没有需要迁移的生产数据。切换时允许重建数据库基线。

## 2. 启动与验证

在仓库根目录执行：

```bash
make setup
docker compose up -d
make dev
```

前端另开终端：

```bash
make frontend
```

构建发布产物：

```bash
make build
./bin/reqflow
```

数据库是启动必需依赖。LLM、Embedding、Rerank、OpenSearch 和 MinerU 未配置时只影响对应能力，不应阻止服务启动。

当前配置为空时，旧模型 Executor 会在调用阶段失败；新系统的目标是让设计、预览、发布和每个模型节点都能转入人工完成。这一目标尚未全部接入运行时。

提交前至少执行：

```bash
make test
cd web && pnpm exec tsc --noEmit
```

`make test` 包含 `go vet ./...`、`go test ./...`、架构围栏和密钥扫描。涉及数据库事务、迁移或 Worker 时，还要运行对应 PostgreSQL integration tests。

## 3. 架构边界

四层依赖只允许向内：

```text
cmd/reqflow      组装具体实现
internal/infra   HTTP、数据库、仓储和第三方适配器
internal/app     应用用例
internal/port    出站接口
internal/domain  领域模型和纯逻辑
```

强制规则：

- `internal/domain` 不依赖任何内部包或第三方包；
- `internal/app` 只能依赖 `internal/domain` 和 `internal/port`；
- `internal/port` 只能依赖 `internal/domain`；
- `internal/infra/httpgin` 只调用应用层用例，不直接访问仓储或领域实现；
- `internal/infra/repository`、`llm`、`embedding`、`parser` 和 `blobstore` 不依赖应用层；
- 依赖白名单由 `scripts/arch-check.sh` 强制执行。

### 3.1 当前开发入口

| 位置 | 职责 |
|---|---|
| `docs/WORKFLOW_REBUILD_PLAN.md` | 新工作流系统的唯一设计与阶段计划 |
| `internal/domain/workflow/` | 新 Capability、Draft、Connection、RuleBundle、Revision、DesignSession、WorkflowRun 和 NodeRun |
| `internal/app/workflow/` | 新 Capability Catalog、Draft Command、DesignSession 和线性运行时 |
| `internal/app/agent/` | 可复用 Agent Loop、RunState、trace、checkpoint 和工具执行内核 |
| `internal/port/llm.go` | Provider 无关的消息、工具和 LLMClient 合同 |
| `internal/app/pipeline/` | 可复用的数据清洗、校验、审核和 Batch 发布能力 |
| `internal/app/retrieval/` | 可复用的 Retrieval Snapshot、混合检索和知识工具 |
| `internal/app/analysis/` | 待改造为 RuleBundle OutputContract 驱动的结构化分析能力 |
| `internal/infra/repository/` | 数据资源、WorkflowRun/NodeRun、lease/checkpoint、attempt fencing 和事务持久化实现 |
| `internal/infra/database/migrations/` | 当前数据库基线；最终切换时按新模型重建 |

### 3.2 待删除面

以下内容只用于识别切割范围，不得成为新代码依赖：

- `internal/domain/model/task_definition.go` 和 `internal/domain/logic/task_definition.go`；
- `$task.*`、`$step.*`、`depends_on` 和通用 DAG ready-set；
- `TaskDefinition/StepDefinition` 仓储、API 和数据库表；
- `ExtractionProfile/RetrievalProfile/AnalysisProfile` 的独立创建、引用和数据库表；
- `internal/app/platformagent` 中创建、查询旧 Workflow/Task 的业务工具；
- `web/src/pages/v2/workflowBlocks.ts` 中重复维护的 Executor 合同；
- `web/src/pages/v2/taskTemplates.ts` 中写死的完整步骤 JSON；
- 旧 Definition 创建、详情、任务派生和 Profile 管理页面；
- 只服务上述范式的测试、文档和迁移定义。

旧 Orchestrator 中的 lease、checkpoint、fencing、幂等输出和数据库事实源机制可以抽取复用，但不得把旧 Definition 格式带入新运行时。

## 4. 新系统长期不变量

### 4.1 Workflow 与 Capability

- 首期节点间主连接必须形成一条覆盖全部节点的单链；不支持分支、汇聚、循环、条件或跨级引用。
- 节点只保存 Capability 版本引用和内联配置；Connection 是执行顺序和数据流的唯一事实源。
- Capability 的端口、资源类型、配置 Schema、规则要求、副作用、LLM 依赖和人工完成能力只在后端注册一次。
- 前端、Agent、发布校验和运行时必须读取同一 Capability 合同。
- 每个 Capability 必须且只能有一个主输入和主输出；侧输入只能来自流程输入。
- 每个成功节点都必须产生按 `node_id + port` 定位、可查看和可复用的一等资源。
- 插入、删除、替换和追加必须以 Draft Command 原子执行，禁止先改节点数组再尝试补连接。
- 发布产生不可变、自包含的 WorkflowRevision；运行不得读取可变草稿或外部 Profile。

### 4.2 RuleBundle 与规则推断

- DataContract、ExtractionSpec、SearchSpec 和 OutputContract 随流程内联，不要求业务用户先创建或命名独立 Profile。
- 规则候选先来自确定性样本画像；Agent 只在可用时补充语义判断和解释。
- 自动观察或推断的决策必须记录值、来源、置信度、理由和可定位证据。
- 记录粒度、唯一键和业务硬约束属于高风险决策，发布前必须记录确认人和确认时间。
- 用户可以编辑所有推断结果；Agent 不能直接修改已接受决策或发布 Revision。
- 发布前至少有一个端到端验收用例，并且全部用例最近一次执行通过。
- 规则变更必须显示对字段、节点、样本输出、搜索行为和既有验收用例的影响。

### 4.3 手动优先与 Agent 增强

- 没有 LLM 时必须能够创建、编辑、预览、校验、发布和执行流程。
- `RequiresLLM` 的 Capability 必须同时声明 `ManualCompletion`，否则不得注册。
- Agent 只能生成 Command Proposal；用户接受后仍由同一 Draft Command 层执行。
- 模型不可用、限流、鉴权失败、上下文溢出、输出非法或策略阻断必须是类型化故障。
- 设计态故障只废弃尚未接受的 Proposal，不丢失 Draft、画像、人工确认或验收用例。
- 运行态模型节点必须能进入 `awaiting_manual_completion`；人工结果与模型结果使用同一 Output Contract 校验。
- 需要业务判断时使用可恢复的 `HumanQuestion/needs_human` 协议，不把人工 Gate 伪装成模型工具成功。

### 4.4 数据与检索

- Workflow DataContract 是流程设计事实源；底层存储 Schema 必须由发布快照派生或固化，不再让业务用户额外维护一套同义合同。
- Dataset 是长期数据容器，正式写入通过不可变 DatasetBatch 完成。
- 当前写入语义为 APPEND；已提交 Batch/Item 不原地修改，`item_key` 冲突整批失败。
- `commit_seq` 在 Dataset 内连续递增，读取边界使用 `dataset_id + through_seq` 固化。
- Batch 提交事务必须同时完成 Item、Batch、Dataset 汇总、Outbox 和相关 Cursor 推进；LLM、索引和其他外部调用不得放进事务。
- Item 必须具有稳定 `item_key`、fingerprint、batch_id、commit_seq 和 provenance。
- Query Dataset 是数据，RetrievalSnapshot 是可重建派生资源。
- BM25 和 Vector 必须覆盖相同 `source_seq` 并通过计数校验后才能激活 Snapshot。
- 当前检索使用 OpenSearch BM25、pgvector 和应用层 RRF；rerank 不可用时允许降级为纯融合。

### 4.5 新运行时

- WorkflowRun 持有完整 Revision 快照和流程输入。
- NodeRun 只按线性主链推进，不再计算通用 DAG ready-set。
- NodeRun 是状态事实源；SSE/Broker 只负责通知，不能承担状态存储。
- Worker 继续使用数据库 lease、owner fencing、checkpoint、重试预算和过期恢复。
- 相同节点 attempt 的输出必须幂等；过期 Worker 不得覆盖新 owner 的状态或资源。
- 自动输出与人工输出必须经过同一端口资源类型和 Output Contract 校验。

## 5. 当前执行顺序

当前状态以 `docs/WORKFLOW_REBUILD_PLAN.md` 的复选框为准。

下一切片：

1. 补齐 Draft Command：替换、追加、规则编辑、乐观并发和撤销事件；
2. 提供新的 Capability、Draft、Validate 和 Preview API；
3. 建立无需 LLM 的纵向编辑和样本验收路径；
4. 抽取现有 Agent 内核，增加类型化故障、Provider fallback、`needs_human` 和规则推断工具；
5. 建立新的 WorkflowRun/NodeRun 线性运行时与人工完成协议；
6. 新入口通过真实端到端验收后，一次性删除旧代码、页面、表和迁移定义。

切割期间的约束：

- 不为旧 API、旧草稿、旧数据库记录或旧测试增加兼容代码；
- 不让新领域包 import 旧 `domain/model` 或 `app/orchestrator`；
- 不在旧前端编排器上继续堆功能；
- 不提前把半完成的新 Draft 编译成旧 TaskDefinition；
- 复用行为通过新接口重新接入，不复制旧模型。

## 6. 安全与运维

### 6.1 数据库和迁移

- PostgreSQL/pgvector 通过 `docker compose up -d` 启动；迁移在服务启动时自动执行。
- 当前项目未上线。新系统切换时重建单一初始 migration，不做旧表迁移、回填或双写。
- 破坏性重建只能针对明确的 ReqFlow 开发数据库或 Compose 数据卷；不得对宽泛目录、主目录或未确认数据库执行删除。
- 不要通过删除 `schema_migrations` 单行在半旧 Schema 上重放迁移。

### 6.2 密钥

- `config.yaml` 和 `config.*.yaml` 不得提交，示例文件除外；
- `make setup` 启用 `.githooks/pre-commit`；
- `make test` 会运行密钥扫描；
- 启动时 `config.CheckExampleLeak` 检查示例配置是否误填真实密钥；
- 日志只记录已配置的密钥名称，不记录密钥值；
- 发生泄漏时先轮换平台密钥，再清理 Git 历史。

### 6.3 仍有效的实现注意事项

- GORM `Create(&emptySlice)` 会失败，仓储层应在空集合时提前返回；
- UUID 主键由仓储层显式生成，不能把空字符串写入 PostgreSQL UUID 列；
- pgvector Raw SQL 参数使用 `pgvector.NewVector([]float32)`；
- OpenAI/Anthropic SSE scanner buffer 当前为 8 MB，避免大型工具参数截断；
- MinerU 预签名 PUT 上传裸字节，不附加 Content-Type；
- Tool Parameters 是 JSON Schema，注册和测试必须检查 `json.Valid`；
- 归一化中的全角空格处理位于 `internal/domain/logic/record_cleaning.go`；
- 不假设工作区干净，不使用 reset/checkout 覆盖不属于当前任务的改动。

## 7. Agent 内核来源

以下能力源自 pi 的 Go 化实现，是新 Rule Synthesis Agent 的复用基础：

| ReqFlow | 保留的能力 |
|---|---|
| `internal/port/llm.go` | 可序列化 Context、三角色消息、工具调用、StopReason 和流事件 |
| `internal/infra/llm/openai.go` | OpenAI 兼容流式协议和工具调用聚合 |
| `internal/infra/llm/anthropic.go` | Anthropic SSE、thinking signature 和 tool result 回放 |
| `internal/app/agent/loop.go` | 工具循环、截断保护、自纠错回执和显式终止 |
| `internal/app/agent/run.go` | RunState、trace、checkpoint、progress 和用量统计 |

当前只有一个动态 LLM 配置入口，还没有 Provider 健康路由和运行态人工完成；不要把 `Complete` 方法误认为 Provider fallback。

## 8. 交接检查

开始工作前：

1. 阅读本文件和 `docs/WORKFLOW_REBUILD_PLAN.md`；
2. 执行 `git status --short`，保留他人修改；
3. 查看计划复选框和新 workflow 包测试；
4. 修改领域边界前运行 `scripts/arch-check.sh`；
5. 完成切片后更新计划状态、本文件的当前状态以及对应测试。

外部能力参考：pi 源码、MinerU 精准解析 API、OpenSearch 和 pgvector 官方文档。具体本机路径和个人环境配置不属于仓库交接合同。

## 9. 继续开发前必须掌握的代码事实

### 9.1 新领域包的实际行为

建议按以下顺序阅读：

1. `internal/domain/workflow/model.go`
2. `internal/domain/workflow/catalog.go`
3. `internal/domain/workflow/validate.go`
4. `internal/domain/workflow/revision.go`
5. `internal/domain/workflow/design_session.go`
6. `internal/domain/workflow/workflow_test.go`
7. `internal/app/workflow/catalog.go`
8. `internal/app/workflow/draft_editor.go`
9. `internal/app/workflow/draft_editor_test.go`

关键行为：

- `WorkflowDraft.Nodes` 的数组顺序不代表执行顺序；`LinearOrder` 只读取 node output → node input 的主连接。
- `ValidateDraft` 允许尚未填写完整的业务内容，以 warning 返回；端点非法、类型冲突、重复 ID 等结构问题仍是 error。
- `ValidatePublish` 要求流程身份、所有必填端口、规则、人工确认和验收用例全部满足。
- `BuildRevision` 会再次执行 Publish 校验，解析节点顺序，固化完整 CapabilityDefinition 和默认配置，再计算内容哈希。
- `StaticCatalog` 强制每个 Capability 只有一个主输入和一个主输出；LLM Capability 没有 `ManualCompletion` 时注册失败。
- `InsertBetween` 只支持两个相邻节点的主连接，不支持流程头尾；失败不会修改传入 Draft。
- `RemoveAndBridge` 只支持有明确前驱和后继的中间节点；节点暴露了流程输出时必须先处理输出。
- DesignSession 的 manual/agent/awaiting_human 状态只存在于领域层，尚未落库或接入 HTTP/Agent Loop。

已知但尚未修复的设计缺口：

- AcceptanceCase 只有 `LastPassed`，还缺通过时的 Draft revision 和 Preview ID；接 API 前必须补齐。
- RuleExpression 仍是字符串；接用户输入前必须改为受控类型化 DSL，不能解释执行任意表达式。
- Capability ConfigSchema 当前只检查 JSON 合法性，没有执行真正的 Schema validation。
- 当前没有 Workflow Repository、Command Service、HTTP Handler、数据库表或新前端页面。
- 当前 `ToolNeedsHuman` 只是领域状态枚举，Agent Loop 尚不会识别并持久化挂起。
- 当前动态 LLM Client 每次读取一个激活配置，没有健康检查、备用 Provider 路由或故障分类。

### 9.2 常见校验错误的含义

| 错误族 | 代表 code | 处理位置 |
|---|---|---|
| 连接错误 | `connection_type_mismatch`、`connection_target_occupied` | 节点插入/连接前阻止 |
| 线性拓扑 | `linear_branch_forbidden`、`linear_merge_forbidden`、`linear_cycle_forbidden`、`linear_chain_incomplete` | Draft Command 和发布 |
| 必填端口 | `required_input_unconnected`、`workflow_input_unused`、`workflow_output_unconnected` | 编辑器节点卡和发布栏 |
| 规则合同 | `required_rule_section_missing`、`field_key_invalid`、`key_field_not_found` | 对应 RuleEditor |
| 推断证据 | `decision_evidence_required`、`decision_evidence_invalid` | 建议卡和证据抽屉 |
| 人工确认 | `high_risk_decision_unconfirmed`、`business_decision_required` | 发布前确认步骤 |
| 验收 | `acceptance_case_required`、`acceptance_case_not_passed` | 预览/验收面板 |

前端只能依赖 code 和 path 定位交互，不能解析 message 文案。

### 9.3 首条验收链

领域测试中的标准链是：

```text
assets
  → source.parse
  → document.extract
  → data.transform
  → data.validate (+ target dataset 侧输入)
  → human.review_records
  → data.publish
  → retrieval.build
```

交付输出为 `data.publish.batch` 和 `retrieval.build.snapshot`。该链同时覆盖 LLM 节点、人工节点、侧输入、副作用节点、交付输出、DataContract、ExtractionSpec 和 SearchSpec，应作为 Phase 2/3 的第一条 HTTP 与浏览器端到端用例。

## 10. 下一位开发者的具体起步顺序

### 第一步：补强领域模型

- 给 AcceptanceCase 增加 `last_passed_revision/last_preview_id`；
- 将 RuleExpression 替换为类型化 normalization/validation DSL；
- 增加 Append、Prepend、Replace、SetConfig 和 Rule Commands；
- 规则或拓扑变化后使验收结果和相关高风险确认失效；
- 先写领域测试，不接数据库。

### 第二步：建立 Command 和持久化边界

- 新建 `internal/app/workflow/command.go`、`service.go`；
- 新建 `internal/port/workflow_repo.go`；
- 新建 `internal/infra/repository/workflow_repo.go`；
- 使用 `command_id + expected_revision` 实现幂等和乐观并发；
- Draft 更新与 CommandEvent 必须在同一事务；
- PostgreSQL 集成测试必须覆盖两个并发写入只有一个成功。

### 第三步：只接最小新 API

- 在 `internal/app/workflow/api.go` 定义 HTTP DTO；
- 在 `internal/infra/httpgin/handler_workflow.go` 实现 Handler；
- 在 `httpgin.Services` 和 `cmd/reqflow/main.go` 注入新服务；
- 首先只挂 `/api/capabilities`、`/api/workflows`、`commands` 和 `validate`；
- 新 Handler 只调用应用服务，不直接调用 Repository 或 domain.Validate。

### 第四步：建立独立前端入口

- 新建 `web/src/api/workflows.ts` 和 `web/src/pages/workflows/`；
- 用纵向链实现读取、插入、删除、配置和 issues 定位；
- 不修改 `V2DefinitionNew.tsx`、`workflowBlocks.ts` 或 `taskTemplates.ts` 来承载新功能；
- 新页面稳定后再切换 `/workflows` 导航并删除旧 `/definitions` 页面。

### 第五步：完成手动闭环后再接 Agent

手动创建、规则编辑、预览、验收和发布在空 LLM 配置下通过之前，不开始 Rule Synthesis Agent。Agent 接入顺序是确定性画像 → 只读工具 → Proposal → 人工接受 → Command，不允许 Agent 获得 Repository。

更完整的命令、表结构、API、状态机和测试矩阵见 `WORKFLOW_REBUILD_PLAN.md` 第 11～25 节。

## 11. 现有实现的复用与拆除顺序

复用不是直接依赖旧模型。推荐顺序：

1. 为新 Capability 定义只接受 `ResolvedNode + RuleBundle + ResourceBinding` 的执行接口。
2. 从旧服务抽取纯处理逻辑，例如解析、受控清洗 DSL、校验、Batch 事务、检索和 Artifact 写入。
3. 先让新 Executor 通过新接口调用这些能力。
4. 为新 WorkflowRun 接入 lease/checkpoint/fencing。
5. 新运行路径通过故障测试后停止旧 Worker 和旧路由。
6. 删除 TaskDefinition/Profile/旧页面/旧 Agent 业务工具和相关表。
7. 最后重建单一 migration，并在干净数据库执行端到端验收。

特别注意的旧耦合：

- `CleaningService.Validate` 当前从 TransformedRecordSet 反查 ExtractionProfile 和 TargetSchema；新接口必须直接接收 Revision 中的 DataContract/规则快照。
- `data.publish` 当前使用 SourceStepRunID 做幂等和 fencing；新实现改用 NodeRunID + attempt。
- `retrieval.build` 当前读取 RetrievalProfileID；新实现读取 Revision.SearchSpec。
- `knowledge.analyze` 当前读取 AnalysisProfileID；新实现读取 Revision.OutputContract 和内联 instruction。
- 旧 Scheduler 使用 `depends_on` 和 ready-set；不要将其包装成线性调度器，直接实现 ordinal 推进。
- `platformagent` 当前仍暴露 `create_workflow/list_workflows/create_task/run_task` 等旧业务工具；新设计 Agent 只能提交 Command Proposal。

## 12. 调试和验收入口

领域快速验证：

```bash
go test ./internal/domain/workflow ./internal/app/workflow -count=1
```

全仓门禁：

```bash
make test
cd web && pnpm exec tsc --noEmit
```

增加 Repository 后的最低集成验证：

- 干净数据库自动迁移；
- 创建 Workflow@revision=0；
- 成功执行命令得到 revision=1；
- 相同 command_id 重放结果不变；
- 两个 expected_revision=1 的并发命令仅一个成功；
- 发布时 Draft 被并发修改必须返回 409；
- 删除所有 LLM 配置后，Draft/Validate/Preview/Publish 仍成功。

增加运行时后的最低故障验证：

- Worker 崩溃后从 checkpoint 恢复；
- lease 过期的旧 Worker 无法提交；
- LLM 401/429/5xx 分别映射到正确故障类型；
- Provider 全部失败后进入人工完成；
- 人工产物通过同一合同后下游继续执行；
- 每个 node_id + port 产物都能从运行详情打开。
