# ReqFlow

> 需求与缺陷导入工作台：上传需求文档 → AI 解析为结构化工作项 → 项目匹配与查重 → 批量建单到 PingCode 等协作平台。
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
  pingcode/        PingCode 开放 API 客户端（企业授权 + 重试 + 分页）
  parser/          docx(标准库 OOXML) / pdf(MinerU 云端) / xlsx(excelize, 第二波开放) / txt·md
  httpgin/         Gin 路由与 SSE handler，只调 app 用例

internal/app     用例编排：parse / analyze / sync / match / import / record / settings / overview + bug(第二波占位)
internal/port    出站接口契约：repo / llm / platform / embedding / parser
internal/domain  实体模型 + 纯领域逻辑（归一化、相似度换算、宽松 JSON 恢复、名称→UUID 映射）
```

**依赖白名单**（`make lint-arch` 强制）：`infra/httpgin → app`；其余 infra 实现包 → `port / domain / infra 基建`；`app → port / domain`；`port → domain`；`domain → 仅标准库`。越层 import 直接报错。

## 快速开始

```bash
# 1. 数据库（Docker，PG16 + pgvector）
docker compose up -d

# 2. 配置
cp config.example.yaml config.yaml   # 首次直接运行二进制也会自动生成模板
#    按需填写 llm.api_key / pingcode.client_id+client_secret / embedding.api_key / parser.mineru.api_token

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
| llm | base_url / api_key / model / temperature / max_tokens / timeout_ms / **agent_mode** / agent_max_iterations | OpenAI 兼容协议（DeepSeek/GLM/Qwen/Kimi 适用）；`agent_mode: true` 时需求分析启用 agent loop（read_document / search_document / write_work_items / ask_human 四工具，见 HANDOVER §5.2）；agent_max_iterations 迭代上限默认 32 |
| embedding | base_url / api_key / model / dimensions / batch_size | 语义匹配向量（默认 bge-m3 1024 维）；不配置则自动降级为仅精确匹配 |
| pingcode | host / client_id / client_secret / grant_type / workload_unit / import_concurrency / sync_* | 企业授权凭据；工时单位 minute/hour/day |
| match | duplicate_threshold / project_top_n | 查重阈值（默认 0.75）与项目推荐数 |
| parser | max_file_mb / mineru.* | 上传上限与 MinerU 云端 PDF 解析 |
| security | encryption_key | 第二波敏感字段入库加密密钥（64 hex） |
| workspace | name / upload_dir / demand_dir | 工作区名与数据目录 |

## HTTP API

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/tasks/:id/parse` | multipart 上传 → fire-and-forget 解析步骤（进度走 SSE） |
| POST | `/api/tasks/:id/analyze` | fire-and-forget AI 分析步骤（agent 模式：读取/检索/草稿写入/问人四工具） |
| POST | `/api/tasks/:id/dialog` | 人工回答 agent 的提问（`{call_id, answer}`；ask_human 的出口） |
| POST | `/api/tasks/:id/dataset` | fire-and-forget 写入数据集：`{mode: create\|merge\|upsert\|replace, dataset_id?, dataset_name?}`（写入声明见下） |
| POST | `/api/tasks/:id/dataset/preview` | 写入预览：新增/更新/无变化/非法 分桶（不落库） |
| GET/POST/PATCH | `/api/tasks`（`/:id`、`/:id/items`、`/:id/pause`、`/:id/resume`、`/:id/complete`、`/:id/events`） | 任务生命周期 + 门内草稿 + SSE 事件流 |
| DELETE | `/api/tasks/:id` · `/api/datasets/:id` | 归档（移入独立归档表，可恢复，退出主业务循环） |
| GET | `/api/archives?kind=task\|dataset&type=` | 归档列表（任务含明细快照；数据集含条目与向量） |
| POST | `/api/archives/:kind/:id/restore` | 归档恢复到主表（数据集恢复后查重/检索语料随之生效） |
| GET | `/api/metadata` · `/api/metadata/task-types/:type` | 元数据目录：任务类型聚合定义（workflow + schema + profile + 工具清单 + source），前端「元数据」tab 数据源 |
| POST | `/api/metadata/render/preview` | 提示词预览：`{task_type, special_requirements?}` → 三段提示词实时渲染（与运行时装配同一函数） |
| POST | `/api/metadata/schemas/:type/check` | schema 兼容性 dry-run：规则表判定（✅/⚠️/❌）+ 存量数据集影响面 |
| PUT/DELETE | `/api/metadata/schemas/:type` · `/api/metadata/profiles/:type` | 受控编辑（❌ 拦截 / ⚠️ confirm_risky + 版本递增 + 审计）/ 回退内置 |
| PUT | `/api/metadata/workflows/:type`（`check` `POST` / `DELETE` 回退） | 工作流受控编辑（M4）：⚠️ 确认流 + 审计；热编辑仅影响新任务（存量按 tasks.workflow 快照） |
| POST | `/api/metadata/task-types` | 新任务类型向导：编排既有 kind + 字段合同 + 指令头 → 产物 disabled 入库（草稿，`status` 端点人工启用） |
| GET/POST | `/api/metadata/history/:kind/:key` · `/api/metadata/export` · `/api/metadata/import` | 版本历史（kind 含 workflow）/ effective 视图导出 / 导入（新类型按向导注册为草稿） |
| POST | `/api/match/duplicates` | 同项目查重（标题精确 / 语义阈值） |
| GET | `/api/datasets` `/api/datasets/:id` | 数据集与条目浏览 |
| GET | `/api/datasets/schemas` | 数据集 schema 目录（字段合同：表格/筛选/向量组装驱动） |
| GET | `/api/datasets/:id/items` | 条目查询：`q=` 语义检索 + `f[字段]=值`（`|` 分隔为 in）筛选叠加 |
| GET/POST | `/api/settings` `/api/settings/test-llm` | 脱敏配置与连通性测试 |

## 通用数据集（任务间衔接的标准化接缝）

- **Schema 注册表**（`internal/domain/model/dataset_schema.go`）：数据集类型 = 声明式字段合同（类型/枚举/必填/可筛/向量角色/主键参与）。写入校验、语义向量文档组装、明细表格渲染、条目主键全部 schema 驱动——新任务类型只需注册 schema。
- **条目身份**：`item_key`（schema 主键字段归一化拼接，同 key = 同一条目）+ `fingerprint`（内容哈希，相同则跳过更新与重嵌）。
- **写入策略**（`POST /api/tasks/:id/dataset` 的 `mode`）：
  - `create` 新建数据集（断点续跑为同集全量重建）
  - `merge` 并入：仅插入新条目，已存在跳过
  - `upsert` 并入并更新：新条目插入，已存在按内容更新
  - `replace` 覆盖本任务此前写入的条目（同源重跑；其他来源数据不动）
- **终态可重写**：已完成的任务停留在数据集步骤时可换策略再次写入（幂等，不产生重复条目）。
- **统一查询**：字段过滤（filterable 字段 SQL 下推）+ 语义检索叠加，数据集浏览、agent 工具、后续任务输入共用。
- **归档**：任务与数据集的删除不是物理删除，而是事务性搬入独立归档表（`archived_*`，与主表同构直搬，不带索引不占检索成本）。已归档数据物理离开主表——列表、查重语料、语义检索、统计自动不再触达；归档页可查看、可原样恢复（数据集条目向量原生保留，任务含步骤/明细快照，恢复后可继续未走完的流程）。运行中任务与被进行中任务引用的数据集拒绝归档。

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

## 第二波路线图（扩展点已预留）

- **✅ agent 模式已交付**（`llm.agent_mode`）：分析从单发提取升级为 pi 式工具驱动——`read_document` 分批阅读（行号分页 + 续读提示）/ `search_document` 正则检索 / `write_work_items` 分批产出草稿（同 key 覆盖可修订，逐条校验回执）/ `ask_human` 关键决策点人工交互（SSE 弹窗 + HTTP 应答）；分析会话（Context）随记录落库，暂停续跑时草稿从会话重放；系统提示词的工具指南从实际工具集组装，不漂移；前端分析页展示工具轨迹与人工交互弹窗
- **Bug 处理链路**：Excel 导入（xlsx 行级解析已就绪）→ 编号/语义双层匹配需求（top3 人工确认）→ P0~P3 批量 LLM 定级 → 确认后同步缺陷到平台（关联关系写入描述；PingCode 6.13.5 暂无关联 API）
- PingCode OAuth 用户授权（TokenSource 扩展点）、多协作平台（PlatformClient 接口）、refine 微调会话（AgentContext 载体已落库）、自定义属性映射
