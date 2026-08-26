# ReqFlow

> 需求与缺陷导入工作台：上传需求文档 → AI 解析为结构化工作项 → 项目匹配与查重 → 批量建单到 PingCode 等协作平台。
> 技术栈：React 18 + Ant Design 5（前端）/ Go + Gin + GORM（后端）/ PostgreSQL 16 + pgvector（DB）。
> 发布形态：`go:embed` 单二进制，配置全部本地 YAML，代码与产物零硬编码。

📄 **文档**：[产品总纲](./docs/PRODUCT.md)（定位/功能全景/路线图） · [交接文档](./docs/HANDOVER.md)（架构/代码地图/流程/踩坑/第二波指南）

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
| llm | base_url / api_key / model / temperature / max_tokens / timeout_ms / **agent_mode** | OpenAI 兼容协议（DeepSeek/GLM/Qwen/Kimi 适用）；`agent_mode: true` 时需求分析启用 agent loop（只读工具自主查证，见 HANDOVER §12） |
| embedding | base_url / api_key / model / dimensions / batch_size | 语义匹配向量（默认 bge-m3 1024 维）；不配置则自动降级为仅精确匹配 |
| pingcode | host / client_id / client_secret / grant_type / workload_unit / import_concurrency / sync_* | 企业授权凭据；工时单位 minute/hour/day |
| match | duplicate_threshold / project_top_n | 查重阈值（默认 0.75）与项目推荐数 |
| parser | max_file_mb / mineru.* | 上传上限与 MinerU 云端 PDF 解析 |
| security | encryption_key | 第二波敏感字段入库加密密钥（64 hex） |
| workspace | name / upload_dir / demand_dir | 工作区名与数据目录 |

## HTTP API

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/parse` | multipart 上传 → SSE `progress`/`parsed`（解析确认门） |
| POST | `/api/analyze` | `{text, file_name, special_requirements}` → SSE `token(delta,phase)`/`tool`(agent 模式工具轨迹)/`complete` |
| POST | `/api/sync` | SSE 增量同步平台项目/工作项/元数据并写入向量 |
| POST | `/api/match/projects` | 项目名 → 推荐 top N（精确前置 + 语义兜底） |
| POST | `/api/match/duplicates` | 同项目查重（标题精确 / 语义阈值） |
| POST | `/api/import` | SSE 批量建单（含 `new:项目名` 自动建项目） |
| GET | `/api/records` `/api/records/:id` `/api/records/:id/source` | 导入记录、明细、原文 |
| GET | `/api/overview` `/api/projects` `/api/work-items` | 概览与已同步数据浏览 |
| GET/POST | `/api/settings` `/api/settings/test-llm` `/api/settings/test-pingcode` | 脱敏配置与连通性测试 |

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

- **✅ agent 模式已交付**（`llm.agent_mode`）：PingCode 查询能力包装为 5 个只读工具接入 `agent.Loop`，分析从单发提取升级为「分析 → 自主查证 → 终稿」；分析会话（Context）随记录落库，为 refine 微调铺路；前端分析页展示工具查证轨迹
- **Bug 处理链路**：Excel 导入（xlsx 行级解析已就绪）→ 编号/语义双层匹配需求（top3 人工确认）→ P0~P3 批量 LLM 定级 → 确认后同步缺陷到平台（关联关系写入描述；PingCode 6.13.5 暂无关联 API）
- PingCode OAuth 用户授权（TokenSource 扩展点）、多协作平台（PlatformClient 接口）、refine 微调会话（AgentContext 载体已落库）、自定义属性映射
