# ReqFlow 开发契约与规范（v1.0.0）

> 面向后续开发者。本文回答四件事：**当前架构与技术路线、两条参考流程的实现契约、如何开发新流程、如何给数字大脑添加工具**。
> 产品定位与方向性决策见 [PRODUCT.md](./PRODUCT.md)；交接上下文与踩坑总表见 [HANDOVER.md](./HANDOVER.md)。本文与两者冲突时，以代码 + 本文为准（本文随 v1.0.0 交付固化，2026-09-02）。

## 1. 架构路线与技术路线

### 1.1 架构路线（不变量，违反即架构回退）

- **四层单向依赖**：`cmd → infra → app → port → domain`，由 `make lint-arch` 强制。`infra/httpgin` 只准 import `app`；`domain` 只准标准库。接口集中在 `internal/port`，实现在 `internal/infra`，用例在 `internal/app`。
- **定义与执行分离**：`TaskDefinition` 是端口化 DAG（怎么做的合同）；`Task` 从 active Definition 派生并**冻结完整定义快照 + 资源绑定 + 数据边界**。运行期不受流程后续演进影响。
- **不可变资源链**：Schema / ExtractionProfile / RetrievalProfile / DatasetBatch / ApprovedRecordSet / RetrievalSnapshot 均创建后不可改。合同变化 = 创建新资源（无 PUT/PATCH）。删除带引用防护（见 §2.4）。
- **StepRun 是执行事实源**：数据库 lease/checkpoint/fencing 保证恢复与并发安全；SSE/Broker 只做 UI 通知，不承担状态。
- **写路径必经人工 Gate + 显式审核资源**：AI 只产出草稿与候选；正式数据发布只消费不可变 `ApprovedRecordSet`，经 attempt fencing 的 Batch 事务原子提交。Agent（数字大脑）是只读顾问，没有任何写工具。
- **优雅降级**：依赖缺失不阻断启动（无 embedding/rerank → 检索自动降级并回显策略），但缺什么要在策略/日志里可见。

### 1.2 技术路线

| 层 | 选型 | 备注 |
|----|------|------|
| 前端 | React 18 + antd 5 + Vite + TanStack Query | 构建产物 `go:embed` 进单二进制 |
| 后端 | Go + Gin + GORM | SQL 迁移内嵌二进制，`auto_migrate` 启动自动执行 |
| 数据 | PostgreSQL 16 + pgvector（vector 1024 维硬约束） | 追加型数据集，`commit_seq` 连续递增 |
| 检索 | OpenSearch BM25 + pgvector + 加权 RRF + SiliconFlow rerank | 物理索引按（数据集, 规则）惰性创建，随构建自愈 |
| LLM | OpenAI 兼容协议（+ Anthropic 适配器） | 流程内 Agent loop 统一运行壳 `internal/app/agent` |
| 部署 | docker compose（pg + opensearch）+ `make start` | 单机单实例多客户端；升级 = 换二进制重启 |

## 2. 参考实现：两条固定流程的契约

v1 只交付两条内置流程（种子见 `internal/app/orchestrator/builtin_definitions.go`），流程管理界面已隐藏。**任何新流程都以这两条为契约范本。**

### 2.1 数据清洗入库（key=data_clean_import）

```text
assets(asset_set) + target(dataset)
  → parse(source.parse)            文件 → ParsedDocumentSet（逐文件状态/checkpoint）
  → extract(document.extract)      按 ExtractionProfile 驱动 Agent，产出 RecordDraftSet
  → transform(data.transform)      确定性归一化（单位/日期/枚举由代码而非 LLM 决定）
  → validate(data.validate)        Schema + 业务规则校验 → ValidationResultSet
  → review(human.review)           人工门：approve/edit/exclude → 不可变 ApprovedRecordSet
  → publish(data.publish)          只消费 ApprovedRecordSet，幂等 Batch 原子发布
输出 batch(dataset_batch)
```

契约要点：

- **抽取规则按字段服务**：`ExtractionProfile.TargetSchemaID` 固化目标结构，规则本身（granularity、system_instruction）创建后不可变。任务与数据集必须结构一致，前端创建任务时按 `target_schema_id` 过滤。
- **规则在任务运行期注入**：流程定义中步骤 config 为空占位；创建任务时通过 `step_configs: {step_id: {extraction_profile_id}}` 注入，冻结进任务快照。
- **每个环节是一等 Manifest 资源**：ParsedDocumentSet → RecordDraftSet → TransformedRecordSet → ValidationResultSet → ApprovedRecordSet → DatasetBatch，全部逐条落库、可恢复、可审计（provenance 记录到 Asset/Block 级）。

### 2.2 建立检索索引（key=retrieval_index）

```text
dataset(dataset_boundary，创建任务时固化 through_seq)
  → build(retrieval.build)   BM25 + Vector 覆盖相同 source_seq，计数校验后激活 Snapshot
输出 snapshot(retrieval_snapshot)
```

契约要点：

- **快照是合同**：`source_seq` 固化数据边界；词法/向量覆盖计数不一致则不得激活。增量构建基于上次 active 快照续跑。
- **检索策略默认值（写死在工具/前端推荐值）**：hybrid 模式重排序默认开启、`score_threshold=0.3`（rerank 分 0..1）、`lexical_weight/semantic_weight=0.4/0.6`（服务端归一化）、top 8。分数语义：**最终分 = rerank 分**，RRF 融合分保留原始量纲只做排序；rerank 未配置自动降级且阈值归零。
- **索引规则删除防护**：仍有快照的 RetrievalProfile 拒绝删除（409）；删除规则时清理残留向量 chunk，BM25 物理索引按规则 ID 命名不复用、无需清理。数据结构删除防护同理（被数据集/抽取规则/索引规则任一引用即 409）。**这是所有不可变资源删除的通用范式：有 lineage 引用 → 409 + 指引文案；无引用 → 硬删 + 清理派生物。**

### 2.3 执行器两级校验契约

每个步骤 Kind 的 Executor 可实现 `RuntimeStepValidator`：

- `ValidateDefinition`（定义级）：流程发布/任务创建时校验；允许规则 ID 为空占位（v1 固定流程的形态）。
- `ValidateRuntimeStep`（运行级）：任务创建时以合并后的有效定义校验，要求规则 ID 非空且资源存在。
- 运行期 `Execute` 对空规则兜底报错。任务创建入口只允许对 `document.extract` / `retrieval.build` 两类步骤做 `step_configs` 浅合并（allowlist 在 `definition_service.go`），其余 Kind 注入配置直接拒绝。

### 2.4 资源删除防护契约

| 资源 | 防护引用 | 端点 |
|------|----------|------|
| 数据结构 Schema | 数据集（含归档）/ 抽取规则 / 索引规则 计数 | `DELETE /api/v2/schemas/:id` |
| 抽取规则 | record_draft_sets + transformed_record_sets 计数 | `DELETE /api/v2/extraction-profiles/:id` |
| 索引规则 | retrieval_snapshots 计数（删除时顺带清 retrieval_chunks） | `DELETE /api/v2/retrieval-profiles/:id` |
| 索引快照 | 无（可删；物理索引自愈） | `DELETE /api/v2/retrieval-snapshots/:id` |

统一形态：应用层定义 `ErrXxxInUse` 哨兵错误 → handler `errors.Is` 映射 409（错误文案给出引用计数与解除路径）→ 其余错误 404。前端预览弹窗内嵌删除按钮，后端错误原文透出给用户。

## 3. 如何开发一个新流程

v1 里"新流程"= 新增一种步骤 Kind（或组合既有 Kind）+ 注册一条内置流程定义。按此清单执行：

1. **能组合就不新增**：先看既有 Kind（`source.parse / document.extract / data.transform / data.validate / human.review / data.publish / data.query_derive / retrieval.build / knowledge.analyze / data.analysis_publish / artifact.render / graph.build`）能否表达。禁止在 Orchestrator 状态机里加业务分支。
2. **实现 Executor**（参考 `internal/app/retrieval/executor.go` 的两级校验形态）：
   - 实现 `StepExecutor`（`Execute`）+ 可选 `RuntimeStepValidator`；注册进 `cmd/reqflow/main.go` 的 Registry（Kind 是分发键，封闭集）。
   - 输入只引用 `$task.<port>` / `$step.<id>.<port>`；输出写 `step_resource_bindings`，不塞 progress。
   - Manifest 一等资源化：产物建独立表 + 仓储 port + attempt fencing + 逐条 checkpoint（参考 `cleaning_repo.go`）；迁移文件成对新增 `NNNN_*.up/down.sql`。
   - 幂等：同 StepRun 同 attempt 重跑不重复产出；外部调用严禁放进 Batch 提交事务。
3. **前端同步三处**（无编译防漏，手工检查）：
   - `web/src/pages/v2/workflowBlocks.ts`（Executor 目录 / 资源类型 / 默认配置）；
   - `web/src/pages/v2/status.ts` 的 `STEP_KIND_LABEL`；
   - 涉及规则注入时，`NoCodeTaskNew.tsx` 的 `step_configs` 组装与任务创建页表单。
4. **内置流程种子**：面向业务的新流程在 `builtin_definitions.go` 增加定义（config 留空占位，规则由 `step_configs` 运行期注入）；种子按 `(workspace_id, key)` insert-if-missing，已有同名定义不动。
5. **测试基线**：Executor 单测（两级校验 + Execute 幂等）+ 仓储集成测试 + `make test` 全绿；架构围栏不允许 handler/import 越层。

## 4. 如何给数字大脑（platformagent）添加工具

数字大脑是**只读**平台助手，工具全部在 `internal/app/platformagent/tools.go` 的 `buildTools` 注册。规范：

1. **只读红线**：新工具不得产生写副作用（不写数据集、不创建/运行任务、不落库业务数据）。查询类工具统一 `workspaceID` 过滤；返回前做存在性校验。
2. **Spec 契约**：`port.ToolSpec{Name, Description, Parameters}`。
   - `Parameters` 是 **JSON Schema 字符串**——必须 `json.Valid` 可解析（`service_test` 有防漏校验），`additionalProperties:false`，枚举/上下限内联写进 schema；
   - `Description` 面向模型写清：能力边界 + **推荐参数值与调参场景**（如 query_data 的阈值 0.3 / 权重 0.4-0.6 / rerank 默认开），让模型按任务自主决定，而不是只给开关。
3. **输出契约**：`ToolOutput{Output（回传模型，JSON 或结构化文本）, Details（UI 工具轨迹一行摘要）, IsError}`。错误走 `toolError`，文案要可行动（如"多个数据集时返回 name(ID) 清单"——模型拿错误就能自愈，而不是死循环）。
4. **自愈优先**：参数缺失时尽量自动消解（唯一候选自动锁定），只有真正歧义才报错并列出选项。
5. **注册与启停**：工具加进 `buildTools` 的 `all` 列表即获得设置页启停能力；`PromptSnippet/PromptGuidelines` 必须同步——系统提示词从实际工具集组装，工具增删提示词自动跟随。
6. **内置 Skill 种子**：面向用户的引导型能力做成纯文本 Skill（`builtin_skills.go`，按 `(workspace, slug)` upsert，启动刷新内容；用户可在设置中停用）。Skill 无工具执行能力，只注入提示词；涉及保存类动作一律引导用户到 UI 表单完成。欢迎页卡片（`AgentHome.tsx quickPrompts`）与 Skill 清单保持一致。

## 5. 种子数据契约（交付开箱体验）

所有启动种子（`builtin_definitions.go` / `builtin_skills.go` / `cmd/reqflow/starter_kit.go`）遵守同一契约：

- **幂等**：按业务键（definition key / skill slug / 资源 name）存在即跳过；绝不覆盖业务后续改动，绝不重复创建（冷启动与二次启动已验证）。
- **启动期执行**：在 `main.go` 服务组装完成后同步执行；失败仅告警不阻断启动（流程种子失败则退出——它是功能前提）。
- **开箱即用**：种子互相引用完整（示例数据集 ↔ 结构 ↔ 规则成套），让用户零配置即可走通"清洗 → 索引 → 检索"闭环；数据集种子保持空数据，业务数据只能由清洗任务发布产生。
