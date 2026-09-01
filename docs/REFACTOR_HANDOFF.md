# ReqFlow 重构执行交接

更新时间：2026-09-01（第八波完成后）

## 1. 当前停点

第八波「真实 Preview 与正式人工审核链」已完成并随本文档一并提交。主链现状：

1. 新 Workflow Draft、Command、Rule DSL 与发布校验；
2. Draft 持久化、并发、幂等与新 API；
3. Preview/Acceptance/Revision 闭环与新前端工作流页面；
4. Agent fallback、熔断、`needs_human` 与 DesignSession；
5. WorkflowRun/NodeRun 线性运行时、lease、checkpoint、retry、pause/resume、attempt/owner fencing；
6. 业务资源 producer 从旧 Task/StepRun 全量切到 WorkflowRun/NodeRun，旧 Orchestrator 与 Platform Agent 删除；
7. 全部自动 Capability 接入新运行时，Profile 范式删除，执行合同改为 Revision 内联并由资源冻结；
8. Preview 改为真实 dry-run（按 CapabilityRef 注册执行器，顺序执行 ResolvedNode，输出全部 temporary）；Acceptance 用用例自身 input 重跑并按结构化 expectation 比较；`human.review_records` / `human.approve_analysis` 人工完成改为按 Capability 注册的领域处理器，通用「客户端提交 outputs」路径已删除。

下一位接手者不要重做以上波次，也不要恢复任何旧 Task、StepRun、Profile、`/api/v2` 兼容层、旧数据迁移或通用 outputs 提交路径。

## 2. 已落地提交

| 提交 | 内容 |
|---|---|
| `0eb6432` | 原子 Draft Command、规则 DSL、Schema 校验 |
| `8ec1f62` | Draft 持久化、并发、幂等、基础 API |
| `f38bc78` | Preview、Acceptance、Revision、前端工作流 |
| `aa126ea` | Agent 错误分类、fallback、熔断、人工挂起、DesignSession |
| `0d6aad4` | WorkflowRun/NodeRun 运行时与 Worker |
| `40beffc` | producer 切到 WorkflowRun/NodeRun，删除旧运行面 |
| `471db3f` | 内联资源合同、删除 Profile、补齐全部自动 Executor |
| （本波） | 真实 Preview dry-run、真实 Acceptance、人工完成处理器、债账更新 |

## 3. 当前系统事实

### 3.1 唯一运行模型

- 运行真相只有 `workflow_runs`、`workflow_node_runs`、`node_resource_bindings`。
- 运行时已注册：`source.parse`、`document.extract`、`data.transform`、`data.validate`、`data.publish`、`retrieval.build`、`knowledge.analyze`、`artifact.render`，以及两个人工 Capability（运行时统一进入 awaiting_manual_completion）。
- NodeRun 的租约、checkpoint、progress、完成写入均受 `attempt + lease_owner` fencing；人工确认类资源写入受 `awaiting_manual_completion` 状态 + `producer_node_run_id` 唯一约束 fencing。
- Capability execution 的 `workspace_id` 来自已发布 Revision，不由节点请求体提供。

### 3.2 唯一规则与资源合同

- `DataContract`、`ExtractionSpec`、`SearchSpec`、`OutputContract` 只存在于 Workflow `RuleBundle`。
- `RecordDraftSet` 冻结 DataContract、ExtractionSpec、编译后的 JSON Schema 及各自哈希；`TransformedRecordSet` 继承合同哈希；`RetrievalSnapshot` 冻结 SearchSpec 与 embedding model；`AnalysisResult` 冻结 instruction、OutputContract、编译 Schema 及哈希。
- 后端代码和数据库定义中不存在 Extraction/Retrieval/Analysis Profile 表或 Profile ID。

### 3.3 Preview / Acceptance / 人工完成合同（第八波新事实）

- Preview 输入是 `{"inputs":{<流程输入端口>:{resource_id,boundary}},"samples":{<节点ID>:{<端口>:<样本载荷>}}}`，DisallowUnknownFields 严格解码。
- Preview 引擎（`internal/app/workflow/dry_run.go`）按 `DryRunRegistry` 顺序执行节点；任一节点失败即停止并把下游标记 `skipped`；全部输出进入 `workflow_previews.output_manifest`，`temporary=true`，样本嵌入上限 64KiB。
- dry-run 真实性分级：`source.parse` 内存真实解析；`data.transform`/`data.validate` 运行 `internal/domain/logic` 真实内核并只读复查目标 Dataset；`data.publish`/`retrieval.build`/`artifact.render` 专用 dry-run（不落库、不建索引）；LLM/人工节点只接受 `samples` 显式样本，标记 `simulated` 并按冻结合同 Schema 校验（真实模型 dry-run 是 `WF-8`）。
- Acceptance endpoint `POST /api/workflows/:id/acceptance-cases/:case_id/run` 不再接收 `preview_id`：服务端用用例自身 input 重跑、按 expectation（`{"nodes":{<节点>:{status,simulated,outputs:{<端口>:{metrics:{…}}}}}}`，只比较声明过的字段）比较真实 manifest，通过才原子更新 `LastPassedRevision/LastPreviewID`；未通过返回 422 + mismatches，不盖章。
- 人工完成 endpoint `POST /api/workflow-runs/:id/nodes/:node_id/manual-completion` 只提交 `{"payload":{…}}`；actor/workspace 由服务端注入，输出绑定全部由处理器生成。
- `human.review_records` 处理器（`internal/app/pipeline/manual_review.go`）：载荷 `{rationale,decisions:[{validation_result_id,action,fields?,note?}]}` 必须覆盖 ValidationResultSet 全部记录；approve 沿用结果字段、edit 服务端按 Schema 规范化并重算 ItemKey/Fingerprint、exclude 保留审核时字段；生成不可变 `ApprovedRecordSet`（`producer_node_run_id` 幂等，review_hash 不同即拒绝）。
- `human.approve_analysis` 处理器（`internal/app/analysis/manual_approval.go`）：不复用客户端 AnalysisResult ID，而是为新 `analysis_results` 行（`CreateHumanApprovedAnalysis`，状态 fencing + 幂等）写入 succeeded 人工结果，合同哈希必须与 Revision 一致、输出按 OutputContract 校验。

### 3.4 仍在的开发期结构

- 数据能力 handler 文件、函数和依赖字段仍带 `v2`/`V2` 内部命名，但唯一公开根路由已是 `/api`。
- 前端仍保留 `web/src/pages/v2/**`、`web/src/pages/agent/**`、`web/src/api/v2/**`、旧导航与 Profile UI；这些应整体删除，不能改造成兼容适配器。
- migration 仍是 `0001`～`0005` 开发链，最终必须压成一个纯新 `0001_init`。

## 4. 最近验证结果

第八波提交前已执行：

```bash
make test
cd web && pnpm exec tsc --noEmit
git diff --check
go test -tags integration ./internal/infra/database \
  -run TestIntegrationFreshMigration -count=1 -v
```

结果：

- Go test、vet、架构约束、密钥扫描全部通过；
- TypeScript 类型检查通过；
- `git diff --check` 通过；
- fresh migration 测试按本机 PostgreSQL 可用性决定是否跳过；未启动只能记录为跳过，不能伪称实际迁移成功。

每个后续功能波次仍必须独立执行同一组门禁并单独提交。涉及迁移时必须再次运行 fresh migration 测试。

## 5. 下一波：确定性画像与 Design Agent 工具

优先做 §6.1（样本画像 + Design Agent 只读工具 + 前端证据展示，销账 `WF-4`）。要点重申：

- 画像必须可缓存：文件/区块统计、字段候选、空值率、唯一率、类型与长度分布、key 候选、检索字段候选和证据样本。
- 给 Design Agent 注入只读 profile/sample/evidence/validate/preview 工具；Proposal 仍只能经用户接受后走 Draft Command，Agent 不能直接获得 Repository。
- `WF-8`（LLM 节点真实模型 dry-run）可在画像波次一并评估：抽取/分析服务需要一个不落库的样本级模型执行入口，接入前 Preview 的 LLM 节点保持显式样本。

## 6. 后续剩余波次

### 6.1 确定性画像与 Design Agent 工具

- 实现可缓存的样本画像：文件/区块统计、字段候选、空值率、唯一率、类型与长度分布、key 候选、检索字段候选和证据样本。
- 给 Design Agent 注入只读的 profile summary、sample、evidence、validate 和 preview 工具。
- Proposal 仍只能经用户接受后走 Draft Command，Agent 不能直接获得 Repository。
- 前端展示画像、证据、Proposal 差异和 trace。
- 完成后销账 `WF-4`。

### 6.2 身份与 workspace 边界

- actor/workspace 必须由服务端请求上下文提供，请求体不能覆盖。
- Workflow、Revision、Preview、DesignSession、Run 和资源读取都要 workspace-scoped；跨 workspace 按不存在处理。
- 完成后销账 `WF-2`。

### 6.3 删除旧前后端范式

- 删除 `web/src/pages/v2/**`、`web/src/pages/agent/**`、`web/src/api/v2/**`、旧模板/Block 和旧导航。
- 首页只进入 `/workflows`，导航保留 Workflow、Runs、必要资源和设置。
- 后端 `handler_v2_*`、`V2*` 服务字段统一改为无代际命名；不新增旧路由别名。
- 更新 README、HANDOVER 和配置注释中的旧 Profile/Task/V2 叙述。
- 完成后销账 `WF-7`。

### 6.4 压平迁移与最终验收

- 把当前 `0001`～`0005` 压成单一 `0001_init.up.sql` / `down.sql`，测试期无需兼容旧数据库。
- producer FK 全部直接指向 WorkflowRun/NodeRun。
- fresh schema 必须断言不存在 Task/StepRun、Profile、Agent session/config、旧 metadata/archive 表。
- migration integration test 期望版本改为 `1`。
- 跑全仓门禁和真实浏览器主链验收；完成后销账 `WF-6`。

## 7. 活跃技术债

以 `docs/DEBT.md` 为唯一账本。当前活跃项：

- `WF-2`：固定 local actor 与 workspace 授权边界；
- `WF-4`：缺确定性画像、样本和证据工具；
- `WF-6`：migration 尚未压平；
- `WF-7`：内部仍有 V2 命名；
- `WF-8`：Preview 的 LLM 节点尚未接入真实模型 dry-run（现为显式样本 + simulated 标记）。

`WF-1`、`WF-3` 与 `WF-5` 已销账，不要重新登记为"待兼容"的工作。

## 8. 继续工作时的第一组命令

```bash
git status --short
git log --oneline -8
sed -n '1,260p' docs/REFACTOR_HANDOFF.md
sed -n '1,260p' internal/app/workflow/dry_run.go
sed -n '1,180p' internal/app/workflow/preview.go
sed -n '1,240p' internal/app/pipeline/manual_review.go
sed -n '1,180p' internal/app/analysis/manual_approval.go
```

继续时先确认工作树只包含预期变更；不要 reset，也不要覆盖用户改动。
