# ReqFlow 线性工作流重建落地计划

状态：执行中
目标：以新的线性工作流、内联规则合同和“手动完整、Agent 增强”的产品模型，整体替换现有 TaskDefinition/DAG/Profile 编排范式。

## 1. 重建原则

1. **不兼容旧定义**：不读取、不迁移、不编译到旧 `TaskDefinition`、`StepDefinition`、`depends_on`、`$task/$step` 或 Profile ID 引用。
2. **连接定义流程**：节点只声明能力与配置；连接是执行顺序和数据流的唯一事实源。
3. **首期只做线性**：节点间主连接只能形成一条链，不支持分支、汇聚、循环、条件或跨级数据引用。
4. **规则随流程内联**：字段合同、抽取规则、搜索规则和分析输出合同直接固化在 Workflow Revision 中，不要求业务人员先创建独立 Profile。
5. **手动能力完整**：没有 LLM 时仍可创建、配置、预览、校验、发布并执行流程。
6. **Agent 只做增强**：Agent 生成候选、解释证据和执行确定性工具，不拥有绕过校验或直接发布的权限。
7. **发布快照不可变**：运行只读取完整、自包含的 Workflow Revision；共享预设使用时复制快照，不形成隐式实时依赖。
8. **每步都有交付物**：节点成功必须产生符合 Output Contract 的一等资源，任务页按 `node_id + port` 查看、下载或复用。

## 2. 明确删除的旧范式

切换完成时删除以下能力，不提供兼容适配器：

- `TaskDefinition` / `StepDefinition` 及其旧 HTTP API；
- `depends_on` 和 `$task.*` / `$step.*` 引用语法；
- 前端 `workflowBlocks.ts` 的重复 Executor 合同；
- `taskTemplates.ts` 中硬编码的完整步骤 JSON；
- `EmbeddedResourceCreate` 的 Schema/Profile 嵌套创建模式；
- 节点配置中的 `extraction_profile_id`、`retrieval_profile_id`、`analysis_profile_id`；
- 旧 `create_workflow`、`list_workflows` 等 Platform Agent 业务工具；
- 旧草稿 localStorage 格式、复制逻辑和旧 Definition 详情/创建页；
- 旧任务定义、步骤运行和 Profile 表，以及只服务这些表的仓储接口。

可以复用的只有通用机制，例如数据库 lease、checkpoint、幂等写、Agent Context/Tool/Trace 和底层数据处理服务；新领域模型不得依赖旧定义格式。

## 3. 目标领域模型

### 3.1 CapabilityDefinition

节点能力的唯一合同来源，由后端注册并通过 API 提供给设计器：

- `kind + version`；
- 展示名称、说明、风险和副作用；
- 输入/输出端口、主链角色、资源类型、Schema 约束、必填性和基数；
- 配置 Schema、默认值和渐进式 UI 提示；
- 是否依赖 LLM、是否允许人工完成；
- 幂等、重试、恢复和交付物查看能力。

前端、Agent、发布校验和运行时使用同一份合同，不再复制端口定义。

### 3.2 WorkflowDraft

可编辑聚合：

- 基本信息；
- `nodes`：仅保存节点 ID、Capability 引用和内联配置；
- `connections`：流程输入/节点输出到节点输入/流程输出的显式连接；
- `revision`：用于乐观并发和命令撤销；
- 规则推断决策、证据和待确认问题；
- 当前预览结果与验收用例。

设计顺序从主连接推导，数组顺序不具备执行语义。

### 3.3 RuleBundle

工作流内联、可完整手工编辑：

- `DataContract`：记录粒度、唯一键、字段类型、必填性、枚举、业务说明和校验；
- `ExtractionSpec`：全局要求、字段指南、示例、归一化与校验规则；
- `SearchSpec`：精准、语义、筛选字段与高级检索参数；
- `OutputContract`：分析或人工节点的结构化产出合同；
- `DecisionLedger`：每项决策的来源、置信度、证据、确认人和确认时间。

### 3.4 WorkflowRevision

发布后的不可变、自包含快照：

- 完整节点能力版本和解析后的配置；
- 完整连接；
- 完整 RuleBundle；
- 验收用例及其通过结果；
- 内容哈希、发布时间和发布者。

### 3.5 WorkflowRun / NodeRun

新的线性运行模型：

- 一个 WorkflowRun 持有 Revision 快照和流程输入；
- NodeRun 按主链逐个就绪，不再计算通用 DAG ready-set；
- 输出通过 `node_id + port` 固化为一等资源；
- LLM 故障可进入 `awaiting_manual_completion`；
- 人工结果与 Agent 结果使用相同 Output Contract 校验并记录 provenance。

## 4. 规则推断与验收

规则推断不是发布入口，只产生带证据的候选：

1. 确定性解析和样本画像；
2. Agent 基于样本、业务目标、目标合同和示例查询生成候选；
3. 记录粒度、唯一键和业务硬约束必须人工确认；
4. 对代表性样本执行端到端预览；
5. 用户修正沉淀为验收用例；
6. 所有阻断项通过后才生成 Workflow Revision。

每项推断至少记录：值、来源、置信度、理由、证据引用和确认状态。

## 5. 手动降级与 Agent 复用

### 5.1 设计态

- LLM 未配置或不可用时，确定性画像、全部规则编辑器、预览、校验和发布保持可用；
- Agent 中断只丢弃未接受的 Proposal，不丢失 Draft、样本画像或用户确认；
- 用户可以随时在自动、引导和专家模式之间切换。

### 5.2 运行态

依赖模型的节点采用统一降级状态：

`running -> retry_wait -> provider_fallback | awaiting_manual_completion -> succeeded`

- 短暂故障按预算重试；
- 策略允许时切换备用 Provider；
- 用户可立即人工接管；
- 手工表单或批量导入由 Output Contract 生成；
- 人工提交必须通过与 Agent 相同的验证器。

### 5.3 复用现有 Agent 内核

保留并重构：

- 可序列化 Context；
- JSON Schema Tool；
- 工具错误自纠错；
- 显式结构化完成工具；
- RunState、checkpoint、progress 和 trace；
- Provider 无关的 LLMClient 抽象。

新增：

- 类型化模型错误；
- Provider 路由与健康熔断；
- `needs_human` Tool Outcome 和可恢复挂起；
- Command Proposal；
- 规则推断工具集和 `submit_rule_bundle` 完成工具。

Agent 和手动 UI 必须调用同一个 WorkflowDraft Command 层。Agent 只能提出命令，发布由用户和发布服务完成。

## 6. 产品路径

### 6.1 创建流程

1. 选择业务目标或从空白开始；
2. 选择/上传样本和目标；
3. 系统执行确定性画像；
4. Agent 可用时给出带证据建议，不可用时直接进入手动编辑；
5. 用户确认记录粒度、唯一键、硬约束和典型搜索问题；
6. 系统生成线性节点和内联 RuleBundle；
7. 样本端到端预览并修正；
8. 发布不可变 Revision。

### 6.2 线性编辑器

- 插入节点必须同时兼容前后连接并原子重连；
- 删除节点只有在前后可兼容桥接时才允许；
- 替换节点必须同时满足两侧合同；
- 追加只展示可以消费当前末端产出的 Capability；
- 侧输入只能来自流程输入；
- 所有错误定位到具体节点、端口或规则字段。

## 7. 实施阶段

### Phase 1：新领域核心

- [x] 新建 `internal/domain/workflow`；
- [x] 定义 Capability、WorkflowDraft、Connection、RuleBundle 和 DesignSession；
- [x] 实现线性拓扑、端口、规则和手动可用性校验；
- [x] 建立领域测试和规范化哈希。

完成标准：完全不依赖旧 orchestrator/model；`llm=nil` 不影响领域操作。

### Phase 2：Draft Command 与 Capability Catalog

- [x] 新建 `internal/app/workflow`；
- [ ] 实现 Draft Command、乐观并发、撤销所需事件；
- [x] 实现插入、删除、替换、追加、前置、连接和规则编辑命令；
- [x] 注册首期线性 Capability；
- [x] 提供 Capability、Draft、Validate、Preview API。

完成标准：后端 API 可以在没有 Agent 的情况下完成流程设计。

### Phase 3：规则画像与手动编辑器

- [ ] 实现样本画像服务；
- [x] 新建纵向线性流程编辑器；
- [ ] 新建 DataContract、ExtractionSpec、SearchSpec、OutputContract 编辑器；
- [ ] 实现规则影响和差异视图；
- [x] 实现样本预览与验收用例管理（temporary manifest；真实 Capability dry-run 留 Phase 5）。

完成标准：关闭 LLM 配置后，从空白创建、预览、验收并发布一条线性流程。

### Phase 4：Rule Synthesis Agent

- [x] 抽取 Agent Runtime 通用内核；
- [x] 增加类型化错误、Provider fallback 和 `needs_human`；
- [x] 增加 Proposal/Human 核心工具，Agent 不直接修改/发布；
- [ ] 增加基于画像的规则推断工具集；
- [ ] UI 展示证据、置信度、决策和实时 trace。

完成标准：Agent 中途断开后能无损切换手动模式或恢复。

### Phase 5：新线性运行时

- [ ] 新建 WorkflowRun/NodeRun 状态机；
- [ ] 实现线性调度、lease、checkpoint、重试和幂等输出；
- [ ] 实现人工完成协议；
- [ ] 实现节点产物查看器；
- [ ] 接入首期数据处理 Capability。

完成标准：自动和人工产生的相同端口产物可被同一下游节点消费。

### Phase 6：切换与删除

- [ ] 新 API 和前端路由成为唯一入口；
- [ ] 删除旧 Definition/Task/Profile API 与页面；
- [ ] 删除旧数据库表、仓储、模型和文档；
- [ ] 删除旧 Platform Agent 业务工具；
- [ ] 重建单一初始 migration；
- [ ] 全仓检查不存在旧术语和引用。

完成标准：代码库中不再存在兼容层或旧流程范式。

## 8. 首期 Capability 与模板范围

首期支持：

- 文件解析；
- 结构化抽取；
- 确定性清洗；
- 数据校验；
- 记录人工审核；
- 数据发布；
- 检索索引；
- 知识分析；
- 分析人工确认；
- 业务制品生成。

首期模板：

- 数据清洗入库；
- 数据清洗并建立索引；
- 独立索引构建；
- 产品方案生成。

Bug 分析的双产出和知识图谱的分支/汇聚不进入首期；不得用隐藏旁路伪装成线性流程。

## 9. 强制验收门槛

1. 界面允许的增删改操作不会产生断链 Draft；不兼容操作必须在执行前阻止。
2. Capability 合同只有一个后端事实源。
3. 创建普通流程不要求单独创建或命名 Schema/Profile。
4. `llm=nil` 时服务可启动，设计、预览和发布路径完整。
5. Agent 无法直接发布 Workflow Revision。
6. Agent 与人工结果使用同一输出验证器。
7. 所有自动规则都可解释、可编辑、可测试、可追溯。
8. 发布 Revision 自包含，不依赖可变规则资源。
9. 每个成功节点产物可按节点和端口打开。
10. 全仓测试、架构检查、类型检查和真实端到端用例通过。

## 10. 当前执行切片

本轮已完成 Phase 1，并启动 Phase 2 的手动优先设计会话基础：

- [x] 建立新领域包和核心模型；
- [x] 实现单链连接及端口兼容校验；
- [x] 实现 RuleBundle 基础校验、证据追溯和高风险决策确认规则；
- [x] 实现 Agent 可选、人工可继续的 DesignSession 状态；
- [x] 注册首期 Capability Catalog；
- [x] 实现节点原子插入和可桥接删除；
- [x] 添加覆盖无 LLM、断链、分支、能力版本、高风险决策和编辑原子性的测试。

下一执行切片是建立 Draft Command 的持久化事务边界（幂等、乐观并发与事件），然后暴露新的 Draft/Validate API；在这之前不接旧 API，也不建立任何兼容层。

## 11. 当前代码基线

本节描述已经存在的实现。新开发者应先读这些文件及其测试，再开始新增代码。

| 文件 | 已实现职责 | 不应承担的职责 |
|---|---|---|
| `internal/domain/workflow/model.go` | 新资源类型、Capability、端口、显式连接、RuleBundle、Draft、Revision | 持久化、HTTP DTO、旧模型转换 |
| `internal/domain/workflow/catalog.go` | Capability 注册、版本查找、端口和人工降级不变量 | 运行 Executor、读取数据库 |
| `internal/domain/workflow/validate.go` | Draft/Publish 两级结构化校验 | 自动修复 Draft、调用模型 |
| `internal/domain/workflow/revision.go` | 从连接推导线性顺序、固化 Capability、生成内容哈希 | 发布事务、版本号分配 |
| `internal/domain/workflow/design_session.go` | 手动/Agent 会话、Proposal、HumanQuestion、故障降级 | 调用 LLM、直接执行命令 |
| `internal/app/workflow/catalog.go` | 首期 Capability 唯一应用目录 | 兼容旧 StepKind |
| `internal/app/workflow/draft_editor.go` | 原子插入、可桥接删除 | 持久化、并发控制、旧 Definition 编译 |
| `internal/domain/workflow/workflow_test.go` | 领域、发布和降级主场景 | 数据库或 HTTP 集成 |
| `internal/app/workflow/draft_editor_test.go` | 编辑原子性和类型桥接 | UI 行为 |

当前实现中的重要事实：

- `WorkflowDraft.Nodes` 的数组顺序没有执行语义；`LinearOrder` 只读节点间主连接。
- Draft 校验把尚未配置完整的内容作为 warning，结构损坏始终是 error；Publish 校验把必填项升级为 error。
- `BuildRevision` 会重新执行 Publish 校验，固化完整 CapabilityDefinition、默认配置、规则、连接和验收用例，然后计算 SHA-256。
- `StaticCatalog` 拒绝没有人工完成能力的 LLM Capability。
- `InsertBetween` 当前只支持切开两个相邻节点间的主连接；失败返回原 Draft，不发生部分修改。
- `RemoveAndBridge` 当前只允许删除具有明确前驱和后继、且没有暴露流程输出的中间节点。
- DesignSession 已建模模型故障与人工问题，但尚未持久化，也尚未接入现有 Agent Loop。

在继续 Phase 2 前必须修正两个模型缺口：

1. `AcceptanceCase` 增加 `last_passed_revision` 和 `last_preview_id`。只保存 `last_passed=true` 无法证明当前 Draft Revision 已通过。
2. `RuleExpression.Expression string` 不得成为脚本入口。应改成类型化受控 DSL，或直接复用 `record_cleaning.go` 的规范化结构；禁止 JavaScript、SQL、CEL、模板表达式和任意函数调用。

## 12. Capability v1 精确合同

Capability Catalog 是唯一事实源。前端不得再维护平行的端口和默认配置表。

| Capability | 主输入 | 主输出 | 侧输入/交付输出 | 所需规则 | LLM/人工 |
|---|---|---|---|---|---|
| `source.parse@1` | `assets: asset_set` | `documents: parsed_documents` | 无 | 无 | 否/不适用 |
| `document.extract@1` | `documents: parsed_documents` | `drafts: record_drafts` | 无 | DataContract、ExtractionSpec | 是/必须支持 |
| `data.transform@1` | `drafts: record_drafts` | `records: transformed_records` | 无 | DataContract | 否/不适用 |
| `data.validate@1` | `records: transformed_records` | `validation: validation_results` | `dataset: dataset` 必填侧输入 | DataContract | 否/不适用 |
| `human.review_records@1` | `validation: validation_results` | `approved: approved_records` | 无 | 无 | 人工节点 |
| `data.publish@1` | `approved: approved_records` | `dataset: dataset_boundary` | `batch: dataset_batch` 交付输出 | 无 | 有副作用 |
| `retrieval.build@1` | `dataset: dataset_boundary` | `snapshot: retrieval_snapshot` | 无 | DataContract、SearchSpec | 否；Embedding 可降级策略另行定义 |
| `knowledge.analyze@1` | `knowledge: retrieval_snapshot` | `analysis: analysis_result` | 无 | OutputContract | 是/必须支持 |
| `human.approve_analysis@1` | `analysis: analysis_result` | `approved: analysis_result` | 无 | 无 | 人工节点 |
| `artifact.render@1` | `analysis: analysis_result` | `artifact: artifact` | 无 | OutputContract | 有副作用 |

Capability 新增规则：

1. 先定义资源语义和可独立查看的输出，再定义配置字段。
2. 必须说明幂等键、可重试错误、不可重试错误、checkpoint 粒度、副作用和人工完成方式。
3. 配置 Schema 必须 `additionalProperties=false`，默认配置必须通过同一 Schema 校验。
4. 版本内合同不可变；端口、语义或恢复协议变化必须升版本。
5. 不允许以 Capability 配置保存 Profile ID 或旧步骤引用。
6. `HasSideEffects=true` 的节点在预览时必须使用 dry-run/模拟输出，不能写正式数据。

当前 `ConfigSchema` 只做了合法 JSON 检查。Phase 2 必须增加真正的 JSON Schema 校验器，并在 Catalog 注册、Draft Command、Publish 和 Run 启动四处复用。

## 13. Draft Command 执行协议

所有人工 UI 和 Agent Proposal 都只能提交同一命令信封：

```json
{
  "command_id": "uuid",
  "expected_revision": 7,
  "type": "insert_between",
  "payload": {},
  "actor": {"type": "user", "id": "user-1"}
}
```

`actor` 是应用层命令信封字段，由认证上下文填充；HTTP 请求体不能自行声明或覆盖操作者身份。没有认证系统的开发阶段使用显式的本地开发主体，也不能信任前端传入的 user ID。

命令处理固定顺序：

1. 按 Workflow ID 开启数据库事务并锁定 Draft 行。
2. 用 `command_id` 查重；重复命令返回第一次的结果，不再执行。
3. 比较 `expected_revision`；不一致返回 `revision_conflict` 和最新 revision，不做自动合并。
4. 反序列化具体命令并执行领域方法；领域方法不得访问数据库。
5. 执行 `ValidateDraft`；任何 error 都回滚，warning 随响应返回。
6. 将 revision 精确加一，写 Draft 和 CommandEvent。
7. 同事务写 inverse command 或变更前快照信息，供撤销生成新命令。
8. 提交后返回完整 Draft、issues 和新 revision。

首期命令集合：

| 命令 | 核心前置条件 | 原子结果 |
|---|---|---|
| `create_from_blank` | 输入、输出、首节点合同合法 | 建立最小可运行链 |
| `insert_between` | 新 Capability 可消费前驱并产出后继所需类型 | 一条旧边替换为两条新边 |
| `append_after` | 新主输入匹配当前尾节点主输出 | 原尾节点输出改接新节点，必要时重绑流程输出 |
| `prepend_before` | 新主输出匹配当前头节点主输入 | 流程输入改接新节点，再接旧头节点 |
| `remove_and_bridge` | 前驱输出与后继输入同类型；节点无不可迁移交付输出 | 删除节点及其边并桥接邻居 |
| `replace_node` | 新节点同时兼容前后端口；侧输入齐全 | 保持节点位置，替换 Capability/配置/端口 |
| `set_node_config` | 配置通过 Capability ConfigSchema | 更新配置，不改变连接 |
| `bind_side_input` | 仅 workflow_input → side input，类型一致 | 新增或替换侧输入绑定 |
| `set_workflow_port` | 端口名、类型和现有连接一致 | 更新流程输入/输出合同 |
| `set_data_contract` | 字段类型和 key_fields 合法 | 更新合同并使旧验收结果失效 |
| `set_extraction_spec` | 只引用 DataContract 字段 | 更新抽取/转换/校验规则 |
| `set_search_spec` | 字段存在且向量字段为 string | 更新搜索语义 |
| `set_output_contract` | 字段合同合法 | 更新分析/人工/制品输出合同 |
| `confirm_decision` | actor、时间、路径和值完整 | 写入高风险确认记录 |
| `upsert_acceptance_case` | 输入和期望为合法 JSON | 新增或更新用例并标记未运行 |
| `accept_proposal` | Proposal revision 等于当前 Draft revision | 执行 Proposal 内命令并记录接受者 |
| `undo` | 目标事件属于当前 Draft 且可逆 | 以新 revision 执行 inverse command |

规则失效原则：

- 修改字段、记录粒度、唯一键、节点配置或连接后，当前 revision 的所有验收通过状态失效。
- 修改某个高风险路径时，旧 `confirmed_by/confirmed_at` 必须清空；用户本次明确确认的命令除外。
- 接受某个 Proposal 后，所有基于旧 revision 的 pending Proposal 变为 obsolete。
- Undo 不是数据库回滚，也不降低 revision；它是一条可审计的新命令。

## 14. 持久化模型与事务边界

Phase 2 建议新增以下表。字段名可在实现时微调，但职责不可混合：

### `workflows`

- `id/workspace_id/key/name/description`
- `draft_revision`
- `draft_document jsonb`
- `active_revision_id`
- `created_by/updated_by/created_at/updated_at`
- 唯一约束：`(workspace_id, key)`

`draft_document` 保存完整 WorkflowDraft 内容；查询不跨多个 Profile 表拼装草稿。

### `workflow_command_events`

- `id/command_id/workflow_id`
- `base_revision/result_revision`
- `actor_type/actor_id`
- `command_type/command_payload/inverse_payload`
- `created_at`
- 唯一约束：`command_id`、`(workflow_id, result_revision)`

### `workflow_revisions`

- `id/workflow_id/revision_no/content/content_hash`
- `published_by/published_at`
- 唯一约束：`(workflow_id, revision_no)`、`content_hash`

Revision 内容必须足够让运行时在 Capability Catalog 后续升级或规则资源删除后仍能执行和审计。

### `workflow_design_sessions`

- `id/workflow_id/draft_revision/mode/status`
- `agent_available/failure/pending_question/proposals`
- `agent_state/trace`
- `created_at/updated_at`

### `workflow_previews`

- `id/workflow_id/draft_revision/status`
- `input/output_manifest/issues`
- `started_by/started_at/finished_at`
- 预览产物必须标为 temporary，不能进入正式 Dataset/Artifact。

### `workflow_runs`、`node_runs`、`node_resource_bindings`

在 Phase 5 创建，不能复用旧 tasks/step_runs 表：

- WorkflowRun 固化 revision_id、inputs、status、当前节点和运行策略；
- NodeRun 固化 node_id、ordinal、Capability 快照、attempt、状态、checkpoint、progress、lease 和错误；
- NodeResourceBinding 固化每个 node_id + port 的资源类型、资源 ID、boundary 和 provenance。

开发期可以增加临时 `0002_workflow_rebuild` 迁移建立新表，目的是让新路径可独立运行，不是兼容旧数据。Phase 6 切割时删除旧表并把迁移链重新压成单一 `0001_init`。

## 15. Repository 与应用服务边界

新增 `internal/port/workflow_repo.go`，接口按用例需要拆分，不建立万能 Repository：

```go
type WorkflowDraftRepo interface {
    GetDraft(ctx context.Context, id string) (*workflow.WorkflowDraft, error)
    ApplyCommand(ctx context.Context, request DraftCommit) (*workflow.WorkflowDraft, error)
}

type WorkflowRevisionRepo interface {
    Publish(ctx context.Context, draft workflow.WorkflowDraft, revision workflow.WorkflowRevision) error
    GetRevision(ctx context.Context, id string) (*workflow.WorkflowRevision, error)
}
```

实际 `ApplyCommand` 需要把锁、revision CAS、事件写入和 Draft 更新放在同一事务。不要先 `GetDraft`，在应用层修改后再无条件 `Save`。

应用层建议拆为：

- `DraftService`：创建、读取和提交命令；
- `ValidationService`：返回 Draft/Publish issues，不产生副作用；
- `PreviewService`：按 Draft revision 执行临时样本；
- `PublicationService`：重新校验、分配 revision_no、构建 Revision 并原子发布；
- `DesignService`：持久化 DesignSession、调用 Agent、处理 Proposal/HumanQuestion；
- Phase 5 的 `RunService`：创建、启动、暂停、继续、人工完成和读取运行快照。

HTTP Handler 只处理鉴权上下文、参数、DTO 和错误映射；不能直接调用领域函数或仓储。

## 16. 新 HTTP API 合同

新系统使用无旧版本含义的 `/api` 路径；切换后删除旧 `/api/v2` 业务路由。开发工作区中旧路由会在切割前客观存在，但新路由必须使用独立 DTO、服务、仓储和表，不得调用、转换或返回旧模型；任何可发布构建都不能同时暴露两套产品入口。

### 16.1 设计与发布

| 方法 | 路径 | 用途 |
|---|---|---|
| `GET` | `/api/capabilities` | 返回版本化 Capability Catalog |
| `POST` | `/api/workflows` | 创建带初始 Draft 的 Workflow |
| `GET` | `/api/workflows` | 列出 Workflow 和 active revision |
| `GET` | `/api/workflows/:id` | 获取当前 Draft、revision 和 issues |
| `POST` | `/api/workflows/:id/commands` | 执行统一 Draft Command |
| `POST` | `/api/workflows/:id/validate` | 按 draft/publish 模式校验 |
| `POST` | `/api/workflows/:id/previews` | 对指定 draft revision 创建预览 |
| `GET` | `/api/workflow-previews/:id` | 查询预览、节点产物和 issues |
| `POST` | `/api/workflows/:id/acceptance-cases/:case_id/run` | 运行并记录验收用例 |
| `POST` | `/api/workflows/:id/publish` | 发布不可变 Revision |
| `GET` | `/api/workflows/:id/revisions` | 列出历史 Revision |
| `GET` | `/api/workflow-revisions/:id` | 获取自包含 Revision |

### 16.2 设计 Agent

| 方法 | 路径 | 用途 |
|---|---|---|
| `POST` | `/api/workflows/:id/design-sessions` | 创建手动或 Agent 会话 |
| `GET` | `/api/design-sessions/:id` | 恢复会话、问题、Proposal 和 trace |
| `POST` | `/api/design-sessions/:id/messages` | 发送目标、样本说明或修订要求 |
| `POST` | `/api/design-sessions/:id/questions/:question_id/answer` | 回答人工问题并恢复 Agent |
| `POST` | `/api/design-sessions/:id/proposals/:proposal_id/accept` | 通过 Command 层接受 Proposal |
| `POST` | `/api/design-sessions/:id/proposals/:proposal_id/reject` | 拒绝 Proposal |
| `POST` | `/api/design-sessions/:id/manual` | 立即切换手动编辑 |

### 16.3 运行

| 方法 | 路径 | 用途 |
|---|---|---|
| `POST` | `/api/workflow-runs` | 从已发布 Revision 创建运行 |
| `GET` | `/api/workflow-runs` | 运行目录 |
| `GET` | `/api/workflow-runs/:id` | 运行、节点和产物快照 |
| `POST` | `/api/workflow-runs/:id/start` | 启动 |
| `POST` | `/api/workflow-runs/:id/pause` | 请求暂停并保存 checkpoint |
| `POST` | `/api/workflow-runs/:id/resume` | 从 checkpoint 继续 |
| `POST` | `/api/workflow-runs/:id/nodes/:node_id/retry` | 重试失败节点 |
| `POST` | `/api/workflow-runs/:id/nodes/:node_id/manual-completion` | 提交人工产物 |
| `GET` | `/api/workflow-runs/:id/events` | 快照优先的 SSE 通知 |

统一错误响应：

```json
{
  "error": {
    "code": "revision_conflict",
    "message": "草稿已被其他操作更新",
    "path": "revision",
    "details": {"expected": 7, "actual": 8}
  }
}
```

建议状态码：参数/领域错误 422，并发冲突 409，不存在 404，未授权 401/403，模型暂时不可用但可手工继续返回成功会话状态而不是 5xx。

### 16.4 鉴权与工作区边界

- actor 和 workspace 来自服务端认证上下文，不接受请求体伪造；
- 所有 Workflow、Revision、Preview、DesignSession、Run 和资源查询都必须带 workspace 条件；
- UUID 只用于定位，不构成授权；跨 workspace 统一返回 404，避免泄露资源存在性；
- 绑定 Asset/Dataset/RetrievalSnapshot 时由应用服务验证资源类型、workspace 和可用状态；
- Agent 工具继承当前 workspace 和显式资源范围，不允许调用任意 ID；
- 发布、人工确认、人工完成和副作用节点必须写 actor、时间与 provenance；
- Capability Catalog 可以全局只读，但隐藏内部凭据、Provider 配置和实现细节。

## 17. 校验与发布门禁

校验输出必须保持结构化 `code/path/message/severity`。UI 不解析中文 message 来决定行为。

主要错误族：

- 身份：`workflow_key_invalid`、`node_id_invalid`、重复 ID；
- 端口：端点不存在、类型不匹配、单值端口多来源、必填输入未连接；
- 线性拓扑：branch、merge、cycle、self、disconnected、incomplete；
- 规则：缺 section、字段/key 不合法、搜索字段或分块参数非法；
- 决策：缺证据、高风险未确认、同路径重复；
- 验收：用例缺失、payload 非法、未通过或通过 revision 过期。

发布服务必须在同一事务外先完成纯校验，在事务内重新读取带锁 Draft 并确认 revision 未变化，然后构建 Revision。流程为：

```text
读取 Draft@N
  → ValidatePublish
  → 确认 acceptance.last_passed_revision == N
  → 开事务并锁 Draft
  → 再确认仍为 N
  → 分配 revision_no
  → BuildRevision
  → 写 workflow_revisions
  → 更新 workflows.active_revision_id
  → 提交
```

不允许用前端已经显示“校验通过”代替服务端发布校验。

## 18. 规则画像与推断执行细节

规则创建分成确定性画像、候选生成、人工确认和样本验收四层。

### 18.1 确定性画像

输入：AssetSet、ParsedDocument Blocks、可选目标 Dataset 和用户目标描述。

至少计算：

- 文件类型、页数、区块类型、表格列、章节路径和解析失败率；
- 字段名候选、出现率、空值率、唯一率、类型分布、长度范围和典型值；
- 可能的记录边界和重复模式；
- 可作为 key 的字段组合候选及冲突样本；
- 文本字段长度、枚举基数、日期/数值/单位形态；
- 可搜索字段候选和用户示例查询的词法/语义覆盖。

画像结果必须是独立、可缓存的数据结构；LLM 不可用时手动编辑器直接消费它。

### 18.2 Agent 候选

Agent 只接收：业务目标、确定性画像摘要、必要的证据片段、当前 Draft 和允许执行的工具。不得把全部原文件无边界塞进提示词。

首期工具：

- `get_profile_summary`
- `inspect_sample_records`
- `inspect_evidence`
- `propose_record_granularity`
- `propose_data_contract`
- `propose_extraction_spec`
- `propose_search_spec`
- `propose_output_contract`
- `validate_draft`
- `run_sample_preview`
- `request_human_decision`
- `submit_command_proposals`

工具只返回候选或查询结果；唯一写 Draft 的路径是用户接受 Proposal 后执行 Draft Command。

### 18.3 受控规则 DSL

首期归一化操作沿用现有确定性引擎能力：`enum_alias`、`boolean_alias`、`date`、`unit_scale`、`split`、`concat`。

首期校验操作：`required`、`regex`、`range`、`length`、`one_of`、`compare(eq/ne/lt/lte/gt/gte)`。

每条规则必须声明 operation 和类型化参数。服务端规范化后存储稳定 JSON；不存自然语言表达式作为可执行内容。自然语言只放 description。

### 18.4 合理性保证

系统不能证明业务语义绝对正确，只能通过以下门禁把错误显性化并可追溯：

1. 确定性统计支持每项候选；
2. 自动候选携带证据与置信度；
3. 高风险决定必须人工确认；
4. 规则能在代表性样本上解释输入到输出的变化；
5. 典型、边界、缺失、冲突和噪声样本进入验收用例；
6. Draft 变化使旧验收结果失效；
7. 发布固化规则、证据、确认和验收结果。

## 19. 前端实施细节

目标信息架构：

```text
/workflows                 流程目录
/workflows/new             创建向导
/workflows/:id/design      纵向线性编辑器
/workflows/:id/revisions   发布历史
/runs                      运行目录
/runs/new?revision_id=...  绑定输入并创建运行
/runs/:id                  节点时间线、trace 和产物
```

线性编辑器固定为纵向主链，不使用自由画布：

- 每个节点卡展示能力、输入交付物、输出交付物、规则依赖、模型/人工标记和当前 issues；
- 节点之间的加号只展示能桥接前后资源类型的 Capability；
- 删除按钮在无法桥接或存在交付输出时禁用并解释原因；
- 侧输入显示为节点卡内的资源选择器，不画成第二条主链；
- 右侧检查器按“基础配置、字段合同、抽取与清洗、搜索、输出合同、高级参数”渐进展开；
- 规则建议展示来源、置信度、证据和影响，逐条接受或修改；
- 底部固定栏显示 Draft revision、warning/error 数量、预览状态和发布按钮；
- 收到 409 时保留本地未提交表单，刷新最新 Draft 后要求用户重新应用，不静默覆盖。

手动模式必须与 Agent 模式使用相同编辑器。Agent 是建议面板，不是另一套创建流程。

前端文件建议：

- `web/src/api/workflows.ts`：新 API DTO；
- `web/src/pages/workflows/WorkflowList.tsx`；
- `web/src/pages/workflows/WorkflowDesigner.tsx`；
- `web/src/pages/workflows/components/LinearChain.tsx`；
- `web/src/pages/workflows/components/RuleEditors/*`；
- `web/src/pages/workflows/components/PreviewPanel.tsx`；
- `web/src/pages/runs/*`。

不要修改旧 `V2DefinitionNew.tsx` 形成渐进兼容；新路由完成后直接切换导航并删除旧页面。

## 20. Agent 内核改造细节

复用：

- `port.Context/Message/ToolSpec/LLMClient`；
- `agent.Tool`、`DocumentedTool` 和 `RequireToolTermination`；
- `RunState`、`TraceEnvelope`、checkpoint flush 和使用量统计；
- 工具错误作为 toolResult 返回模型自纠错的行为。

必须新增：

1. `ModelError` 分类器，把 Provider HTTP/协议错误映射为 DesignSession 已定义的故障类型。
2. `ProviderRouter`，按健康状态、优先级和故障预算选择 Provider；`Complete` 不是 fallback。
3. `ToolOutcome` 解释层，将 `needs_human` 持久化为 HumanQuestion 并安全退出 loop。
4. Proposal sink，要求 Agent 最终调用 `submit_command_proposals`，不能直接获得 DraftRepo。
5. 恢复入口，从持久化 Context、领域状态、PendingQuestion 和 trace 继续运行。
6. Prompt 组装器从实际工具和 Capability Catalog 构建说明，禁止硬编码不存在的工具。

Provider fallback 只处理技术故障，不得用另一模型绕过策略拒绝或用户确认。所有 Provider 都失败时，DesignSession 切到 manual；已经落盘的 Draft 和用户输入保持不变。

## 21. 新线性运行时执行细节

WorkflowRun 状态建议：

```text
draft → queued → running → pausing → paused
                         ├→ awaiting_manual_completion
                         ├→ failed
                         └→ succeeded
```

NodeRun 状态建议：

```text
pending → queued → running → retry_wait → queued
                    ├→ awaiting_manual_completion → validating → succeeded
                    ├→ failed
                    └→ succeeded
```

调度算法不使用 DAG：

1. 从 Revision 的连接计算并保存 node ordinal；
2. Run 启动时只把 ordinal=1 置 queued；
3. 当前 Node 成功且输出校验通过后，将 ordinal+1 置 queued；
4. 最后节点成功后校验 Workflow outputs 并完成 Run；
5. 任一节点 awaiting_manual_completion 时 Run 同步进入该状态；
6. 人工结果验证成功后恢复 running 并继续下一节点。

每次领取必须携带 `node_run_id + attempt + lease_owner`。checkpoint、progress、输出提交和完成更新都必须校验 owner 与 attempt，防止过期执行覆盖新结果。

模型节点故障策略：

1. 在重试预算内对 retryable error 指数退避；
2. ProviderRouter 有健康备用时切换；
3. 不可重试或预算耗尽时进入人工完成；
4. 用户也可以在重试期间立即接管；
5. 人工提交生成同一资源类型，记录 `producer=human`、提交者、时间和原因；
6. 继续执行前走同一输出验证器和幂等持久化事务。

## 22. 复用现有数据能力的方式

新 Capability Executor 通过新的适配接口调用成熟的数据服务，不把旧 Profile 或 StepRun 传进去：

| 新 Capability | 可复用实现 | 需要移除的旧耦合 |
|---|---|---|
| `source.parse` | BlobStore、AssetService、Parser、解析缓存 | 旧 StepRun checkpoint DTO |
| `document.extract` | Agent Loop、ExtractionUnit、证据校验、RecordDraft 持久化思想 | ExtractionProfileID、旧 Task 输入引用 |
| `data.transform` | `record_cleaning.go` 受控 DSL 和字段 Diff | 从 ExtractionProfile 读取规则 |
| `data.validate` | Schema 校验、业务规则、重复/冲突检测 | TargetSchemaID/Profile 交叉引用 |
| `human.review_records` | 审核决定、编辑重校验、provenance | `human.review` 特殊 StepKind |
| `data.publish` | Batch/Item/Outbox 原子事务和 attempt fencing | SourceStepRunID 作为唯一幂等身份 |
| `retrieval.build` | OpenSearch、pgvector、RRF、Snapshot 激活 | RetrievalProfileID |
| `knowledge.analyze` | KnowledgeScope、结构化完成工具、Agent trace | AnalysisProfileID |
| `artifact.render` | Artifact 持久化和下载 | 旧 Definition 输出绑定 |

适配后的输入必须来自 `ResolvedNode + RuleBundle + NodeResourceBinding`。禁止为了少改代码而临时构造旧 Profile/TaskDefinition；这会形成事实兼容层。

## 23. 测试矩阵

### 23.1 领域测试

- 所有 EndpointKind 的合法/非法组合；
- 类型不匹配、重复连接、占用单值端口；
- branch、merge、cycle、断链、自连接；
- 所有 RuleBundle 字段类型、key、搜索和证据边界；
- Draft 与 Publish 严重级别差异；
- 验收 revision 过期；
- Revision 哈希确定性和深拷贝；
- 无 LLM Capability 注册和 DesignSession 降级。

### 23.2 Command 测试

- 每个命令成功、前置条件失败和原 Draft 不变；
- command_id 幂等；
- expected_revision 冲突；
- 撤销产生新 revision；
- 高风险确认和验收结果失效；
- Agent Proposal 与人工命令产生相同 Draft。

### 23.3 Repository/HTTP 集成

- 两个并发命令只有一个成功；
- 重复 command_id 返回相同结果；
- Publish 锁内检测 revision 漂移；
- Revision 内容可以在空 Catalog/无 Profile 表条件下读取；
- `llm.api_key` 为空时 Capability、Draft、Validate、Preview、Publish API 可用；
- 结构化错误 code/path/status 稳定。

### 23.4 浏览器端到端

- 从空白创建七节点数据清洗并索引流程；
- 在中间插入/删除节点后预览仍通过；
- 不兼容 Capability 不出现在插入候选；
- 手工完成字段、记录粒度、key 和 SearchSpec；
- Agent 断开后无刷新切到手动继续；
- 409 冲突不覆盖用户输入；
- 发布后从 Revision 创建运行并查看每个节点产物。

### 23.5 故障测试

- LLM 未配置、超时、401、429、5xx、非法工具 JSON、上下文溢出；
- Provider fallback 成功与全部失败；
- Worker 在 checkpoint 前后崩溃；
- lease 过期后旧 Worker 提交被拒；
- 人工完成提交非法资源、重复提交和提交后恢复。

## 24. Phase 2 下一切片文件级任务

建议按以下顺序提交，每一步都保持主干可测试：

1. **领域补强**
   - 扩展 AcceptanceCase 的 revision/preview 证据；
   - 把 RuleExpression 改为类型化 DSL；
   - 增加 Replace/Append/Prepend/Config/Rule commands；
   - 完成规则失效测试。
2. **Command Service**
   - 新建 `internal/app/workflow/command.go` 和 `service.go`；
   - 定义 CommandEnvelope、CommandResult 和领域错误；
   - 实现内存 Repository contract tests。
3. **持久化**
   - 新建 `internal/port/workflow_repo.go`；
   - 新建 `internal/infra/repository/workflow_repo.go`；
   - 增加临时开发迁移和 PostgreSQL 并发集成测试。
4. **HTTP**
   - 新建 `internal/app/workflow/api.go` DTO；
   - 新建 `internal/infra/httpgin/handler_workflow.go`；
   - 在 `Services` 注入新 Workflow 服务并挂 `/api/capabilities`、`/api/workflows`；
   - Handler 测试覆盖 422/409/404。
5. **前端最小纵向切片**
   - 新建 API client、Workflow 目录和线性编辑器；
   - 只先支持读取、插入、删除、配置和校验；
   - 不改旧 Definition 页面。
6. **手动发布闭环**
   - 接入规则编辑、预览、验收和发布；
   - 用空 LLM 配置完成真实浏览器验收。

这一切片完成标准：从空白创建流程、配置内联规则、执行样本预览、通过验收并发布 Revision，全程不调用 LLM，也不写任何旧 Definition/Profile 表。

## 25. 完成和切割检查表

新入口切换前必须同时满足：

- [ ] 新 Workflow API 覆盖设计、发布和运行；
- [ ] 手动无 LLM 端到端用例通过；
- [ ] Agent Proposal 和人工编辑共用 Command 层；
- [ ] 所有 LLM Capability 支持人工完成；
- [ ] 新运行时通过 lease/checkpoint/fencing 故障测试；
- [ ] 新前端不 import 旧 workflowBlocks/taskTemplates/Definition DTO；
- [ ] 新代码不 import 旧 task_definition/orchestrator/profile 模型；
- [ ] 数据服务不再要求 Profile ID；
- [ ] 旧 Platform Agent 业务工具已替换；
- [ ] 导航和 API 只暴露新入口；
- [ ] 旧表、代码、页面、测试和文档已删除；
- [ ] migration 已压成新系统单一初始基线；
- [ ] 全仓无 `TaskDefinition`、`StepDefinition`、`depends_on`、`$task.`、`$step.` 和 Profile ID 残留；
- [ ] `make test`、TypeScript、PostgreSQL 集成和浏览器 E2E 全绿。
