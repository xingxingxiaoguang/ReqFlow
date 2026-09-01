# ReqFlow

> 面向业务的无代码 AI 知识库平台：把零散的文档资料清洗成结构化知识，一键建立精准 + 语义混合检索，直接支撑搜索与智能问答。
> 技术栈：React 18 + Ant Design 5（前端）/ Go + Gin + GORM（后端）/ PostgreSQL 16 + pgvector + OpenSearch。
> 发布形态：`go:embed` 单二进制，配置全部本地 YAML，自带依赖编排与开箱种子数据。

📄 **文档**：[产品总纲](./docs/PRODUCT.md)（定位/功能全景/路线图） · [交接文档](./docs/HANDOVER.md)（架构/代码地图/流程/踩坑） · [技术债台账](./docs/DEBT.md)

## v1.0.0 交付范围

### 核心业务流

1. **数据清洗入库**（任务管理页发起）：上传文件 → AI 按抽取规则把原文结构化 → 确定性清洗与 Schema 校验 → 人工审核门 → 原子发布进数据集。支持按文件拆分为多个独立任务，单个文件失败不影响其他文件。
2. **检索与问答**（数据管理页发起）：数据集上一键创建索引任务，构建精准（BM25）+ 语义（向量）混合检索并重排序；数据追加后再次建索引即可增量补齐。数字大脑「查询分析」可代做范围确认、多步检索与召回调优。

### 操作入口

| 页面 | 能做什么 |
|------|----------|
| 数据管理 | 创建数据集（字段结构、业务唯一键、索引规则）；字段结构 / 抽取规则 / 索引规则均可**预览**与**删除**（被数据或任务产物引用时自动防护）；数据集「索引」抽屉内直接建索引、查看快照列表、按规则筛选、索引落后提醒、删除过期快照；数据浏览与混合检索 |
| 任务管理 | 发起数据清洗任务（固定流程，抽取规则决定字段结构、数据集自动对齐）；任务目录与详情：进度、模型运行面板、人工审核（通过/修改/剔除）、失败重试 |
| 数字大脑 | 平台只读助手：解答平台操作问题、查询流程 / 任务 / 数据。内置两个 Skill——**平台指南**（/platform-guide）与**查询分析**（/query-analysis，多步搜索，召回不理想时提取原文关键词与关键语义重搜） |
| 归档管理 | 数据集归档与原样恢复 |

### v1 明确收敛（不在交付范围）

- 自由流程编排：流程固定为两条内置流程（数据清洗入库 / 建立检索索引），流程设计器与流程归档恢复已隐藏；抽取 / 索引规则在创建任务时按数据集字段结构选择，而非写死在流程里。
- 数据集用途统一为「用于搜索和智能问答」，不再提供多分类选择。
- 归档管理仅保留数据集；流程与任务的归档恢复不开放。

### 开箱种子（首次启动自动创建，全部幂等，同名跳过）

- 两条固定流程；示例知识库 **DH1功能&需求知识库** 四件套（字段结构「HD1-功能原文」、抽取规则「DH1文档数据清洗」、索引规则「HD1-语义索引」、空数据集）；数字大脑两个内置 Skill。
- OpenSearch 物理索引无需预建：第一次执行索引任务时按（数据集, 规则）自动创建，增量构建自动自愈。

## 快速开始（业务交付：一键启动）

```bash
./scripts/start.sh        # 或 make start
```

脚本会依次：启动依赖容器（PostgreSQL+pgvector、OpenSearch）→ 等待就绪 → 构建单二进制 → 运行于 :8080。打开 http://localhost:8080 即可使用。

- 前置：Docker、Go、pnpm；Linux 需 `sudo sysctl -w vm.max_map_count=262144`（OpenSearch 要求）。
- 首次启动自动生成 `config.yaml`，并**自动完成数据库迁移与种子数据**（见上文交付范围）。
- LLM 能力（文件抽取、审核辅助、数字大脑）需要编辑 `config.yaml` 填写 `llm.api_key` 与 `embedding.api_key` 后重启；启动脚本会检测并提醒。

配置加载优先级：`-config` 指定路径 > `$REQFLOW_CONFIG` > `./config.yaml`；任何配置项都可用 `REQFLOW_` 前缀环境变量覆盖（如 `REQFLOW_LLM_API_KEY`）。

## 配置项一览

| 分组 | 键 | 说明 |
|------|-----|------|
| server | port / log_level / log_format | HTTP 端口、日志级别（debug~error）、日志格式（text/json） |
| database | dsn / auto_migrate / retry_* | PG 连接串；启动自动迁移 |
| worker | concurrency / lease_seconds / poll_interval_ms / recovery_interval_ms / reconcile_limit | 持久化流程任务协程池；单实例默认并发 6，可由 `REQFLOW_WORKER_*` 覆盖 |
| llm | base_url / api_key / model / temperature / max_tokens / timeout_ms / agent_max_iterations | OpenAI 兼容协议（DeepSeek/GLM/Qwen/Kimi 适用）；抽取与分析节点统一运行 Agent loop，agent_max_iterations 是迭代安全阀 |
| embedding | base_url / api_key / model / dimensions / batch_size / rerank_* | 语义向量与 SiliconFlow rerank 共用供应商凭证；默认 bge-m3 1024 维 + bge-reranker-v2-m3 |
| opensearch | base_url / username / password / index_prefix / timeout_ms | BM25 索引与检索；混合模式的词法检索后端 |
| parser | max_file_mb / mineru.* | 上传上限与 MinerU 云端 PDF 解析（docx / txt / md 走内置解析，无需配置） |
| security | encryption_key / encryption_key_file | 敏感字段入库加密密钥（64 hex，留空自动生成本机密钥） |
| workspace | name / upload_dir / demand_dir | 工作区名与上传、存档目录 |

## 开发简介

### 架构（四层，依赖只允许向内）

```
cmd/reqflow      组装点：读配置 → 构造 infra 各实现 → 注入 app 用例 → 挂 httpgin

internal/infra   外层一体（基建 + 三方客户端 + 仓储 + HTTP 路由）
  config/ database/ log/ crypto/ repository/ llm/ embedding/ opensearch/ parser/ httpgin/

internal/app     V2 用例编排：platformagent / pipeline / orchestrator / retrieval / catalog
internal/port    出站接口契约：仓储 / LLM / embedding / rerank / parser / BlobStore
internal/domain  实体模型 + 纯领域逻辑（Schema、Dataset、DAG、资源边界、检索）
```

**依赖白名单**（`make lint-arch` 强制）：`infra/httpgin → app`；其余 infra 实现包 → `port / domain / infra 基建`；`app → port / domain`；`port → domain`；`domain → 仅标准库`。越层 import 直接报错。

### 常用命令

```bash
make setup          # clone 后执行一次：启用 git 钩子（密钥护栏 pre-commit）
make start          # 业务一键启动：依赖容器 → 等待就绪 → 构建 → 运行 :8080
make dev            # 开发模式：后端 :8080（另开终端 make frontend 起 Vite :5173 代理 /api）
make build          # 前端构建 + embed 单二进制 → bin/reqflow
make test           # go vet + go test + 架构围栏 + 密钥扫描
make clean          # 清理 bin 与前端产物
```

### 开发须知

- 数据库迁移内嵌于二进制（`internal/infra/database/migrations`），`auto_migrate: true` 时启动自动执行；Schema/Profile 为不可变资源，不提供 PUT/PATCH，合同变化必须创建新资源。
- 人工审核只接受对校验结果的逐条 approve/edit/exclude 决定，服务端生成不可变审核集；发布通过带 attempt fencing 的 Dataset Batch 事务原子完成。
- 任务创建时冻结完整定义快照与资源边界；抽取 / 索引规则通过 `step_configs` 在任务级注入，运行期校验。
- 密钥安全护栏（四道防线）：真实密钥只放本地 `config.yaml`（已 gitignore）或环境变量；pre-commit 扫描、`make check-secrets` 仓库扫描、启动自检兜底，详见下表。

| 防线 | 机制 |
|------|------|
| 1. gitignore | `config.yaml` 及变体不入库（example 模板除外） |
| 2. pre-commit 钩子 | 暂存内容密钥扫描；`make setup` 启用（`--no-verify` 为误报逃生通道） |
| 3. `make check-secrets` | 仓库级扫描（敏感字段非空值 / 带密码 DSN），纳入 `make test` |
| 4. 启动自检 | 模板被填入真实密钥时 ERROR 告警；日志只打印密钥名称，绝不打印值 |

更多实现细节（代码地图、执行器语义、踩坑记录）见 [交接文档](./docs/HANDOVER.md)。
