# 元数据管理模块 · 分波执行计划

> 设计依据见 [METADATA.md](./METADATA.md)，本文只回答：分几波、每波做什么（文件级）、怎么验收、怎么回退。
> 波次编号 M1~M4，独立于产品波次（第二波 Bug 链路等）并行推进；M1/M2 无相互依赖，M3 依赖 M2，M4 依赖 M3。
> 每波交付以 `make test` 全绿（vet + test + 架构围栏 + 密钥扫描）+ 前端 `make build` 通过为门禁。

## 波次总览

| 波次 | 内容 | 依赖 | 规模预估 | 状态 |
|------|------|------|---------|------|
| M1 | 注册点收敛 + 只读元数据目录 + 提示词预览 | 无 | 后端 ~5 文件，前端 ~5 文件 | ✅ 已交付 |
| M2 | 草稿字段袋化（全链路 map 贯通，消灭 DraftItem struct） | 无（建议在 M1 后） | 后端 ~10 文件 + 迁移，前端 ~4 文件 | ✅ 已交付 |
| M3 | 元数据存储 + 受控编辑 + 兼容守卫 + 导入导出 | M2 | 后端 ~8 文件 + 2 迁移，前端编辑器 | ✅ 已交付 |
| M4 | 工作流定义外置 + 新任务类型向导 | M3 | 与产品第四波合流 | 🔲 |

---

## M1 · 注册点收敛 + 只读元数据目录 ✅ 已交付

**目标**：任务类型定义收敛为单一聚合注册表；元数据页上线，「看懂一个任务类型」从翻 4 个文件变成开 1 个页面 + 提示词预览。**纯增量 + 薄委托重构，零行为变更。**

**交付记录**：`app/registry.go`（聚合注册表 + 一致性单测）、`app/metadata.go`（Catalog / TaskTypeView / PromptPreview + 渲染单测）、`httpgin/handler_metadata.go`（3 端点）、前端 `/metadata` 路由 + 「元数据」菜单 + `Metadata.tsx`（概览/字段合同/装配描述/提示词预览四区）；文档（README/PRODUCT/HANDOVER）已同步。

### 后端

1. **`app/registry.go`（新）**：`TaskTypeDefinition` 聚合 + `TaskTypes()` / `TaskTypeOf()`，聚合现有四处定义（`requirementImportWorkflow()` / `model.RequirementSchema` / `requirementProfile()` / `DatasetTypeOfTask` 映射）。
2. **薄委托**：`WorkflowOf`（workflow.go）、`AnalyzeProfileOf`/`profileFor`（analyze_profile.go）改为委托 registry；`model.SchemaOf` / `DatasetTypeOfTask` 保持原样（domain 被 registry 引用，方向合法）。`DatasetWriter` 写入计划里的 `datasetWritePlanFor`（runner.go）改从聚合取 schema + datasetType，删掉两级查找拼接。
3. **`app/metadata.go`（新）**：`MetadataService` —— `Catalog()`（总览）/ `TaskTypeView(type)`（聚合视图 DTO）/ `PromptPreview(type, special)`。预览复用 `renderAgentSystem` / `renderDocManifest` / `renderAnalyzePrompt` + `buildToolset`（零值依赖：空 DocSource / 空 sink / nil Ask），**与运行时同一渲染函数**。
4. **`infra/httpgin/handler_metadata.go`（新）+ server.go 路由**：`GET /api/metadata`、`GET /api/metadata/task-types/:type`、`POST /api/metadata/render/preview`。DTO 全部定义在 app 层（httpgin 只碰 app 的铁律）。
5. 既有 `/workflows`、`/datasets/schemas` 端点行为不变。

### 前端

1. `router.tsx` 增 `{ path: 'metadata', element: <Metadata /> }`；`AppLayout.tsx` 菜单增「元数据」。
2. `api/metadata.ts`（新）+ `types.ts` 增类型（`TaskTypeView` / `MetadataCatalog`，条目带 `source` 字段，M1 恒为 `builtin`）。
3. `pages/Metadata.tsx`（新）：左栏资产导航，右侧任务类型详情四区（概览步骤链 Timeline / 字段合同表格 / 装配描述 markdown+代码块 / 提示词预览三 tab，可填 special_requirements 调 preview 端点）。

### 测试

- `app/registry_test.go`：聚合与旧查找函数一致性（每任务类型：Workflow 相等 / Schema 相等 / Profile 相等 / DatasetType 相等）——保证薄委托不漂移。
- `app/metadata_test.go`：preview 渲染包含 schema 字段规范段、工具指南段；未注册类型报错。

### 验收标准

- `make test` 全绿 + `make build` 通过；
- 元数据页能完整展示 requirement_import 的步骤链/字段合同/指令头/三段提示词预览；
- 任务创建、分析、数据集生成全链路行为不变（委托重构零回归，跑一遍主链路确认）。

### 风险与回退

低风险：纯增量 + 委托。回退 = revert 单 commit。唯一注意点：`PromptPreview` 构造工具集时勿触发 `orDefault()` 意外路径（空 WriteSpec 会兜底 requirement，恰好正确，加测试钉住）。

### 文档同步（随本波交付）

README（API 表 + 文档链接）、PRODUCT（§5 底座表增行、路线图增行、§4 决策二补记 §7 内容）、HANDOVER（§3.2 代码地图 + §4.3 API 速查）。

---

## M2 · 草稿字段袋化（最大的一笔，独立一波）✅ 已交付

**目标**：草稿管线从 requirement 定型 struct 改为 schema 驱动的字段袋，**字段定义端到端生效**（schema 增删字段 → 提示词 / 写入校验 / 草稿存取 / 数据集写入全链路自动跟随）——这是元数据「受控编辑」（M3）的硬前置，也顺手消灭 METADATA §3 的 B1 双源漂移。

**验收金标准**：在聚合注册表加一个玩具任务类型（自定义 2~3 字段 schema，如 `test_review`），**不写任何 struct / Normalize / ValuesOf**，经 API 跑通「创建任务 → 解析 → 分析（单发模式即可）→ 草稿落库 → 生成数据集」全链路；requirement_import 则在前端全链路回归（含门内编辑与查重）。

**交付记录**（2026-08）：`model.TaskItem` 袋化（Fields JSON 文本 + `Values()` 读侧入口，`DraftItem` 全删）；`FieldSpec` 增 `Default`（含 `{current_time}`）/`Clean` 声明；`logic.NormalizeValues` schema 驱动归一化（代码零字段知识）；`tools.WriteSpec` 瘦身为 `{Name,Schema}`、`DraftSink` 存字段袋且 key=`ItemKeyOf`（与数据集条目身份同一口径）；`match` 改字段袋入参 + `TitleFieldOf` 标题口径；迁移 `0008_task_items_fields`（task_items 推倒重建为 fields TEXT，清空存量 33 行开发数据）；前端 `MatchImportPanel` schema 驱动编辑列（enum→Select/number→InputNumber/text→展开行/标题列挂查重徽标）。金标准自动化：`internal/app/golden_test.go`（`test_review` 玩具类型 agent 全链路 + 单发模式；注册缝 `registry.go extraTaskTypes` 生产恒空）。门禁与验收：`make test` + `make build` 全绿；真机迁移/API/前端编辑保存落库均验证通过。

### M2.0 执行原则（本波红线，先读）

1. **零兼容**：开发阶段没有正式数据——**不做旧形状兼容、不写数据迁移逻辑、不留双格式解析分支**。`DraftItem` 及其全部配套代码（转换器/白名单/物理列）直接删除；task_items 表推倒重建；归档表里的旧 `items_snapshot` 不用管（无正式数据）。一切"以防旧数据"的代码都是本波禁止的额外代价。
2. **架构白名单不破**（`make lint-arch` 把关）：httpgin→app；app→port/domain；domain 仅标准库。新代码照旧。
3. **产品红线不松动**（PRODUCT §8.4）：write_work_items 仍写内存 DraftSink；数据集写入仍走人工门；状态机与单写者不变。

### M2.1 现状地图：草稿数据今天怎么流（改造前必读）

```
【产出侧 · 两条入口】
agent 模式: write_work_items 工具 → applyWriteArgs(write.go:172)
            → validateDraft(schema) → w.Normalize(map, now)   ← WriteSpec.Normalize = logic.NormalizeDraft
            → DraftSink.Upsert(DraftItem)                     ← key = draftKey() 硬编码 title+project_name
单发模式:   parseDrafts(analyze.go:418, 宽松恢复) → logic.NormalizeDrafts([]any, now) → []DraftItem
            （agent 暂停续跑: Resume → DraftSink.ReplayFrom(会话消息, WriteSpec) 重放写入调用）

【收束】analyze.finalize(:355) → AnalyzeOutcome.Items = []model.TaskItem{ID, TaskID, DraftItem(嵌入), Status, ErrorMessage}
        → runner.execAnalyzeStep → tasks.ReplaceTaskItems 落库

【消费侧 · 三处】
门内编辑:   GET /tasks/:id (items) → 前端扁平 snake_case 展示/编辑
            → POST /tasks/:id/items {items:[{id?, draft: DraftInput}]} → task.ReplaceItems → toModel() → ReplaceTaskItems
查重:      POST /match/duplicates {items:[DraftInput]} → match.CheckDuplicates
            （DraftInput.Title 精确层；语义层 draftValuesOf(DraftInput.toModel()) 组装向量文档；
             语料侧 requirementTitle(fields) = requirementFieldsOf(fields).Title）
生成数据集: runner.execDatasetStep / PreviewDatasetWrite → GetTaskItems
            → draftValuesOf(item.DraftItem) 转 map → DatasetWriter.Prepare（此刻才回到 schema 世界：
            ItemKeyOf/FingerprintOf/ValidateValues 全 schema 驱动——数据集侧已是袋化终点）

【旁路】
归档:      archive.ArchiveTask → GetTaskItems → items_snapshot TEXT（TaskItem[] JSON 快照，非同构表）
SSE:       publishItems → items 事件（TaskItem JSON）→ useTaskEvents.ts:92 写入 react-query 缓存
仓储:      taskItemRow 12 物理列（repo.go:85）+ itemToRow/itemRowToModel 转换器
```

**改造的本质**：消费侧的"数据集写入"早已是 schema 驱动的 map 世界，中间的草稿态（DraftItem struct + 物理列）是仅存的 requirement 定型段。M2 = 把这段也变成 map 袋，让 schema 从头贯穿到尾。

### M2.2 逐触点任务清单（按依赖顺序执行，每步编译须绿）

**第 1 步 · domain（model + logic）**

| 文件 | 动作 |
|------|------|
| `model/model.go:54-120` | **删除 `DraftItem`**；`TaskItem` 改为 `{ID, TaskID string; Fields string; Status, ErrorMessage string}`（Fields = JSON 文本，与 `DatasetItem.Fields` 完全同构）；补 `func (t TaskItem) Values() map[string]any`（Unmarshal Fields，读侧统一入口） |
| `model/dataset_schema.go` | `FieldSpec` 增 `Default any`（默认值；`"{current_time}"` 占位 = 运行时刻，与 Prompt 的既有占位符约定同源）与 `Clean string`（清洗声明，当前唯一取值 `"state"`）；`RequirementSchema` 把原 draft.go 硬编码的默认值搬进来：priority=Medium、type_id=story、estimated_hours=8、start_at="{current_time}"、project_name="未分类项目"、title="未命名工作项"；state 字段标 `Clean:"state"` |
| `logic/draft.go` | **重写为 schema 驱动归一化**：`NormalizeValues(schema, raw map[string]any, now) map[string]any`——遍历 schema.Fields：string trim；enum 白名单取 `Enum`（越界回落 Default）；number 宽松解析（string 也收）+ 正数校验；Default 兜底（含 `{current_time}` → now.Format(RFC3339)）；`Clean=="state"` 走原 normalizeStateName 的占位词/疑似ID 清洗（正则随迁）。**删除** `NormalizeDraft`/`NormalizeDrafts`/`prioAllowList`/`typeAllowList` 与全部硬编码默认值 |
| `logic/schema.go` | `asNumber` 宽松数字解析从 tools/write.go 移入（归一化与工具校验共用）；`TitleFieldOf(schema) (FieldSpec, bool)`——取 `InVector==VectorTitle` 的字段（查重/展示标题的 schema 口径） |

**第 2 步 · tools（写入工具与 sink）**

| 文件 | 动作 |
|------|------|
| `tools/write.go` | `WriteSpec` 瘦身为 `{Name string; Schema model.DatasetSchema}`（**删 Normalize 函数指针**）；`DefaultWriteSpec` 相应简化（orDefault 判据改 Name/Schema.Type）；`DraftSink` 改存 `map[string]map[string]any`，**删除 draftKey**——key 直接 `logic.ItemKeyOf(schema, values)`（与数据集条目身份同一函数同一口径，"同组同名=同一条"语义天然保持）；`applyWriteArgs` 内 `w.Normalize(...)` → `logic.NormalizeValues(w.Schema, item, now)`，validateDraft 保留（ValidateValues + 正数）；`ReplayFrom` 零改动（本就用同一 applyWriteArgs 与同一 WriteSpec——重放确定性由 schema 驱动归一化天然保证） |

**第 3 步 · app（用例层）**

| 文件 | 动作 |
|------|------|
| `app/analyze.go` | `runClassic` 的 `NormalizeDrafts` → 按 profile.Schema 循环 `NormalizeValues`；`finalize` 的 `TaskItem{DraftItem: d}` → `TaskItem{Fields: marshalJSON(values)}` |
| `app/dataset.go` | **删除** `DraftInput`/`toModel`/`draftValuesOf`/`requirementFieldsOf`；`DraftSaveInput` 改 `{ID string; Fields map[string]any}`，`toTaskItem` → `TaskItem{Fields: marshalJSON(...)}` |
| `app/task.go` | `ReplaceItems`/`PreviewDatasetWrite` 中 DraftInput/draftValuesOf 引用按新形状改（items[i].Values() 取 map） |
| `app/runner.go` | `execDatasetStep` 的 `draftValuesOf(items[i].DraftItem)` → `items[i].Values()` |
| `app/match.go` | `CheckDuplicates(ctx, values []map[string]any)`；草稿侧标题 = `stringify(values[titleField.Key])`，语义层直接 `VectorDocOf(schema, values, ...)`（不再经 DraftItem 中转）；语料侧 `requirementTitle` → 解析 it.Fields 后按 `TitleFieldOf` 取；**范围控制**：语料类型仍写死 requirement（按数据集类型可配是 B3，留 M3） |
| `app/archive.go` | 零改动（TaskStepsSnapshot 序列化 model.TaskItem，形状自动跟随；旧快照按零兼容原则不处理） |

**第 4 步 · 仓储 + 迁移**

| 文件 | 动作 |
|------|------|
| `migrations/0008_task_items_fields.up.sql`（新） | `DROP TABLE task_items;` + 重建为 `(id UUID PK, task_id UUID REFERENCES tasks ON DELETE CASCADE, fields TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending', error_message TEXT, created_at TIMESTAMPTZ DEFAULT now())` + 原 task_id 索引。**fields 用 TEXT 存 JSON 文本，不用 JSONB**——与 dataset_items.fields 及 0003 注释的既有决策一致（GORM 字符串直写免类型转换；无库内查询需求）。down 照旧 DROP 恢复旧形状（仅供 revert） |
| `repository/repo.go:85` | `taskItemRow` 改 `{ID, TaskID, Fields, Status, ErrorMessage *string, CreatedAt}` |
| `repository/task_repo.go` | `itemToRow`/`itemRowToModel` 相应简化（转换器从 40 行缩到 ~15 行） |

> 迁移策略说明：改 0003 源文件 + 清库重建也可（HANDOVER §6 惯例），但需要本机手工 DROP；新增 0008 重启即自动生效（`database.auto_migrate: true`），且全新环境两步建表无害。选 0008。0007 的 archived_tasks 不用动（快照是 TEXT JSON，形状运行时跟随）。

**第 5 步 · HTTP 层**

| 文件 | 动作 |
|------|------|
| `httpgin/handler_tasks.go` | `taskItems` 端点入参 `{items:[{id?, fields: <字段对象>}]}`（bind 到 `app.DraftSaveInput`）；getTask/SSE snapshot 的 items 形状自动跟随（TaskItem 新 JSON：Fields 为 JSON **文本**，与 DatasetItem 一致——前端用 parseDatasetItemFields 解析） |
| `handler_match.go` | duplicates 入参 bind `[]map[string]any` |

**第 6 步 · 前端**

| 文件 | 动作 |
|------|------|
| `api/types.ts` | `TaskItem` 改 `{ID, TaskID, Fields: string, Status, ErrorMessage}`（复用 `parseDatasetItemFields` 解析）；**删除 DraftItem 接口**；新增 `DraftFieldValues = Record<string, any>` |
| `api/tasks.ts` | `saveItems` payload：`{id?, fields: Record<string, unknown>}`（签名已是 Record，改字段名即可） |
| `panels/MatchImportPanel.tsx` | 状态改持有 `{row: TaskItem, values: DraftFieldValues}[]`；**编辑列 schema 驱动**：拉 `tasksApi.schemas()` 取 requirement schema，按 `fields` 生成列——enum→Select(options=Enum)、number→InputNumber、string→Input、text→展开行 ParagraphBlock、date→Input(ISO 文本，别上 DatePicker 控范围)；**查重/保存 payload 直接发 values**（删掉两处手列 11 字段的对象字面量）；**统计卡与列头措辞保持 requirement 语境**（该面板本就是 requirement_import 的 per-type 工作区——PRODUCT §8.1 前端面板 per-type 是既定范式，别在本波发明"统计卡 schema 标注"之类的过度设计）；查重徽标 MatchBadge 挂在标题列上（列生成时识别 `in_vector==='title'` 的字段） |
| `TaskDetail.tsx` / `useTaskEvents.ts` | MonitorView 只用 `i.Status`（不受影响，确认即可）；useTaskEvents 的 items 补丁类型跟随 types.ts 自动通过 |
| `ConfirmParsePanel.tsx` / `AnalysisPane.tsx` | 不触草稿形状（确认即可，grep 一遍 `.title`/`.priority` 防漏） |

### M2.3 设计决策备忘（已定案；改动需在 PR 说明推翻理由）

1. **Fields 为 JSON 文本（string）而非 map/JSONB**：与 DatasetItem.Fields 同构、与 0003"TEXT 存 JSON"决策一致；`TaskItem.Values()` 是唯一读侧解析入口，避免散装 Unmarshal。
2. **DraftItem 彻底删除，不留别名/视图**（零兼容原则）。
3. **默认值/枚举/清洗全部进 schema**（`Default`/`Enum`/`Clean`），draft.go 只剩 schema 驱动的纯函数——B1 双源消灭，M3 编辑 schema 即可改默认值且提示词（Prompt 文案）/行为（Default）仍需人工对齐的残余点记录在 METADATA.md §4.4（Label/Prompt 属兼容变更）。
4. **DraftSink key = ItemKeyOf**：草稿身份与数据集条目身份共用同一函数——覆盖修订、首插序保留、"同 key 同条目"语义全链路一个口径。
5. **`{current_time}` 占位符**贯穿 Default 与 Prompt（既有约定复用，渲染点在 NormalizeValues 与 prompt 渲染器两处）。
6. **match 只泛化字段口径，不泛化语料类型**（B3 留 M3）。
7. **前端编辑控件 schema 驱动，面板语境 requirement 固定**——per-type 面板是产品范式，玩具任务类型的验收走 API 而非前端。

### M2.4 测试与验收

**单测改造（既有 5 个文件引用 DraftItem/NormalizeDraft，逐一迁移）**

- `logic/logic_test.go`：NormalizeValues 全分支（默认值/枚举越界回落/数字字符串/正数拒绝/state 清洗/{current_time}）；TitleFieldOf；ItemKeyOf 回归；
- `tools/tools_test.go`：DraftSink 袋化后 Upsert 覆盖/首插序/ReplayFrom 确定性重放（含被拒条目跳过口径）；
- `app/analyze_agent_test.go` / `task_test.go` / `archive_test.go`：全链路用例改袋断言（Fields JSON 内字段值）；归档快照新形状往返；
- **新增**：注册玩具任务类型（test_review）的端到端用例——自定义 schema（2~3 字段）+ 复用 requirement 四步工作流定义 + 最小 profile，断言分析→草稿→数据集全程字段自定义。**这就是金标准的自动化形态，也是"袋化成功"的定义**。

**真机验收（make test 之外必做）**

1. `agent_mode: true` 跑一篇真实文档：工具轨迹正常、write 回执正常、暂停 → Resume（会话重放袋化草稿）→ 门内编辑保存 → 查重（精确+语义各命中一次）→ 四种写入模式各跑一遍（create/merge/upsert/replace）；
2. `agent_mode: false` 单发模式全链路（宽松恢复路径）；
3. 任务归档 → 恢复（items_snapshot 新形状往返）；数据集页表格正常（读侧零改动，确认即可）；
4. 金标准手测：临时注册 test_review（或跑单测）经 curl 走完四步。

### M2.5 风险与坑

- **最大风险：JSON key 字符串散落**——struct 删除后编译器抓 struct 引用，但 `"title"`/`"priority"` 等字符串 key 不会编译报错。清点命令：`grep -rn '"estimated_hours"\|"solution_suggestion"\|"project_name"' internal/ web/src --include='*.go' --include='*.ts*'`，逐个确认是 schema 声明处还是散引处；
- 仓库已知坑索引（HANDOVER §4.5）：GORM 空 slice Create 报错（ReplaceTaskItems 已有提前 return，改行结构时保持）；UUID 必须仓储层显式赋值；`env -u GOROOT`；无 psql 用 docker exec；
- 前端 `rowKey={(_, i) => String(i)}` 现按索引——袋化后保持即可（编辑态本地数组，保存才落库）；
- 迁移 0008 会清空本机存量草稿——零兼容原则下预期行为，开跑前知会一声即可；
- **范围防线**：本波不做 match 语料类型泛化、不做统计卡 schema 标注、不做 per-type 前端面板框架——见 M2.3-6/7，越界设计留给 M3/M4 按需评估。

### M2.6 回退

单 PR revert + 0008 down（或 DROP task_items 后按 0003 重建）。无数据兼容负担，回退是干净的结构替换。

---

## M3 · 元数据存储 + 受控编辑 + 兼容守卫 ✅ 已交付

**目标**：schema 与 profile 可在元数据页受控编辑（DB override + 版本历史 + 兼容检查 + 审计 + 导出导入），seed/override/effective 三态生效。

**交付记录**（2026-08）：迁移 `0009_metadata_registry` + `0010_metadata_audit`（payload 依全库惯例用 TEXT-not-JSONB）；`logic/schema_compat.go` 兼容规则引擎（§4.4 全规则 + enum→string 放宽特例 + 提示词 `{{` 注入告警 + 形状校验含 snake_case key 硬校验——字段 key 会拼进过滤 SQL 的注入面收口）；`FingerprintOf` 纳入向量相关 schema 摘要盐（`VectorBodyLimit` 下沉 domain，InVector/InKey 变更不再跳过重嵌）；`port.MetadataRepo` + `repository/metadata_repo.go`（DISTINCT ON 取每 key 最新版）；`app/registry.go` override 合并层（进程内 effective，写后整体替换）+ `app/metadata_edit.go` 写路径（UpdateSchema/UpdateProfile/Reset/History/Export/Import，❌ 拦截 / ⚠️ 需 confirm_risky / 审计必记 / enabled=false 回退 seed）；`port.Context.TaskSchema` 会话快照（Resume 重放按执行时 schema——快照隔离）；运行时读侧（match/query）切 `effectiveSchemaOf`；8 个新端点；前端元数据页字段合同/装配描述受控编辑器（保存自动 check、影响面确认弹窗、版本历史 diff 抽屉、source 徽标 + 回退到内置、导出按钮）。门禁与真机验收全过：改 Prompt 预览即时变化、枚举收窄拦截并列出影响数据集、删 override 回内置（版本历史保留）、审计四连落库；浏览器实测编辑→检查→保存→徽标→预览全链路。

### 后端

1. **迁移 ×2**：`metadata_registry` + `metadata_audit`（表结构见 METADATA §4.3/§6）。
2. **port/repo.go** 增 `MetadataRepo`；`infra/repository/metadata_repo.go` 实现（照 task_repo 模式）。
3. **`logic/schema_compat.go`（新，纯函数）**：兼容规则引擎（METADATA §4.4 规则表）——输入旧/新 schema + 存量数据集清单，输出逐条判定 + 影响面（哪些数据集需重建/重嵌）。
4. **指纹盐**：`FingerprintOf` 纳入向量相关 schema 摘要（InKey 字段集 + InVector 角色 + 截断参数的哈希），解决 InVector 变更不重嵌的已知缺陷（METADATA §4.4）。
5. **`app/metadata.go` 扩展**：合并加载器（启动 seed → override → effective，进程内缓存 + 写失效）；写路径 `UpdateSchema` / `UpdateProfile`（版本递增 + check 守卫 + 审计）；`Export` / `Import`。
6. **httpgin**：`POST /schemas/:type/check`、`PUT /schemas/:type`、`PUT /profiles/:type`、`GET /export`、`POST /import`。
7. 旧查找函数（`SchemaOf`/`profileFor` 等）背后切到 effective 视图——**运行时仍进程内调用，签名不变**。

### 前端

- 元数据页：字段表格 → 受控编辑器（保存前自动 check，不兼容标红 + 影响面说明 + 二次确认）；版本历史 diff 视图；source 徽标（builtin/overridden）+ 「回退到内置」按钮；导出按钮。

### 测试

- 兼容引擎全规则单测（每行规则 × 兼容/不兼容用例）；
- 指纹盐：InVector 变更后 unchanged 条目正确转 update；
- 写路径：版本递增、审计落库、enabled=false 回退 seed；
- 快照隔离验证：改 schema 后新建任务生效、存量任务（快照）与进行中任务（会话重放）不受影响。

### 验收标准

- 改 Prompt → 元数据页预览即时变化，新任务分析提示词变化，存量任务重放不受影响；
- 枚举扩值放行、收窄被拦截且列出影响数据集；
- 删 override → 回到内置定义。

### 风险与回退

- 缓存失效遗漏 → 运行时读旧定义：写路径与加载器同进程收口，单测覆盖「写后立即读」；
- 兼容规则误判：check 是提示不是硬拦截的项（⚠️ 类）默认放行但记录审计，❌ 类硬拦截。

---

## M4 · 工作流定义外置 + 新任务类型向导（与产品第四波合流）

**目标**：工作流定义从代码字面量迁入元数据存储（kind=workflow）；前端提供「新任务类型向导」（步骤链编排 + schema 字段定义 + 指令头填写 + 绑定声明）。

要点：

1. `TaskManager.Create` 从 effective 定义快照（快照机制不变）；StepKind 仍是封闭集合——向导只能编排既有 kind，**新 kind 执行器仍是代码开发**（决策二红线）；
2. 向导产出的定义先以 disabled 入库，人工验证后启用；
3. 与第二波 Bug 链路对齐：bug_import 作为第一个「经元数据注册」的任务类型落地，验证范式。

---

## 执行守则（每波通用）

1. **门禁**：`make test` + `make build` 全绿才可交付；涉及 agent/数据集链路的波次（M2/M3）必须真机验收主链路；
2. **架构围栏**：新文件遵守依赖白名单（httpgin→app；app→port/domain；domain 仅标准库）；DTO 全部 app 层；
3. **文档同步随波交付**：每波落地时同步 README / PRODUCT / HANDOVER 对应章节（M1 的任务清单里已列）；
4. **单波单 PR**：每波一个可 revert 的完整交付，禁止跨波半成品合并；
5. **红线不松动**（PRODUCT §8.4）：读工具自主/写工具把关、五态状态机、注册表制、数据不外写——元数据模块任何能力不得绕过。
