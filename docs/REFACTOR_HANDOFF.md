# ReqFlow 重构执行交接

更新时间：2026-09-01

## 1. 当前停点

代码停在第八波“真实 Preview 与正式人工审核链”的设计切入点，尚未开始该波代码修改。工作树在写本文档前是干净的；最近一个功能提交是：

- `471db3f refactor: inline workflow resource contracts`

当前主链已完成到：

1. 新 Workflow Draft、Command、Rule DSL 与发布校验；
2. Draft 持久化、并发、幂等与新 API；
3. Preview/Acceptance/Revision 的首版闭环与新前端工作流页面；
4. Agent fallback、熔断、`needs_human` 与 DesignSession；
5. WorkflowRun/NodeRun 线性运行时、lease、checkpoint、retry、pause/resume、attempt/owner fencing；
6. 业务资源 producer 从旧 Task/StepRun 全量切到 WorkflowRun/NodeRun，旧 Orchestrator 与 Platform Agent 删除；
7. 全部自动 Capability 接入新运行时，Profile 范式删除，执行合同改为 Revision 内联并由资源冻结。

下一位接手者不要重做以上波次，也不要恢复任何旧 Task、StepRun、Profile、`/api/v2` 兼容层或旧数据迁移。

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

## 3. 当前系统事实

### 3.1 唯一运行模型

- 运行真相只有 `workflow_runs`、`workflow_node_runs`、`node_resource_bindings`。
- 运行时已注册：`source.parse`、`document.extract`、`data.transform`、`data.validate`、`data.publish`、`retrieval.build`、`knowledge.analyze`、`artifact.render`，以及两个人工 Capability。
- NodeRun 的租约、checkpoint、progress、完成写入均受 `attempt + lease_owner` fencing。
- Capability execution 的 `workspace_id` 来自已发布 Revision，不由节点请求体提供。

### 3.2 唯一规则与资源合同

- `DataContract`、`ExtractionSpec`、`SearchSpec`、`OutputContract` 只存在于 Workflow `RuleBundle`。
- `RecordDraftSet` 冻结 DataContract、ExtractionSpec、编译后的 JSON Schema 及各自哈希。
- `TransformedRecordSet` 继承合同哈希；转换和校验直接消费领域规则类型，不再经过私有 JSON DTO。
- `RetrievalSnapshot` 冻结 SearchSpec、SearchSpecHash、DataContractHash 与 embedding model。
- `AnalysisResult` 冻结 instruction、OutputContract、编译后的 OutputSchema 及哈希。
- 后端代码和数据库定义中不存在 Extraction/Retrieval/Analysis Profile 表或 Profile ID。

### 3.3 仍在的开发期结构

- 数据能力 handler 文件、函数和依赖字段仍带 `v2`/`V2` 内部命名，但唯一公开根路由已是 `/api`。
- 前端仍保留 `web/src/pages/v2/**`、`web/src/pages/agent/**`、`web/src/api/v2/**`、旧导航与 Profile UI；这些应整体删除，不能改造成兼容适配器。
- migration 仍是 `0001`～`0005` 开发链，最终必须压成一个纯新 `0001_init`。

## 4. 最近验证结果

第七波提交前已执行：

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
- fresh migration 测试因本机 `127.0.0.1:5432` 连接拒绝，按测试内既有约定跳过，其余结果通过。

每个后续功能波次仍必须独立执行同一组门禁并单独提交。涉及迁移时必须再次运行 fresh migration 测试；PostgreSQL 未启动只能记录为跳过，不能伪称实际迁移成功。

## 5. 下一波：真实 Preview 与正式人工审核

### 5.1 已确认的问题

当前 `internal/app/workflow/preview.go` 仍生成静态 temporary manifest，没有调用 Capability。当前 Acceptance 只检查一个 Preview 是否属于当前 Draft revision 后直接盖章：

- 没有用 AcceptanceCase 自己的 input 重跑；
- 没有比较 `expectation`；
- 因而不能证明样本行为。

当前 `/workflow-runs/:id/nodes/:node_id/manual-completion` 接受客户端直接提交 `NodeResourceBinding`，只校验端口和资源类型，仍允许伪造任意同类型资源 ID。`review_repo.go` 已能创建不可变 `ApprovedRecordSet`，但对应应用服务在旧 Orchestrator 删除时一并移除了。

### 5.2 推荐实现边界

1. 建立按 `CapabilityRef` 注册的 Preview dry-run 接口和临时资源绑定；Preview 顺序执行 Draft 的 ResolvedNode。
2. 所有 Preview 输出必须带 `temporary=true`，不得进入正式 Dataset、Artifact、OpenSearch 索引或正式资源表。
3. `HasSideEffects=true` 的 Capability 必须有专用 dry-run；禁止调用正式 publish。
4. LLM/人工节点在模型不可用时允许显式样本输出，但必须标记为人工模拟并按冻结 Schema 校验，不能默默伪造成功。
5. Acceptance endpoint 应读取并重跑目标 case 的 input，按结构化 expectation 比较真实 manifest；只有本次运行通过才能原子更新 `LastPassedRevision/LastPreviewID`。
6. 建立按 Capability 注册的 ManualCompletion handler；HTTP 只提交领域 payload，actor/workspace 由服务端上下文注入。
7. `human.review_records` handler 根据节点真实 `ValidationResultSet` 输入创建不可变 `ApprovedRecordSet`，完整覆盖每条记录并复查 Dataset 冲突，然后返回服务端生成的资源绑定。
8. `human.approve_analysis` 不应复用任意客户端 AnalysisResult ID。长期最干净的做法是在 `analysis_results` 中创建一条由人工 NodeRun 生产、合同一致且 Schema 校验通过的 succeeded 结果，供 `artifact.render` 正常消费。
9. 通用“客户端提交 outputs”路径完成替换后直接删除，不保留兼容参数。

第八波建议提交边界：真实 Preview、真实 Acceptance、两个人工 handler、相关 API/测试和债账更新一起完成并独立提交。

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
- `WF-3`：Preview 尚非真实 dry-run；
- `WF-4`：缺确定性画像、样本和证据工具；
- `WF-6`：migration 尚未压平；
- `WF-7`：内部仍有 V2 命名。

`WF-1` 与 `WF-5` 已销账，不要重新登记为“待兼容”的工作。

## 8. 继续工作时的第一组命令

```bash
git status --short
git log --oneline -8
sed -n '1,240p' docs/REFACTOR_HANDOFF.md
sed -n '1,220p' internal/app/workflow/preview.go
sed -n '1,240p' internal/app/workflow/runtime.go
sed -n '1,260p' internal/infra/repository/review_repo.go
```

继续时先确认工作树只包含预期变更；不要 reset，也不要覆盖用户改动。
