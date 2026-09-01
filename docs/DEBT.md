# 技术债台账

> 活账本：修一销一，新增债随波登记。产品级平台通用债另见 [HANDOVER §5.3](./HANDOVER.md)（两处不重复立账，修哪边就销哪边）。
> 每条债记录四件事：现状（在哪）、影响（咬谁）、处置方向（怎么还）、优先级。登记 ≠ 承诺排期——还债与产品波次一起排。

> **V2 口径**：Legacy 元数据与数据集体系已于 2026-09-01 随切割整体删除（见下方销账记录）；本台账只登记现存代码的债务。

## 线性工作流重建专项（2026-09）

| 编号 | 债 | 影响 | 处置方向 | 优先级 |
|------|-----|------|----------|--------|
| WF-1 | 新 Workflow 受控规则 DSL 已在 `internal/domain/workflow` 成为设计事实源，但旧 `record_cleaning.go` 仍维护私有 JSON DTO | Phase 5 接入执行器前存在合同镜像漂移风险；当前新 Draft 尚未调用旧执行路径 | 新 Capability Executor 直接消费 Workflow DSL，并把旧清洗实现改为执行纯函数或随 Phase 6 旧模型删除 | 高（Phase 5 销账） |

## 已销账记录（Legacy 切割，2026-09-01）

**元数据模块专项债（MD-1~MD-8）与数据集归属化专项债（DA-1~DA-4）已整体销账**：两账描述的 Legacy 元数据体系（metadata_registry / seed→override→effective / 兼容规则引擎 / 动态 FTS 索引）与 Legacy 数据集体系（datasets.schema 字段定义归属、受控编辑、merge/upsert/replace 写入、archived_* 归档表）已随 V2 切割整体删除——相关服务、仓储、端口、handler、前端页面、配置组（match/fts）与数据库表不复存在，V2 以不可变 DatasetSchemaDefinition + ExtractionProfile + 只追加 Batch + 状态归档重新表达全部能力。旧债不再适用，不再登记。

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
