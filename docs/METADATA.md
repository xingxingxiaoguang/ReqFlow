# ReqFlow 元数据管理模块设计

> 状态：M1~M3 已落地（注册点收敛 + 目录/预览 + 草稿字段袋化 + 存储与受控编辑；交付记录见 [METADATA_PLAN.md](./METADATA_PLAN.md)），M4（工作流定义外置 + 向导）待与产品第四波合流。产品定位与方向性决策见 [PRODUCT.md](./PRODUCT.md)。
> 本文回答五件事：为什么做、管什么不管什么、怎么存、怎么对外暴露、什么永远不能动。

## 1. 背景与问题

平台已大量采用元数据驱动（数据集 schema、工作流注册表、agent 装配描述），但有四个结构性问题：

| 问题 | 表现 |
|------|------|
| **散落** | 一个任务类型的完整定义分散在 4~5 个注册点、3 个包：`app/workflow.go`（步骤链）、`model/dataset_schema.go`（字段合同 + 任务→数据集映射）、`app/analyze_profile.go`（装配描述）、`app/tools/write.go`（写入绑定）。看懂一个任务类型要横跨四个文件 |
| **双源漂移** | priority/type_id 枚举在 schema（`FieldSpec.Enum`）与 `logic/draft.go` 白名单各定义一份；默认值（Medium/story/8h）在 `FieldSpec.Prompt` 文案与 `NormalizeDraft` 代码里各写一份——改 schema 不改代码即静默漂移 |
| **无目录** | AnalyzeProfile 完全没有对外暴露；schema / workflow 各有一个孤立端点（`/datasets/schemas`、`/workflows`），无统一浏览入口，提示词只能靠读代码想象 |
| **扩展仍靠代码** | 「三注册」范式虽立，但注册点分散 + 草稿管线 requirement 定型（`DraftItem` struct + task_items 物理列），新任务类型每次仍是一次贯穿式代码开发 |

目标：**统一管理可对外暴露的元数据（定义层），让「看懂、校验、调整定义」不需要读代码；「执行」永远留在代码里。**

## 2. 业界方案对照与取舍

| 方案 | 核心做法 | 本模块借鉴 |
|------|---------|-----------|
| Confluent Schema Registry | schema = 带版本的注册产物（subject + version），修改做**兼容性检查**（加字段放行 / 删字段·收窄枚举拦截），REST API 读开放写守卫 | §4.4 版本与兼容规则；写路径守卫 |
| Drupal Field API / Salesforce Metadata API (DX) | **代码出默认（seed）→ 数据库存覆盖（override）→ 运行时读合并视图（effective）→ 导出文件回 VCS** | §4.1 分层真相源；导入导出 |
| DataHub / Apache Atlas / OpenMetadata | 统一元数据实体模型 + **一个目录 UI** 浏览全部资产，而非每资产一个管理页 | §5.2 前端「元数据」tab 的目录形态 |
| Backstage Software Catalog | 注册一次、处处可发现的资产目录（kinds + 关系） | 任务类型聚合视图（§4.2）——一个 API 看全一类任务 |
| Temporal ↔ BPMN/Argo 光谱 | 执行器代码化（Temporal）↔ 定义全外置（BPMN）。业界共识：**执行留代码、定义成可检视产物** | 印证 PRODUCT §4 决策二；第四波「工作流定义外置」= Argo 式定义外置，本模块是其载体 |
| JSON Schema | 字段合同的标准交换格式（工具 Spec 已在用） | FieldSpec 的序列化/校验格式基础 |

**取舍结论**：目录形态（Data Catalog）+ 分层真相源（Drupal/Salesforce）+ 版本兼容守卫（Schema Registry）三者的组合。不做通用元数据平台（无自定义实体类型、无血缘图谱）——平台只有「任务类型」一族资产，够用即可。

## 3. 辖域：管什么、不管什么

### A 层 · 已是声明式数据，直接纳入管理

| # | 资产 | 现位置 | 现有暴露 |
|---|------|--------|---------|
| A1 | 数据集 Schema（字段合同：类型/枚举/必填/可筛/向量角色/主键/提取说明 Prompt） | `model/dataset_schema.go` | GET `/datasets/schemas` |
| A2 | 工作流定义（步骤链 + Kind + Deps） | `app/workflow.go` | GET `/workflows` |
| A3 | agent 装配描述 Profile（指令头 Role / 单发示例 Example） | `app/analyze_profile.go` | **无** |
| A4 | 写入工具绑定（工具名 + schema 绑定声明） | `app/tools/write.go` | **无** |

### B 层 · 信息是元数据但被硬编码在行为代码，抽取为数据

| # | 资产 | 现状 | 目标形态 |
|---|------|------|---------|
| B1 | 草稿默认值与白名单 | `logic/draft.go` 硬编码 `prioAllowList`/`typeAllowList` + 默认值 | `FieldSpec.Default` + 复用 `Enum`，消灭双源 |
| B2 | 任务类型↔数据集类型映射 | `model.DatasetTypeOfTask` switch + profile.Schema 二次绑定 | 并入任务类型聚合定义（§4.2），一处声明 |
| B3 | 匹配策略参数 | `match.duplicate_threshold` 全局一份（已知误报技术债） | 按数据集类型可配，进元数据 |
| B4 | 提示词渲染措辞 | `app/prompt.go` 渲染器埋着 requirement 语境文案（「每个工作项草稿」「待分析文档」） | 文案模板随 profile 声明 |

### C 层 · 永远留代码（目录中只读展示）

- StepKind 执行器（`runner.go` 分发的有类型执行逻辑）；
- `WriteSpec.Normalize` 等函数指针、四件套工具实现、LLM 协议适配器。

**红线（继承 PRODUCT §8.4）**：元数据模块管「定义」，不管「执行」——StepKind 封闭集合、执行器注册、写操作人工把关，一概不因本模块松动。模块化之后新增任务类型的正确路径仍是「注册定义 + 复用/新增 kind 执行器」，只是「注册定义」从改 5 处代码变成改 1 处（最终是填一份元数据）。

## 4. 目标架构

### 4.1 分层真相源（source 三态）

```
code seed（Go 内置默认，随二进制发布）      ← M1 全部在此层
        ↓ 被覆盖
DB override（metadata_registry 表，受控编辑） ← M3 引入
        ↓ 合并
effective（运行时视图：seed ∪ override）     ← 进程内供给，不经 HTTP
```

- **运行时供给路径不变**：`SchemaOf` / `WorkflowOf` / `profileFor` 等进程内函数继续是运行时唯一入口，M3 起它们背后改为读 effective 合并视图（启动加载 + 写入失效重载，缓存进程内）。**绝不引入「运行时走 HTTP 取元数据」**。
- **导出/导入**（DX 语义）：effective 视图可导出 YAML/JSON 回 git；DB 条目可删除回退到 seed。
- 每个元数据条目对外携带 `source: "builtin" | "overridden"`，前端如实徽标。

### 4.2 任务类型聚合定义（TaskTypeDefinition）

目录的核心实体不是散装的四份定义，而是**一个任务类型的聚合视图**：

```go
// app/registry.go —— 唯一注册点（M1 落地）
type TaskTypeDefinition struct {
    Type        string                    // requirement_import | …
    Workflow    model.Workflow            // 步骤链 + 依赖声明（A2）
    DatasetType string                    // 产出数据集类型（B2 收敛点）
    Schema      func() model.DatasetSchema // 字段合同（A1）
    Profile     AnalyzeProfile            // 指令头/示例/写入绑定（A3+A4）
}
func TaskTypes() []TaskTypeDefinition
func TaskTypeOf(typ string) (TaskTypeDefinition, bool)
```

落地方式：`app/registry.go` 聚合现有四处的定义；旧的查找函数（`WorkflowOf` / `AnalyzeProfileOf` / `DatasetTypeOfTask` / `DefaultWriteSpec`）改为薄委托，**存量调用零改动**。依赖方向合法：app → domain（model 保持原样，被聚合引用）。

这是「新增任务类型 = 一处注册」的第一步：M1 后代码扩展从改 5 处收敛为改 1 处；M3 后部分定义（Prompt/枚举/默认值）可经元数据页调整；M4 后定义整体外置。

### 4.3 元数据存储（M3）

```sql
-- migrations/NNNN_metadata_registry.up.sql
CREATE TABLE metadata_registry (
    id         UUID PRIMARY KEY,
    kind       TEXT NOT NULL,        -- dataset_schema | analyze_profile | workflow(M4) | match_policy
    key        TEXT NOT NULL,        -- 数据集类型 / 任务类型
    version    INT  NOT NULL,        -- 同 key 递增，版本历史同表保留
    payload    JSONB NOT NULL,       -- 该版本的完整定义
    enabled    BOOLEAN NOT NULL DEFAULT TRUE,
    created_by TEXT NOT NULL DEFAULT '',   -- 第三波认证前为空/本机标识
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (kind, key, version)
);
-- effective = 每个 (kind,key) 的 max(version) 且 enabled
```

- **版本历史不删**：数据集只存 `SchemaVersion` 数字，目录必须能查回旧版定义（现状 `Schemas()` 只有最新版，是缺口）。
- 审计独立小表 `metadata_audit`（who/when/kind/key/from_version/to_version/summary），写路径必记。
- **落地偏离记录（M3）**：payload 实现为 TEXT（JSON 文本）而非上表的 JSONB——与 dataset_items.fields / 0008 的全库「TEXT 存 JSON」决策一致（GORM 字符串直写免类型转换，无库内查询需求）；「回退 seed」实现为写入 enabled=false 的最新版（版本历史保留），而非物理删除。

### 4.4 版本与兼容规则（写守卫的核心）

保存 schema 修改时按规则表判定，不兼容项必须显式确认影响面（哪些存量数据集需重建）：

| 变更 | 判定 | 理由 / 处置 |
|------|------|------------|
| 新增可选字段（非必填、不进 InKey） | ✅ 兼容 | 存量条目按空值处理 |
| 新增必填字段 | ⚠️ 条件 | 仅对新写入生效；建议先以可选过渡 |
| 删除字段 / 改字段类型 / 改 Key | ❌ 不兼容 | 存量 fields JSON 与读侧（表格/筛选/向量组装）断裂 |
| 枚举扩值 | ✅ 兼容 | 新值可写，旧条目仍合法 |
| 枚举收值 | ❌ 不兼容 | 存量条目可能出现越界值 |
| 改 Label / Prompt / 示例文案 | ✅ 兼容 | 纯展示与提示词层 |
| InKey 变更 | ❌ 不兼容 | item_key 语义漂移 → upsert 基准失效 → 产生重复条目 |
| InVector 变更 | ⚠️ 需重嵌 | 见下方已知问题 |

**已知问题（现状缺陷，M3 必须解决）**：`logic.FingerprintOf` 只哈希字段值，不含 schema 形状——InVector 角色、向量截断参数变更后，unchanged 条目会跳过重嵌，语料向量与新 schema 不对齐。处置：**指纹纳入「向量相关 schema 摘要」**（参与向量组装的字段 key + InVector 角色 + 截断参数的哈希作盐），而非完整版本号（避免改个 Label 触发全量重嵌）。

### 4.5 快照语义（已有地基，明确不改）

- **任务快照**：创建任务时工作流定义快照进 `tasks.workflow`——元数据热编辑只影响新任务，存量任务按自己的快照执行展示。此机制保持不变，是「受控编辑」安全性的来源。
- **数据集钉版本**：数据集创建时记 `SchemaVersion`，读侧按钉定版本解析；新版本只对后续写入生效。
- **会话重放**：agent 暂停续跑按会话中 toolCall 重放，绑定的 schema 与执行时一致（`WriteSpec` 快照随会话）——元数据编辑不影响进行中任务。

## 5. 对外暴露

### 5.1 API（读开放、写守卫；M1 只读，M3 开写）

| 方法 | 路径 | 说明 | 波次 |
|------|------|------|------|
| GET | `/api/metadata` | 目录总览：各资产类型 + 条目 + `source` | M1 |
| GET | `/api/metadata/task-types/:type` | 聚合视图：workflow + schema + profile + 写入绑定 + 工具清单 | M1 |
| POST | `/api/metadata/render/preview` | 提示词预览：`{task_type, special_requirements?}` → agent 系统提示词 / 首轮消息 / 单发 prompt | M1 |
| POST | `/api/metadata/schemas/:type/check` | 兼容性检查（对存量数据集 dry-run，返回规则表判定 + 影响面） | M3 |
| PUT | `/api/metadata/schemas/:type` | schema 受控编辑（版本递增 + 兼容守卫 + 审计） | M3 |
| PUT | `/api/metadata/profiles/:type` | 指令头/示例编辑（影响面 = 新任务提示词） | M3 |
| GET | `/api/metadata/export` · POST `/api/metadata/import` | effective 视图导出 YAML / 导入校验 | M3 |

既有端点（`/workflows`、`/datasets/schemas`）保留不动——存量消费方（创建入口、数据集页表格）继续使用，元数据端点是新增的规范入口。

### 5.2 前端「元数据」tab（路由 `/metadata`）

目录形态（Data Catalog 式），不做 N 个散管理页：

```
元数据
├─ 左栏：资产导航（任务类型 · 数据集 Schema · 装配 Profile）
└─ 右侧详情（以任务类型为主视图，四区）：
   ① 概览    步骤链时间线（Seq/Name/Kind 徽标/Deps 依赖声明）+ 产出数据集类型 + source 徽标
   ② 字段合同  FieldSpec 表格（key/类型/枚举值域/必填/可筛/向量角色/主键/默认值/提取说明）
   ③ 装配描述  指令头 Role（markdown）+ 单发示例（代码块）+ 绑定的写入工具
   ④ 提示词预览 三个 tab（agent 系统提示词 / 首轮文档清单 / 单发完整 prompt），
              可填 special_requirements 实时渲染——提示词调试从「改代码重启」变「所见即所得」
```

M3 在此页上扩展：字段表格 → 编辑器（保存前调 check 端点，不兼容项标红说明影响面）、版本历史 diff、导出按钮。

### 5.3 提示词预览（本模块的招牌能力）

`app/prompt.go` 的渲染器全是纯函数，`buildToolset` 支持空依赖构造——预览端点以零值依赖（空文档/空 sink/nil 交互桥）组装工具集后调用真实渲染路径。**预览 = 运行时装配的精确复现**，不存在第二套渲染逻辑（同源原则：预览与执行共用同一函数）。

## 6. 安全与审计

元数据写路径是新的攻击面/风险面，四条守卫：

1. **提示词注入面**：`FieldSpec.Prompt`、Profile.Role 的文本直接进系统提示词——M3 编辑器对这些字段做长度上限 + 禁止 `{{`/工具冒充类模式的告警；payload 严格 JSON Schema 校验（工具 Spec 上吃过非法 JSON 的亏，同类护栏，见 HANDOVER §4.5）。
2. **写守卫**：第三波认证落地前，写端点为显式管理动作（前端仅元数据页入口）+ 每次写必记审计；认证落地后挂操作校验。
3. **兼容守卫**：schema 结构性变更必须过 check 端点，不兼容项需二次确认。
4. **回退通道**：override 可删除回 seed；导出文件留档。

## 7. 与方向性决策的关系

本设计是 PRODUCT §4 决策二（半元数据驱动）的**自然延伸而非推翻**，落地时在 PRODUCT 补记：

- 「定义即数据」的范围从内置注册表扩大到可管理的元数据资产，但**执行器仍是有类型的 Go 代码**——半元数据的「半」不变；
- 决策二的约束「新增任务类型必须走注册表，禁止 `if type ==`」升级为「必须走聚合注册表（M1 起 `app/registry.go`）」；
- 第四波「自定义任务类型配置化（工作流定义外置）」以本模块为载体（M4），路径：定义外置 → 向导化 → 执行器插件仍走代码。

## 8. 非目标

- 不做通用元数据平台（自定义实体类型/血缘图谱/联邦查询）——只有任务类型一族资产；
- 不做执行器配置化——StepKind 执行器、工具实现、协议适配器永远在代码里；
- 不做元数据的多环境分发/审批流——单工作区单实例（PRODUCT §10 边界），导出文件人工分发即可；
- 运行配置（llm/embedding/parser 端点密钥等）不入本模块——那是 Settings 的辖域，本模块只管**业务定义**。

## 9. 名词表（补充）

| 术语 | 含义 |
|------|------|
| 元数据资产 | 任务类型的声明式定义构件：工作流定义、数据集 Schema、装配 Profile、写入绑定、策略参数 |
| 聚合定义（TaskTypeDefinition） | 一个任务类型的全部定义构件的聚合视图，注册表的注册单元 |
| seed / override / effective | 代码内置默认 / 数据库覆盖 / 运行时合并视图——分层真相源三态 |
| 兼容规则 | schema 修改对存量数据集影响的判定表（§4.4），写守卫的依据 |
| 提示词预览 | 按当前元数据实时渲染三段提示词（agent 系统/首轮/单发），与运行时装配同函数 |
