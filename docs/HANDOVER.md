# ReqFlow 交接文档

> 面向下一个接手开发的同学。读完本文 + [PRODUCT.md](./PRODUCT.md)，你应当能独立完成开发、部署、排障与第二波迭代。
> 写作基准：提交 `8817e36`（第一波交付 + 安全护栏），代码约 5400 行 Go + React 前端。

---

## 1. 项目现状快照

- **能跑什么**：上传需求文档（docx/pdf/md/txt）→ 解析确认门 → LLM 流式分析 → 项目匹配/查重 → 批量导入 PingCode；PingCode 数据增量同步；导入记录恢复。Bug 链路（Excel→匹配→定级）为第二波，前端有路线图占位页。
- **怎么跑起来**：见 §2 五分钟上手。
- **仓库**：`/Users/xxxg/demo/ReqFlow`，main 分支两个提交（`41c5e6b` 功能、`8817e36` 护栏）。
- **出身**：本项目参考同机 `PingCraft`（TS 全栈，路径 `/Users/xxxg/demo/PingCraft`）的核心功能用 Go+React 重写。大量健壮性设计移植自它，对照表见 §8——遇到「这段代码为什么这么写」，先查 PingCraft 对应实现是否踩过坑。

## 2. 五分钟上手

```bash
cd /Users/xxxg/demo/ReqFlow

# 0) 一次性初始化（启用 pre-commit 密钥护栏）
make setup

# 1) 数据库（Docker，PG16 + pgvector；镜像拉取见 §10 环境坑）
docker compose up -d

# 2) 配置（首启会自动生成模板；或手动 cp config.example.yaml config.yaml）
#    至少填 llm.api_key；要同步/导入则填 pingcode.client_id/client_secret
vim config.yaml

# 3) 开发模式（两个终端）
make dev        # 后端 :8080（tsx 无，直接 go run；热重载未配，改代码需重启）
make frontend   # 前端 Vite :5173，/api 代理到 :8080

# 4) 发布验证
make build      # → bin/reqflow（约 28MB 单二进制，前端已 embed）
./bin/reqflow   # 同目录放 config.yaml 即可跑
```

**质量门禁**：`make test` = `go vet` + `go test` + 架构围栏（`scripts/arch-check.sh`）+ 密钥扫描（`scripts/secret-check.sh`）。提交代码前必过。

## 3. 架构与依赖铁律（最重要的一节）

四层架构，**依赖只允许向内**，业务层永远不知道 infra 的存在：

```
cmd/reqflow      组装点：读配置 → 构造 infra 实现 → 注入 app 用例 → 挂 httpgin。
                 全项目唯一知道所有具体实现的地方（cmd/reqflow/main.go）

internal/infra   外层一体（基建 + 三方客户端 + 仓储 + HTTP 路由）
internal/app     用例编排（parse/analyze/sync/match/import/record/settings/overview/browse + bug 占位）
internal/port    出站接口契约（repo/llm/platform/embedding/parser 五个文件）
internal/domain  实体模型 + 纯领域逻辑（零三方依赖，仅标准库）
```

**依赖白名单**（arch-check.sh 强制，越界直接 fail）：

- `cmd → 全部`
- `infra/httpgin → app`（且**只能** import app——不摸 port/domain/其它 infra，HTTP 层的入参出参全部用 app 层 DTO，如 `app.DraftInput`、`app.AnalyzeDelta`）
- `infra/{repository,llm,embedding,pingcode,parser} → port, domain, infra/{config,log,database,crypto}`
- `app → port, domain`；`port → domain`；`domain → 仅标准库`（`go list -deps` 检查）

**为什么这么分**：infra 与 adapter 合并为一层是明确决策（不会大规模换基建，拆开只剩目录成本）；「httpgin 只准调 app」保住 handler 不直摸仓储；接口定义在 port 层（集中契约，好导航），因此「app 不 import infra」主要靠 arch lint 而非编译器——**这是刻意的，改架构时保持 lint 规则同步更新**。

## 4. 代码地图

```
cmd/reqflow/
  main.go            组装点：配置→DB→迁移→infra→app→http；启动自检（密钥名单/模板泄漏）
  static_embed.go    //go:build embed：go:embed dist，SPA 直出（注意：不用 http.FileServer，见 §10）
  static_dev.go      //go:build !embed：开发模式空实现（前端由 Vite 提供）

internal/domain/
  model/model.go     Project/WorkItem/Meta*/DraftItem/ImportRecord(Item) + 状态常量
  logic/             全部纯函数 + 单元测试（改这里不需要起任何服务）
    normalize.go       归一化精确匹配用（全角→半角含 U+3000、小写、空白压缩）
    similarity.go      余弦距离 0-2 → 分数 0-1
    lenientjson.go     LLM 宽松 JSON 恢复三级降级（剥围栏→截取[...]→修复截断数组）
    draft.go           LLM 输出白名单归一化（priority 3 档/type 5 类/状态占位词清洗）
    mapping.go         名称→UUID 映射（优先级别名词典、类型组名、工时单位换算、ID 形状检测）
    identifier.go      新建项目标识生成
    syncdiff.go        增量同步比对（时间戳/内容/归档重现三条件）

internal/port/
  repo.go            ProjectRepo/WorkItemRepo/MetaRepo/ImportRepo + 向量 DTO（ProjectVector 等，Score→Distance）
  llm.go             LLMClient（Stream/Complete/Ping）+ pi 式消息模型（Context/Message/内容块/ToolSpec
                     全可 JSON 序列化——Context 即会话，refine/落库以它为单位）
                     + 流事件协议（start/text_delta/thinking_delta/toolcall_*/done/error）
  platform.go        PlatformClient（平台无关 DTO + 全部平台操作）
  embedding.go       Embedder（Available() 驱动降级）
  parser.go          DocParser

internal/app/        用例层；全部依赖构造注入，进度用回调上报（httpgin 转 SSE）
  agent/loop.go      ⭐ pi 式 agent loop 骨架（Tool 接口 + 自然终止 + MaxIterations 安全阀 +
                     length 截断整批 fail + ToolOutput 的 output/details 拆分）；v1 未接入
                     主链路，analyze 仍单发直调——引入工具（如 PingCode API 包装）后切换
  sync.go            三态增量同步 + 元数据（并发3）+ 向量写入（批次/延迟）
  analyze.go         流式分析编排：prompt 渲染→流式→宽松恢复→非流式回退→落库
  prompt.go          需求分析 Prompt 模板（占位符 {current_time}/{special_requirements_section}/{text}）
  match.go           两层匹配（项目推荐 topN / 同项目查重阈值）
  import.go          批量导入（new:建项目/元数据映射/assignee 三级解析/工时换算/并发建单）
  parse.go browse.go record.go settings.go overview.go
  bug/doc.go         ⭐ 第二波 bug 域完整设计（schema/用例流/关联落地方式）——做第二波先读它

internal/infra/
  config/config.go   YAML+env 覆盖（反射走 env tag）、Validate（dsn 硬校验/其余降级 warns）、
                     FilledSecrets/CheckExampleLeak（安全自检）；example.yaml 内嵌（首启生成模板）
  log/ database/ crypto/
    database.go      GORM 连接（重试）+ 手写迁移器（内嵌 SQL，schema_migrations 表，幂等）
    migrations/      0001_init.up/down.sql（9 张表 + pgvector HNSW 索引）
  repository/        四个仓储实现（GORM + pgvector；Raw SQL 做向量检索返回 Distance）
  llm/               双协议适配器（均移植自 pi，见 §8.5）：client.go 工厂按 llm.provider 分发
                     openai.go——OpenAI 兼容 /chat/completions（reasoning 三字段防重复、
                       tool_calls 流式增量聚合、缺 finish_reason 时推断）
                     anthropic.go——Anthropic Messages 协议（SSE 状态机、thinking 签名回放、
                       连续 toolResult 合并为单条 user 消息）
  embedding/         OpenAI 兼容 /embeddings（批量、按 index 归位）
  pingcode/          client.go（token 缓存 5min 提前量/重试/错误类型）projects.go workitems.go（分页双停止）
  parser/            parser.go（分发+docx 标准库 zip+XML）mineru.go（四步云端解析）xlsx.go（行级解析，第二波用）
  httpgin/           server.go（路由表）sse.go heartbeat.go handler_*.go（parse/analyze/sync/import/match/records/browse/misc）

web/                 React 18 + AntD5 + ProLayout + TanStack Query + Zustand + react-router
  src/stores/importWizard.ts   向导跨页状态（SSE 消费都封装在 store action 里，页面只渲染）
  src/api/sse.ts               POST SSE 手写解析（fetch+ReadableStream 按空行拆帧）
  src/pages/import/            四阶段向导（Upload→Review→Analyzing→Result），URL 驱动
  其余页面：Overview/Sync/Records/Settings/Bugs(占位)
```

## 5. 数据模型（migration 0001）

| 表 | 要点 |
|----|------|
| `projects` / `work_items` | 同步缓存。PK=平台 ID；`remote_updated_at TEXT`（原样字符串做增量比对）；`is_archived` 软删；**`embedding vector(1024)` 行内列** + HNSW 余弦索引（`WHERE NOT is_archived` 部分索引） |
| `work_item_types/states/priorities` | 名称→UUID 映射底料；复合 PK（含 project_id） |
| `work_item_properties` | 建了表但第一波不拉取（扩展点） |
| `import_records` + `import_record_items` | 分析与导入留痕；items 即草稿快照，导入结果逐条回写（status/pingcode_id/identifier/error）；`agent_context`（迁移 0002）= 分析会话 Context 的 JSON 文本，refine 微调载体 |

**向量维度是硬约束**：迁移固定 1024（bge-m3），`config.Validate()` 会拦截 `embedding.dimensions != 1024`。换向量模型 = 改迁移 + 清库重建 + 改配置，不是改个数字就行。

**增量比对规则**（`logic/syncdiff.go`）：远端更新时间变了 / 标题描述变了 / 本地归档项重新出现 → 更新并重建向量；本地有而远端没有 → 软归档 + 删向量。

## 6. 核心流程详解（数据怎么流的）

### 6.1 需求分析主链路（F1）

```
前端 Upload 页 → store.uploadAndParse(file)
  POST /api/parse (multipart) → 存 upload_dir → parser 按后缀分发：
    txt/md 直读；docx=zip 内 word/document.xml 逐 <w:p> 拼接 <w:t>（标准库）；
    pdf=MinerU 四步：申请预签名链接→PUT 裸字节（不带 Content-Type！）→轮询(5s/10min，
      进度经 SSE 透传)→下载 zip 取 full.md
  → SSE: progress / parsed{file_name,text}
→ 用户在 Review 页预览/编辑全文 + 填额外要求 → startAnalyze()
  POST /api/analyze {text,file_name,special_requirements}
  app/analyze: 渲染 prompt（strings.ReplaceAll 三占位符，不用模板引擎避开 JSON 大括号）
  → llm.StreamChat: SSE 增量，delta.reasoning_content→thinking 相位（只展示），
    delta.content→answer 相位（进解析缓冲）→ SSE token{delta,phase} 透传前端双区滚动
  → 解析降级链: json.Unmarshal 标准解析 → logic.ExtractJSONArrayLenient（剥围栏→截取→修复截断）
    → 流彻底失败时 llm.Chat 非流式重调一次（同一 prompt）
    → 流中断但已有部分输出时优先宽松恢复部分结果
  → logic.NormalizeDrafts 白名单归一化 → demand 原文存档（demand_dir）→ 记录+明细落库
  → SSE complete{record_id,items} → 前端自动进 Result 页
→ Result 页: 自动 POST /api/match/projects（项目名去重→精确层→语义层）→ 选项目（或 new:创建）
  → 自动 POST /api/match/duplicates → 行内查重徽标 → 用户编辑行（title/priority/type/hours/assignee）
  → 点导入: POST /api/import → SSE 逐条进度 → 完成后记录终态回写
```

### 6.2 同步（F4）

`POST /api/sync` → `app/sync.Run`：ListProjects → 逐项目元数据（types→每 type states、priorities，goroutine 信号量限 3）→ 项目 diff + 向量（`Project: name
Description: desc` 格式）→ 逐项目 ListWorkItems（分页双停止：本页不满或达 total 或空页）→ diff → 批量 upsert 向量（batch 25、批间 500ms 缓解 embedding 压力）→ 归档缺失项。向量文档格式在 `app/sync.go` 的 `projectVectorDocFmt`/`workItemVectorDocFmt`——**查询侧 `app/match.go` 必须对齐**（标题为主、描述截 500 字）。

### 6.3 匹配（两层，`app/match.go`）

1. **精确层**：`logic.NormalizeForExactMatch`（全角→半角、U+3000、小写、空白压缩）建索引，命中 score=1。理由：项目名/标题是「准标识符」，向量对 V2/V3 这类稀有 token 不敏感；
2. **语义层**：仅对未命中项，批量 embedding（50/批）→ `repo.SearchSimilar`（pgvector `<=>` 余弦距离）→ `logic.DistanceToScore`（1-d/2）→ 项目推荐取 top1 / 查重阈值 0.75（`match.duplicate_threshold` 可配）。embedding 未配置时精确层照跑（降级）。

### 6.4 导入（`app/import.go` + `infra/pingcode`）

project_id 形如 `new:名称` → `GenerateProjectIdentifier` + `CreateProject`（内部 `tryEnsureMember`：企业授权无用户身份，取组织目录第一个用户兜底，失败仅告警——私有项目无成员对所有人不可见）。每条：`ResolveTypeID`（UUID 直通/组名/名称/回退 story）→ `ResolvePriorityID`（别名词典 High→「最高」等）→ `resolveAssigneeID`（精确→双向包含≥2字→空）→ `HoursToWorkload`（minute/hour/day 换算）→ 解决方案建议以「【解决方案建议】」并入描述 → 并发建单（`import_concurrency` 默认 3）→ 逐条回写明细。

## 7. API 速查（全部挂 /api，SSE 端点均为 POST）

| 端点 | 类型 | 说明 |
|------|------|------|
| `/parse` | SSE | multipart file → progress/parsed/error |
| `/analyze` | SSE | {text,file_name,special_requirements} → progress/token{delta,phase}/tool{phase,call_id,name,args?,details?}/complete{record_id,items} |
| `/sync` | SSE | progress{stage,message} → complete{result 统计} |
| `/import` | SSE | {record_id,project_id,items:[{id,draft}]} → progress{current,total,title,status}/project_created/complete |
| `/match/projects` | JSON | {names:[…]} → {matches:[{id,name,score,match_type,suggested_name}]} |
| `/match/duplicates` | JSON | {project_id,items:[DraftInput]} → {results:[{index,match|null}]} |
| `/records` `/records/:id` `/records/:id/source` | JSON | 列表/明细/原文（恢复=前端拉明细回填向导） |
| `/overview` `/projects` `/work-items` | JSON | 概览与浏览（work-items 支持 project_id/search/limit/offset） |
| `/settings` `/settings/test-llm` `/settings/test-pingcode` | JSON | 脱敏视图/连通测试 |
| `/health` | JSON | 存活 |

SSE 事件负载的权威定义在 `infra/httpgin/handler_*.go` 与 `web/src/api/types.ts`——**两端同步改**。

## 8. 与 PingCraft 的传承对照（排障先查这里）

| ReqFlow 位置 | PingCraft 出处 | 移植的关键经验 |
|---|---|---|
| `domain/logic/lenientjson.go` | `backend/src/utils/lenientJson.ts` | 三级恢复避免整段非流式重跑（token 翻倍） |
| `app/prompt.go` | `backend/src/prompts/analyzeRequirements.ts` | state 只提取文档明确标注的；JSON 数组直出 |
| `infra/pingcode` 分页 | `pingcode.ts getWorkItems` | 双停止条件防 total 缺失死循环；100 页保险 |
| `infra/pingcode tryEnsureMember` | `pingcode.ts ensureProjectMember` | 新建项目必须补成员，否则私有项目不可见 |
| `app/sync.go` 三态同步 | `routes/sync.ts` | remote_updated_at 比对 + 软归档 + 向量随同步重建 |
| 两层匹配 | `routes/workItems.ts` | 精确前置防向量吞掉稀有 token |
| MinerU 四步 | `services/mineru.ts` | **PUT 预签名必须裸字节不带 Content-Type**（会 SignatureDoesNotMatch） |
| 距离换算 1-d/2 | `workItems.ts distanceToSimilarity` | SeekDB/pgvector 余弦距离值域同为 0-2 |
| 工时换算/名称映射 | `utils/workItem.ts` | minute=1/60h、day=8h；中英优先级别名词典 |
| SSE 心跳/确认门/实时计数 | `routes/analyze.ts` + 前端 | LLM 思考期静默 → 5s 心跳；首 token 停；`"title":` 正则计数 |

**有意不移植的**：LLM 会话前缀缓存（refine 微调，第二波）；embedding 密钥池（第二波，见 §11）；transformers.js 进程内向量兜底（Go 无等价物，改为降级）；用户/RBAC/OAuth（单工作区不需要）。

### 8.5 LLM 层与 pi 的传承（2026-08 二次重构）

port/llm.go 消息模型、infra/llm 双适配器、app/agent loop 均移植自
**pi**（https://github.com/earendil-works/pi，MIT License, Copyright (c) 2025 Mario Zechner，
源码参考副本在 `/Users/weighingzhang/demo/pi`）。各文件头注有对应源文件映射。

| ReqFlow 位置 | pi 出处 | 移植要点 |
|---|---|---|
| `port/llm.go` | `packages/ai/src/types.ts` | Context{SystemPrompt,Messages,Tools} 全量可序列化；Message 三角色 + text/thinking/toolCall 内容块；StopReason 六态；流事件协议 |
| `infra/llm/openai.go` | `api/openai-completions.ts` | reasoning_content/reasoning/reasoning_text 三字段取首个非空（防重复返回）；tool_calls 按 index 聚合；assistant 回放为纯字符串（块结构会被部分端点镜像导致递归嵌套）；空 content+空 tool_calls 的 assistant 跳过 |
| `infra/llm/anthropic.go` | `api/anthropic-messages.ts` | SSE 事件状态机；thinking 块签名回放（扩展思考+工具调用场景 API 强校验）；连续 toolResult 合并为单条 user 消息的 tool_result 块 |
| `app/agent/loop.go` | `packages/agent/src/agent-loop.ts` | 自然终止（无工具调用即停）；**length 截断的工具调用一律不执行、整批错误回执让模型重发**；error/aborted 短路保留已积累消息；ToolOutput 的 Output(LLM)/Details(UI) 拆分；terminate 语义 |

**有意偏离 pi**（改 loop/协议时保持同步）：
1. 回调事件替代 async iterator；中止走 `ctx`（Go 惯例）
2. `Loop.MaxIterations` 安全阀默认 8——pi 刻意无上限，生产导入必须兜底
3. 缺 finish_reason 的兼容端点按「有无工具调用」**推断**终止（pi 对声明支持 finish_reason 的端点直接报错；我们面向杂牌兼容端点从宽）
4. 不移植：模型注册表/厂商 compat 矩阵/steering 消息队列/beforeToolCall 钩子/prepareNextTurn 换模型/并行工具执行/deferred 响应——需要时按 pi 源码对应段落补

## 9. 密钥安全（四道防线，改安全逻辑必读）

1. `.gitignore`：`config.yaml` / `config.*.yaml` 全变体（example 除外）
2. `.githooks/pre-commit`（`make setup` 启用，本机已配）：真实配置文件名直接拦 + 暂存内容扫描；误报逃生 `git commit --no-verify`
3. `scripts/secret-check.sh`：敏感字段非空值 / 带密码 DSN 两类模式；白名单=环境变量名、点分代码标识符、占位符、用户名=密码的本地 DSN；`make test` 必跑
4. 启动自检：`config.CheckExampleLeak` 发现 example 模板被填真实密钥 → ERROR 告警提示轮换；`Config.FilledSecrets` 只打名单不打值

**密钥真泄漏了怎么办**：先平台轮换，再清 git 历史（filter-repo），不要只删文件。

## 10. 踩坑记录（环境 + 代码级，都是真实踩过的）

**环境（本机）**：
- `proxy.golang.org` 超时 → 已 `go env -w GOPROXY=https://goproxy.cn,direct`（机器级配置）
- 容器运行时是 **Docker Desktop**（context `desktop-linux`）；Docker Hub 直拉 `pgvector/pgvector:pg16` 会卡住，已改走 `docker pull docker.m.daocloud.io/pgvector/pgvector:pg16` 后打回标准 tag（本机已有镜像）
- 会话启动链可能残留 `GOROOT=/usr/local/go`（旧 1.18）——会把 brew go（1.27）钉到旧标准库上导致 vet/build 报 "package cmp is not in std"；已在 `~/.zshrc` go 段加 `unset GOROOT` 兜底，脚本/CI 场景用 `env -u GOROOT` 前缀
- 本机 `grep` 被 shell 函数包装为 **ugrep**（ZCode 工具链），行为与 BSD grep 有差异；脚本中复杂正则已实测通过，但调试时注意 `which -a grep`
- pnpm 可用；前端依赖已装好（`web/node_modules`）

**代码级**：
- **Go `\s` 不含全角空格 U+3000**（JS 才含）→ `normalize.go` 显式转换，有单测
- **`net/http` FileServer 会把 `/index.html` 请求 301 到 `./`** → `static_embed.go` 手写 `c.Data` 直出，别改回 FileFromFS
- **macOS bash 3.2**：`$()` 内嵌套 `case` 的 `)` 解析报错（secret-check.sh 用 awk 过滤绕开）；`$VAR` 后紧跟全角字符（如 `）`）会把高位字节当变量名一部分 → 一律写 `${VAR}`
- `$MODE` 这类变量在中文全角括号旁必须花括号（同上）
- GORM `Create(&emptySlice)` 会报错 → 仓储层全部 `len==0` 提前 return
- pgvector 参数化查询：`pgvector.NewVector([]float32)` 实现 Valuer，Raw SQL 直接当参数传
- LLM SSE 单行可能极大 → scanner.Buffer 扩到 8MB（`llm/client.go`）
- PingCode 响应字段形状不固定（扁平/嵌套、字符串/数字时间）→ `pingcode` 包统一走 `getStr/getUpdated` 防御式取值

## 11. 第二波开发指南 · Bug 链路落地路径

设计定稿在 `internal/app/bug/doc.go`，按此顺序做（每步落点明确，不动存量包）：

1. **迁移** `0002_bug.up.sql`：`bug_batches`(id,file_name,source_path,status,created_at) / `bug_rows`(id,batch_id,raw_jsonb,编号,标题,描述,复现步骤等归一化字段,analyzed_priority,(priority p0-p3),priority_rationale,status) / `bug_matches`(id,bug_row_id,candidate_work_item_id,score,match_type,rank(1-3),human_decision)
2. **port**：`BugRepo` 接口（或评估复用 WorkItemRepo）+ DTO
3. **infra/repository**：实现
4. **app/bug**：五个用例——`ImportBatch`（`parser.ParseXLSXRows` 已就绪，表头→行映射，去空白/空行跳过/重名列去重已处理）/ `MatchBatch`（有编号→`NormalizeForExactMatch` 后与 `work_items.identifier` 及标题精确匹配；无编号→复用两层匹配取 **top3**）/ `ConfirmMatch`（人工确认/否决）/ `Prioritize`（批量 LLM 定级 P0-P3——prompt 要点：给出理由 rationale、P3 判定要保守、输出 JSON 数组带 bug_row_id）/ `Sync`（type_id=bug 建单，**关联关系写入描述**：「关联需求: {identifier} {标题}」；`parent_id` 备选——PingCode 6.13.5 无关联 API，已在 `port/platform.go` 注释确认）
5. **httpgin**：`/api/bugs/*` 路由（导入 SSE / 匹配 / 确认 / 定级 SSE / 同步 SSE），handler 只调 app
6. **前端**：替换 `web/src/pages/Bugs.tsx` 占位（四步向导复用 import 的模式：上传→匹配确认表格（top3 候选单选/否决/标无效）→定级面板（可改档）→同步）
7. **顺手可做**：embedding 密钥池（参考 PingCraft `services/embedding.ts`：轮询/429 冷却读 Retry-After/401 摘除，只在 `infra/embedding` 内改）；LLM refine 微调会话（`app/analyze` 已留注释扩展点，进程内 map 参考 PingCraft `llmSession.ts` 的前缀缓存设计）

## 12. LLM 演进主线：PingCode 工具化接入 agent.Loop

> 背景：LLM 层已完成 pi 化重构（§8.5）——会话化消息模型、双协议适配器、loop 骨架就位，
> v1 分析仍为单发直调。本节是接手「专属 agent loop」落地所需的全部上下文。
>
> **✅ 已落地（2026-08-26）**：`llm.agent_mode: true` 开启后走 agent 链路。落点：
> `internal/app/tools/tools.go`（5 个只读工具 + 成员进程内缓存，均 mock 单测覆盖）、
> `app/analyze.go`（runAgent/runClassic 双路径，loop 彻底失败自动降级单发）、
> `app/prompt.go`（模板拆为指令头+文档节：agent 模式 SystemPrompt=指令头+工具指南，
> 原文只进首轮 user 消息）、迁移 0002（`import_records.agent_context` 落库会话 JSON，
> 单发模式同样落库，refine 的统一载体）、SSE `tool` 事件（`handler_analyze.go` 与
> `web/src/api/types.ts` 两端同步）与前端分析页「工具查证轨迹」时间轴。
> 集成测试 `app/analyze_agent_test.go`：全链路/降级/单发回归三场景。
> 待真机验收项见 §12.5（需要真实 llm.api_key 与已同步语料）。

### 12.1 目标与产品红线

目标：把 PingCode 能力包装为 `agent.Tool` 接入 `agent.Loop`，让需求分析从「单发提取」
进化为「分析 → 自主查证（查项目/查重/查元数据）→ 修正建议」的专属 agent；最终导入
仍走人工确认——产品总纲原则 1「AI 是草稿机不是审批者」。

**红线：读操作工具可自主调用；写操作（`CreateProject`/`CreateWorkItem`）第一波不得
成为 loop 的自主工具。** 如确需写工具，必须 `Terminate: true` 且 Output 只返回
「待确认 payload」——执行仍由人工点击既有 `/api/import` 流程完成。

### 12.2 已就位的地基（接手先读这五个文件）

| 文件 | 就位内容 |
|---|---|
| `internal/app/agent/loop.go` | Loop/Tool/ToolOutput 契约、自然终止、MaxIterations=8 安全阀、length 截断整批拒绝执行、terminate 语义 |
| `internal/port/llm.go` | `Context.Tools` 注入位（loop 的 registerTools 自动写入）；ToolSpec/ToolCall；消息模型全可序列化 |
| `internal/infra/llm/openai.go` | tools + tool_choice 请求渲染、tool_calls 流式按 index 聚合（httptest 覆盖） |
| `internal/infra/llm/anthropic.go` | input_schema 渲染、tool_use 流式聚合（httptest 覆盖） |
| `internal/app/agent/loop_test.go` | scriptedClient mock 模式——工具链集成测试直接复用 |

### 12.3 工具候选清单（读操作优先）

数据源优先用本地缓存仓储（`ProjectRepo`/`WorkItemRepo`——快、不占平台 API 配额），
`PlatformClient` 直查仅做缓存未同步时的兜底：

| 工具名（建议） | 数据源 | 用途 |
|---|---|---|
| `search_projects(name)` | ProjectRepo（复用 `logic.NormalizeForExactMatch` 精确 + contains 模糊） | 草稿项目名 → 真实项目 ID |
| `search_work_items(project_id, title)` | WorkItemRepo（同上归一化精确 + contains） | 导入前自主查重 |
| `get_work_item_types(project_id)` | MetaRepo | type 名称 → UUID 映射（草稿自检 type_id 合法性） |
| `get_project_members(project_id)` | PlatformClient.ListProjectMembers（进程内缓存） | assignee 姓名解析 |
| `list_recent_work_items(project_id, limit)` | WorkItemRepo | 模型主动看语料现状，描述写得更贴实际 |

参考实现：`app/match.go`（两层匹配的精确层就是 `search_projects` 的内核）、
`app/import.go` 的 `resolveAssigneeID`（姓名解析三级策略可直接搬）。

### 12.4 落地步骤

1. 新建 `internal/app/tools/` 包，依赖注入构造（repos + platform client + 配置），
   每个工具实现 `agent.Tool`；Execute 的 Output 返回紧凑 JSON（id/name 等字段），
   Details 返回人读摘要——前端工具轨迹展示用
2. `AnalyzeService` 加开关（如 `llm.agent_mode`）：开启时构造
   `agent.New(llm, tools, agent.Config{})` 替代直调 `llm.Stream`。loop 的 onDelta
   与现有 `AnalyzeDelta`（thinking/answer）天然兼容；onEvent 的
   `tool_execution_start/end` 作为新 SSE 事件类型透传（`handler_analyze.go` 与
   `web/src/api/types.ts` 两端同步加）
3. 工具参数 JSON Schema 保持极简（string/enum 为主）——schema 越简单模型调用越稳；
   枚举值来自 MetaRepo 时在构造时快照，别在 Spec() 里查库
4. 会话膨胀控制：需求文档原文只在首轮 user 消息，轮数由 MaxIterations=8 兜底，
   v1 无需截断。落库会话 = 序列化最终 Context（`import_records` 加列或独立表），
   第二波 refine 微调复用同一载体
5. 前端：分析页时间轴加「工具调用」节点（复用 phase 展示模式，tool_execution
   事件驱动），让用户看到 agent 查证过程——这是产品差异点，不是调试信息

### 12.5 测试与验收

- **单测**：tools 包 mock `PlatformClient`/repo（port 接口即为此设计）；
  loop 集成复用 `loop_test.go` 的 scriptedClient——第一轮脚本返回 toolCall、
  第二轮返回终稿，断言工具执行、toolResult 回填、消息序列
- **手测验收**：上传含模糊项目名的需求文档 → agent 自主调 `search_projects`
  修正 `project_name` → 草稿的 project_id 可直接进入导入；全程 SSE 时间轴
  可见「思考 → 工具调用 → 修正 → 终稿」轨迹
- **真机注意**：DeepSeek 等推理模型的 reasoning_content 以 thinking 相位展示，
  工具调用期间前端要有明确进度感（tool_execution 事件即为此设计）；
  pi 的并行工具执行与 beforeToolCall 审批钩子暂不移植，需要时按
  pi 源码 `agent-loop.ts` 对应段落补（见 §8.5 不移植清单）

## 13. 已知限制与技术债（如实）

| 项 | 影响 | 处置建议 |
|----|------|---------|
| ~~LLM 层无测试~~ | 已解决（2026-08）：infra/llm 双适配器 httptest + app/agent loop mock 单测就位 | 剩余：`app/analyze` 的 mock LLM 编排单测（宽松恢复链的分支覆盖） |
| repository 层无测试 | schema 回归风险 | testcontainers-go 或对本机 docker PG 跑薄集成测试 |
| 向量固定 1024 维 | 换模型要重建库 | 文档已写死流程；如需多维度考虑按维度分表 |
| LLM 会话进程内 | 单实例限定 | 第二波做 refine 时一并决策（Redis 或维持单实例） |
| 后端无热重载 | 开发改代码要重启 | 可引入 air，非必须 |
| refine 微调未做 | 分析结果只能重来 | 第二波（见 §11.7） |
| `logic/mapping.go` 别名词典硬编码在 domain | 纯逻辑无配置依赖的取舍 | 若需用户自定义别名，挪到配置并保持纯函数签名 |
| 前端单 chunk 1.5MB | 首载稍慢 | 按路由 code-split，非紧急 |

## 14. 运维备忘

- **DB**：`docker compose up -d`；数据卷 `reqflow_pgdata`；连接 `postgres://reqflow:reqflow@127.0.0.1:5432/reqflow`
- **迁移**：启动时自动跑（`database.auto_migrate: true`），内嵌 SQL 幂等（schema_migrations 表）；新增迁移文件放 `internal/infra/database/migrations/NNNN_*.up.sql`（+ 可选 down）
- **重建向量场景**：换 embedding 模型/维度 → 停服 → 清库或改迁移 → 重启 → 点「立即同步」全量重建（增量同步只处理有变更的项，不会自动重算存量向量——PingCraft 有 `rebuild:vectors` 脚本，需要时可参照写一个）
- **日志**：stdout（text/json 可配）；启动告警（配置降级）集中在头几行
- **升级发布**：`make build` → 替换二进制 + 保留 config.yaml 与 data/ → 重启（自动迁移）

## 15. 联系与上游

- 产品定义/路线图决策记录：`docs/PRODUCT.md` + 本文件
- 参考实现：`/Users/xxxg/demo/PingCraft`（只读参考，勿改动）
- PingCode 开放 API 文档基准：6.13.5（PingCraft 根目录 `pingcode.md` 有摘录）
- MinerU API：mineru.net 精准解析 v4（限制单文件 ≤200MB、≤200 页）
