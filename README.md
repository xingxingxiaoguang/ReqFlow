# ReqFlow

> 面向异构业务数据的无代码 AI 管线：可视化定义流程，把原始资料清洗为数据集，建立精准 + 语义索引，再由可追溯任务完成查询、分析与制品生成。
> 技术栈：React 18 + Ant Design 5（前端）/ Go + Gin + GORM（后端）/ PostgreSQL 16 + pgvector（DB）。
> 发布形态：`go:embed` 单二进制，配置全部本地 YAML，代码与产物零硬编码。

📄 **文档**：[产品总纲](./docs/PRODUCT.md)（定位/功能全景/路线图） · [交接文档](./docs/HANDOVER.md)（架构/代码地图/流程/踩坑/第二波指南；元数据系统不变量并入其中 §3.4） · [技术债台账](./docs/DEBT.md)

## 架构（四层，依赖只允许向内）

```
cmd/reqflow      组装点：读配置 → 构造 infra 各实现 → 注入 app 用例 → 挂 httpgin

internal/infra   外层一体（基建 + 三方客户端 + 仓储 + HTTP 路由）
  config/          配置加载（YAML + REQFLOW_* 环境变量覆盖）与启动校验
  log/             slog 封装
  database/        GORM 连接 + 内嵌 SQL 迁移
  crypto/          AES-256-GCM（第二波 OAuth 令牌入库时启用）
  repository/      postgres + pgvector 仓储实现
  llm/             OpenAI 兼容 chat/completions SSE 流式客户端
  embedding/       OpenAI 兼容 /embeddings 客户端
  opensearch/      OpenSearch BM25 检索适配器
  parser/          docx(标准库 OOXML) / pdf(MinerU 云端) / xlsx(excelize, 第二波开放) / txt·md
  httpgin/         Gin 路由与 SSE handler，只调 app 用例

internal/app     V2 用例编排：platformagent / pipeline / orchestrator / retrieval / analysis / catalog
internal/port    出站接口契约：仓储 / LLM / embedding / rerank / parser / BlobStore
internal/domain  V2 实体模型 + 纯领域逻辑（Schema、Dataset、DAG、资源边界、检索与制品）
```

**依赖白名单**（`make lint-arch` 强制）：`infra/httpgin → app`；其余 infra 实现包 → `port / domain / infra 基建`；`app → port / domain`；`port → domain`；`domain → 仅标准库`。越层 import 直接报错。

## 快速开始

```bash
# 1. 数据库（Docker，PG16 + pgvector）
docker compose up -d

# 2. 配置
cp config.example.yaml config.yaml   # 首次直接运行二进制也会自动生成模板
#    按需填写 llm.api_key / embedding.api_key / opensearch.* / parser.mineru.api_token

# 3. 开发运行（后端 :8080，前端 Vite :5173 代理 /api）
make dev          # 终端 1
make frontend     # 终端 2

# 4. 发布：单二进制（前端产物 embed 进二进制）
make build        # → bin/reqflow
./bin/reqflow     # 同目录放 config.yaml 即可运行
```

配置加载优先级：`-config` 指定路径 > `$REQFLOW_CONFIG` > `./config.yaml`；任何配置项都可用 `REQFLOW_` 前缀环境变量覆盖（如 `REQFLOW_LLM_API_KEY`）。

## 配置项一览

| 分组 | 键 | 说明 |
|------|-----|------|
| server | port / log_level / log_format | HTTP 端口、日志级别（debug~error）、日志格式（text/json） |
| database | dsn / auto_migrate / retry_* | PG 连接串；启动自动迁移 |
| worker | concurrency / lease_seconds / poll_interval_ms / recovery_interval_ms / reconcile_limit | 持久化流程任务协程池；单实例默认并发 6，可由 `REQFLOW_WORKER_*` 覆盖 |
| llm | base_url / api_key / model / temperature / max_tokens / timeout_ms / agent_max_iterations | OpenAI 兼容协议（DeepSeek/GLM/Qwen/Kimi 适用）；流程中的模型节点统一运行 Agent loop，agent_max_iterations 是每个 Agent 运行的迭代安全阀 |
| embedding | base_url / api_key / model / dimensions / batch_size / rerank_* | 语义向量与 SiliconFlow rerank 共用供应商凭证；默认 bge-m3 1024 维 + bge-reranker-v2-m3 |
| opensearch | base_url / username / password / index_prefix / timeout_ms | BM25 索引与检索；Hybrid 模式的词法检索后端 |
| match | duplicate_threshold / project_top_n | 查重阈值（默认 0.75）与项目推荐数 |
| fts | ts_config | PG 全文检索分词配置（默认 simple；中文全文检索需安装 zhparser/pg_jieba 扩展后改配置） |
| parser | max_file_mb / mineru.* | 上传上限与 MinerU 云端 PDF 解析 |
| security | encryption_key | 第二波敏感字段入库加密密钥（64 hex） |
| workspace | name / blob_dir | 工作区名与内容寻址 Blob 根目录 |

## HTTP API

### V2（当前重构入口）

`/api/v2` 已提供不可变 Schema、追加型 Dataset/Batch、TaskDefinition DAG 和持久化 Task 运行时：

- `GET|POST /api/v2/agent/sessions`、`GET /api/v2/agent/sessions/:id`
- `POST /api/v2/agent/sessions/:id/messages`（SSE：回答增量、工具轨迹和终态会话）
- `POST /api/v2/schemas`、`POST /api/v2/datasets`
- `POST /api/v2/datasets/:id/batches`、`POST /api/v2/batches/:id/commit`
- `GET /api/v2/datasets/:id/items?after_seq=&through_seq=`
- `POST /api/v2/assets`（multipart `file`）、`POST /api/v2/asset-sets`
- `GET /api/v2/asset-sets/:id`、`GET /api/v2/parsed-document-sets/:id`
- `GET /api/v2/parsed-documents/:id/blocks?after_ordinal=&limit=`
- `POST /api/v2/extraction-profiles`、`GET /api/v2/extraction-profiles/:id`
- `GET /api/v2/record-draft-sets/:id`
- `GET /api/v2/transformed-record-sets/:id`、`GET /api/v2/validation-result-sets/:id`
- `GET /api/v2/approved-record-sets/:id`
- `POST /api/v2/task-definitions`、`POST /api/v2/tasks`
- `POST /api/v2/tasks/:id/start|pause|resume`
- `POST /api/v2/tasks/:id/steps/:step_id/retry|approve`
- `GET /api/v2/tasks/:id`、`GET /api/v2/tasks/:id/events`

V2 Schema/Profile 不提供 PUT/PATCH；结构或抽取合同变化必须创建新资源。`source.parse` 将内容寻址 Asset 解析为带逐文件状态的 `ParsedDocumentSet`；`document.extract` 和 `knowledge.analyze` 都通过 `internal/app/agent` 的统一运行壳执行，领域包只提供工具和领域状态。结构化结果必须通过完成工具校验后显式提交，工具或 Schema 错误会作为可纠正反馈返回模型。任务详情的“模型运行面板”读取通用 `agent_runs`，通过 SSE 实时展示思考、输出和工具链，默认折叠。`data.transform` 和 `data.validate` 完成确定性转换、业务规则与冲突校验。人工审核端点只接受对 ValidationResult 的逐条 approve/edit/exclude 决定，服务端生成不可变 `ApprovedRecordSet`，不接受客户端提供资源 UUID；`data.publish` 只消费该审核资源，并通过带 attempt fencing 的 Dataset Batch 事务原子发布。
Task 输入绑定接受具体 `resource_id`；Dataset 也可传 `resource_alias`，创建时会解析为具体 Dataset，并为 `dataset_boundary` 固化当时的 `through_seq`。

## V2 前端操作模型

```text
从空白手动编排 ─┐
                 ├─→ 发布 TaskDefinition（可复用流程蓝图）
可编辑模板起点 ─┘                  │
                                   ├─→ 选择流程 + 绑定本次资源 → 创建 Task
                                   └─→ 一个流程可派生多个互相独立的 Task
                                                      │
                                                      └─→ 运行 / 暂停 / 继续 / 审核 / 重试
```

- `/agent`：默认首页和 ReqFlow 数字大脑；会话持久化，支持停止/续聊；流程、任务、数据和 Skill 工具可独立启停，并支持用 `/` 调用纯文本 Skill。
- `/definitions`：流程定义目录；草稿与已发布流程统一展示，可进入详情、复制并编辑、创建任务或归档流程。
- `/definitions/new`：从空白自由编排、选择五种模板作为**可修改起点**，或复制现有流程形成独立草稿；发布动作只创建 `TaskDefinition`，不隐式创建任务。
- `/tasks/new?definition_id=...`：选择已发布流程、按输入端口绑定本次资源，选择“仅创建”或“创建并运行”。
- `/tasks`：任务运行目录；展示来源流程、状态和执行入口，进入详情后操作 StepRun、人工审核和重试。
- `/datasets`：数据集目录和条目详情；Schema、字段索引规则和混合检索参数在使用处就地选择或创建。

`TaskDefinition` 定义“怎么执行”，`Task` 表示“一次具体执行”。Task 只能从 `active` Definition 派生；创建时冻结完整 Definition Snapshot、资源绑定和 Dataset/Retrieval 读取边界，后续复制、修改或归档流程定义不会改变已经创建的任务。归档流程会转为 `retired`，可从归档管理恢复后继续派生新任务。

旧 `/api/tasks` 运行时、`AnalyzeService` 单发/降级路径及其前端入口已删除；TaskDefinition + StepRun 是唯一任务执行模型。
| POST | `/api/match/duplicates` | 同项目查重（标题精确 / 语义阈值） |
| GET | `/api/datasets` `/api/datasets/:id` | 数据集与条目浏览 |
| GET | `/api/datasets/schemas` | 数据集类型模板目录（新建数据集时带出字段定义） |
| GET | `/api/datasets/:id/items` | 条目查询：`q=` 语义检索 + `f[字段]=值`（`|` 分隔为 in）筛选叠加 |
| GET/POST/PUT/DELETE | `/api/platform-configs/...` | LLM、向量化、重排序、MinerU 多配置管理与单配置激活；`config.yaml` 作为只读兜底 |

## Legacy 通用数据集（已退出 V2 产品模型）

- **字段定义归属数据集**（`datasets.schema`，JSONB）：数据集是字段定义的真相源——创建任务即绑定目标数据集，字段元数据自动带出，分析提示词、写入校验、门内表格、查询过滤全按数据集自身的字段执行。类型级定义（`model/dataset_schema.go` + 元数据页）是「数据集类型模板」：新建数据集时带出初始形状，实例可受控编辑独立演进（兼容守卫防打穿存量条目）。字段可标记 `fts`（全文检索）/ `filterable`（筛选下推）/ `in_vector`（向量角色）/ `in_key`（条目主键）。
- **动态索引随 schema**：FTS 字段建表达式 GIN 索引（`to_tsvector(cfg, fields->>'k')`，中文需 zhparser/pg_jieba）、filterable 字段建表达式 btree——随 schema 受控编辑自动建删，归档回收、恢复重建。
- **条目身份**：`item_key`（schema 主键字段归一化拼接，同 key = 同一条目）+ `fingerprint`（内容哈希，相同则跳过更新与重嵌）。字段袋为原生 JSONB。
- **写入策略**（`POST /api/tasks/:id/dataset` 的 `mode`，目标 = 绑定数据集）：
  - `merge` 并入（默认）：仅插入新条目，已存在跳过
  - `upsert` 并入并更新：新条目插入，已存在按内容更新
  - `replace` 覆盖本任务此前写入的条目（同源重跑；其他来源数据不动）
- **终态可重写**：已完成的任务停留在数据集步骤时可换策略再次写入（幂等，不产生重复条目）。
- **统一查询**：字段过滤（filterable 字段 SQL 下推）+ 语义检索 / 全文检索（`/search`，tsvector 相关度排序）叠加，数据集浏览、agent 工具、后续任务输入共用。
- **归档**：任务与数据集的删除不是物理删除，而是事务性搬入独立归档表（`archived_*`，与主表同构直搬，不带索引不占检索成本）。已归档数据物理离开主表——列表、查重语料、语义检索、统计自动不再触达；归档页可查看、可原样恢复（数据集条目向量原生保留，动态索引随行内 schema 重建，任务含步骤/明细快照，恢复后可继续未走完的流程）。运行中任务与被进行中任务引用的数据集拒绝归档。

## 开发命令

```bash
make setup          # clone 后执行一次：启用 git 钩子（密钥护栏 pre-commit）
make test           # go vet + go test + 架构围栏 + 密钥扫描
make lint-arch      # 依赖方向白名单校验
make check-secrets  # 扫描 git 追踪文件中的疑似密钥
make build          # 前端构建 + embed 单二进制
```

## 密钥安全护栏（四道防线）

真实密钥**只**放两处：本地 `config.yaml`（已 gitignore）或 `REQFLOW_*` 环境变量。防止密钥随代码分享的机制：

| 防线 | 机制 |
|------|------|
| 1. gitignore | `config.yaml` / `config.*.yaml` 全部变体不入库（example 模板除外） |
| 2. pre-commit 钩子 | 暂存内容密钥扫描 + 真实配置文件名直接拦截；钩子在 `.githooks/` 入库分享，`make setup` 启用（`git commit --no-verify` 为误报逃生通道） |
| 3. `make check-secrets` | 仓库级扫描（敏感字段非空值 / 带密码 DSN），纳入 `make test` |
| 4. 启动自检 | 后端启动时若发现 `config.example.yaml` 被填入真实密钥会 ERROR 告警并提示轮换；日志只打印已加载密钥的**名称**，绝不打印值 |

检测规则与白名单（环境变量名引用、代码标识符、占位符、用户名=密码的本地 DSN）见 `scripts/secret-check.sh`。**一旦密钥已提交**：立即在对应平台轮换密钥，再用 `git filter-repo` 清理历史。
