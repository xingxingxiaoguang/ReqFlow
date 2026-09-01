# 技术债台账

> 活账本：修一销一，新增债随波登记。产品级平台通用债另见 [HANDOVER §5.3](./HANDOVER.md)（两处不重复立账，修哪边就销哪边）。
> 每条债记录四件事：现状（在哪）、影响（咬谁）、处置方向（怎么还）、优先级。登记 ≠ 承诺排期——还债与产品波次一起排。

> **纯新系统口径**：项目尚未上线，不保留旧 API、旧数据或旧范式兼容层；本台账只登记现存代码的落地债。

## 线性工作流重建专项（2026-09）

| 编号 | 债 | 影响 | 处置方向 | 优先级 |
|------|-----|------|----------|--------|
| WF-2 | 开发期 Command Service 使用固定 local actor，尚未接入认证与 workspace 上下文 | HTTP 可用但不能证明多主体/跨 workspace 授权边界 | 接入服务端认证上下文和 workspace-scoped Repository 查询；请求体永不覆盖 actor | 高（Phase 6 前销账） |
| WF-4 | Design Agent 已具备 Proposal/Human 核心工具，但 Profile/sample/evidence 查询工具尚未绑定实际数据源 | Agent 目前可安全表达建议和人工问题，不能基于样本自动生成完整规则候选 | Phase 3 画像服务完成后注入只读查询工具；写入仍只走 Proposal sink | 高（Phase 4 收尾/Phase 3 依赖） |
| WF-6 | 新资源生产者外键已统一指向 WorkflowRun/NodeRun，但开发期迁移链仍按 `0001`～`0005` 分段 | 项目未上线却保留演进历史会增加 fresh schema 审计成本 | 删除 Profile 范式后将最终新系统压成单一 `0001_init` | 高（Phase 6 收官销账） |
| WF-7 | 数据能力 HTTP handler 与服务注入字段仍带 `v2`/`V2` 内部命名 | 不影响路由合同，但持续暗示存在多个代际，降低纯新系统可读性 | 唯一入口稳定后统一重命名文件、函数和字段 | 中（Phase 6 收官销账） |
| WF-8 | Preview 中 `document.extract` / `knowledge.analyze` 尚未接入真实模型 dry-run，仅支持显式人工模拟样本（标记 simulated 并按冻结 Schema 校验） | 预览只能证明确定性链路（解析/清洗/校验）的真实行为，LLM 节点行为由用例样本钉住 | 抽取/分析服务提供不落库的样本级模型执行入口后再接入；在此之前不允许伪造成功 | 中（画像/样本工具波次一并评估） |

## 已销账记录（Legacy 切割，2026-09-01）

- **WF-3 已销账**：Preview 已接入按 `CapabilityRef` 注册的 dry-run 执行器并顺序执行 Draft 的 ResolvedNode。`source.parse` 在内存中运行真实解析器；`data.transform`/`data.validate` 运行真实确定性内核并对照目标 Dataset 只读复查 Schema、key_fields 与已提交条目冲突；`data.publish`/`retrieval.build`/`artifact.render` 使用专用 dry-run（真实位点计算与内容渲染，但不落库、不建索引）；LLM/人工节点消费 `input.samples` 显式样本，标记 `simulated` 并按冻结合同 Schema 逐条校验。所有输出只进入 `workflow_previews.output_manifest`，`temporary=true`。Acceptance 用用例自身 input 重跑并按结构化 expectation 比较 manifest，全部通过才原子更新 `LastPassedRevision/LastPreviewID`。LLM 节点的真实模型 dry-run 另立 WF-8。
- **WF-5 已销账**：Parsed/Extraction/Transform/Validation/Review/Publish/Retrieval/Analysis/Artifact 的生产者身份、attempt fencing 与数据库外键均已切换到 `workflow_runs` / `workflow_node_runs`；旧 Task Orchestrator、StepRun、Platform Agent 会话与配置模型已删除。
- **WF-1 已销账**：`document.extract`、`data.transform`、`data.validate`、`retrieval.build`、`knowledge.analyze` 与 `artifact.render` 已直接消费 Revision 的 `RuleBundle`；清洗内核直接接收 Workflow 领域规则类型，不再维护私有 JSON DTO 镜像。RecordDraftSet、RetrievalSnapshot、AnalysisResult 均冻结自包含合同及哈希。

**元数据模块专项债（MD-1~MD-8）与数据集归属化专项债（DA-1~DA-4）已整体销账**：两账描述的旧元数据体系（metadata_registry / seed→override→effective / 兼容规则引擎 / 动态 FTS 索引）与旧数据集体系（datasets.schema 字段定义归属、受控编辑、merge/upsert/replace 写入、archived_* 归档表）均已删除。当前系统以 Revision 内联 DataContract、资源自包含合同快照、不可变 DatasetSchemaDefinition 和只追加 Batch 表达执行事实，不再存在 Profile 表或 Profile ID。

- ~~导出格式口径漂移~~（文档写 YAML / 实现为 JSON）：口径已在 HANDOVER §3.4 定为 JSON。
- ~~锚行双语义、数据集类型所有权不变量无文档~~（代码有约束、设计无处可查）：均已并入 HANDOVER §3.4。

## 数据集归属化专项（2026-08，M5 收尾盘点）

> M5「字段定义归属数据集」改造（datasets.schema 真相源 + 动态 FTS/筛选索引 + 任务创建绑定 + create 模式退场）遗留：

| 编号 | 债 | 影响 | 处置方向 | 优先级 |
|------|-----|------|----------|--------|
| DA-1 | 数据集级 schema 编辑无版本历史视图 | 审计已落 metadata_audit（kind=dataset_schema, key=数据集ID），但 HistoryDrawer 只认类型键——实例级回看无 UI | History 端点已按 (kind,key) 通用，前端加数据集维度入口即可 | 中 |
| DA-2 | InVector 变更后存量条目向量懒更新 | 受控编辑只告警 NeedsReembed 不自动重嵌；存量条目在下次内容更新时才按新口径重嵌，期间语义检索口径混排 | 需要时提供「数据集重嵌」用例（照 DatasetWriter 向量化分批路径） | 中 |
| DA-3 | 门内改绑异构数据集无字段映射 | 任务绑定数据集后若在门内显式改绑另一 schema 的数据集，草稿按新 schema 校验、缺失字段整批 invalid（预览可见，属显式决策） | 字段名相同即值透传的自动映射 + 差异提示 | 低 |
| DA-4 | 归档动态索引回收依赖 ArchiveService 挂钩 | 直接 SQL 删数据集行（绕过用例）会残留 dsidx_* 索引（无害但累积）；无定期对账 | 启动时对账一次（pg_indexes vs datasets 全集） | 低 |
