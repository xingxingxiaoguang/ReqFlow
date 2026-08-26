# ReqFlow 交接文档

> 面向下一个接手开发的同学。本文只回答四件事：**项目现在是什么、怎么跑起来、改代码必须知道的上下文、接手后干什么**。
> 产品定位、方向性决策与「重复人工任务 → AI 驱动 Task」的抽离范式见 [PRODUCT.md](./PRODUCT.md)；技术实现细节以本文件为准。

---

## 1. 项目是什么（30 秒版）

- **现状**：Task 生命周期管理 + AI agent loop 驱动的项目管理辅助平台，**任务与任务通过数据集衔接**（不依赖任何外部协作平台，已全量剪除 PingCode）。需求导入任务化：上传解析 → 确认门 → **AI 分析（agent 工具驱动：经 read_document/search_document 自主阅读原文，write_work_items 分批产出草稿，关键决策点 ask_human 问人）** → 查重确认 → 生成需求数据集。提示词零固定模板——按任务类型 profile 动态装配（指令头注册表 + schema 渲染字段规范 + 工具集同源组装指南）。数据集为第一等公民（结果集浏览、查重与关联匹配的统一语料）。长程流程在任务详情页全程跟踪（步骤时间线 + 分阶段工作区），支持暂停/继续（分析走会话检查点、数据集生成幂等重建）、编辑、手动完成，进度落库可重放，执行脱离页面（断开照跑、服务重启自动标暂停、SSE 断线自动重连）。
- **下一步**：第二波 Bug 链路任务化（消费需求数据集 → 产出 Bug 数据集，方案定稿在 `internal/app/bug/doc.go`，见 §5.1）；多客户端上线补全（认证 / LLM 并发限流，见 §5.4）；技术债清理（§5.3）。
- **仓库**：`/Users/xxxg/demo/ReqFlow`，main 分支。项目自主演进（工具/提示词/装配范式自成体系，见 §4.6 与 §5.2）。

## 2. 怎么接手：跑起来

```bash
cd /Users/xxxg/demo/ReqFlow

# 0) 一次性初始化（启用 pre-commit 密钥护栏）
make setup

# 1) 数据库（Docker，PG16 + pgvector；镜像拉取见 §4.5 环境坑）
docker compose up -d

# 2) 配置（首启会自动生成模板；或手动 cp config.example.yaml config.yaml）
#    至少填 llm.api_key（需要语义查重则填 embedding.api_key）
vim config.yaml

# 3) 开发模式（两个终端）
make dev        # 后端 :8080（直接 go run；热重载未配，改代码需重启）
make frontend   # 前端 Vite :5173，/api 代理到 :8080

# 4) 发布验证
make build      # → bin/reqflow（约 29MB 单二进制，前端已 embed）
./bin/reqflow   # 同目录放 config.yaml 即可跑
```

**质量门禁**：`make test` = `go vet` + `go test` + 架构围栏（`scripts/arch-check.sh`）+ 密钥扫描（`scripts/secret-check.sh`）。提交代码前必过。

## 3. 怎么接手：架构地图

### 3.1 分层与依赖铁律（最重要）

四层架构，**依赖只允许向内**，业务层永远不知道 infra 的存在：

```
cmd/reqflow      组装点：读配置 → 构造 infra 实现 → 注入 app 用例 → 挂 httpgin。
                 全项目唯一知道所有具体实现的地方（cmd/reqflow/main.go）

internal/infra   外层一体（基建 + 三方客户端 + 仓储 + HTTP 路由）
internal/app     用例编排（task/analyze/dataset/match/workflow/parse/settings/overview + bug 占位）
internal/port    出站接口契约（repo/llm/embedding/parser 四个文件）
internal/domain  实体模型 + 纯领域逻辑（零三方依赖，仅标准库）
```

**依赖白名单**（arch-check.sh 强制，越界直接 fail）：

- `cmd → 全部`
- `infra/httpgin → app`（且**只能** import app——不摸 port/domain/其它 infra，HTTP 层的入参出参全部用 app 层 DTO，如 `app.DraftInput`、`app.AnalyzeDelta`）
- `infra/{repository,llm,embedding,parser} → port, domain, infra/{config,log,database,crypto}`
- `app → port, domain`；`port → domain`；`domain → 仅标准库`（`go list -deps` 检查）

**为什么这么分**：infra 与 adapter 合并为一层是明确决策（不会大规模换基建，拆开只剩目录成本）；「httpgin 只准调 app」保住 handler 不直摸仓储；接口定义在 port 层（集中契约，好导航），因此「app 不 import infra」主要靠 arch lint 而非编译器——**这是刻意的，改架构时保持 lint 规则同步更新**。

### 3.2 代码地图

```
cmd/reqflow/
  main.go            组装点：配置→DB→迁移→infra→app→http；启动时 taskMgr.Recover
                     （服务重启把卡在 running 的任务/步骤标为 paused 可手动继续）
  static_embed.go    //go:build embed：go:embed dist，SPA 直出（注意：不用 http.FileServer，见 §4.5）
  static_dev.go      //go:build !embed：开发模式空实现（前端由 Vite 提供）

internal/domain/
  model/model.go     Dataset/DatasetItem/DraftItem/Task/TaskStep/TaskItem + 工作流元数据
                     （Workflow/WorkflowStep/StepDependency/StepKind）+ 状态常量
  logic/             全部纯函数 + 单元测试（改这里不需要起任何服务）
    normalize.go       归一化精确匹配用（全角→半角含 U+3000、小写、空白压缩）
    similarity.go      余弦距离 0-2 → 分数 0-1
    lenientjson.go     LLM 宽松 JSON 恢复三级降级（剥围栏→截取[...]→修复截断数组）
    draft.go           LLM 输出白名单归一化（priority 3 档/type 5 类/状态占位词清洗）

internal/port/
  repo.go            DatasetRepo/TaskRepo + 向量 DTO（DatasetItemVector/SimilarDatasetItem）
  llm.go             LLMClient（Stream/Complete/Ping）+ pi 式消息模型（Context/Message/内容块/
                     ToolSpec 全可 JSON 序列化——Context 即会话，暂停检查点与 refine 以它为单位）
                     + 流事件协议（start/text_delta/thinking_delta/toolcall_*/done/error）
  embedding.go       Embedder（Available() 驱动降级）
  parser.go          DocParser

internal/app/        用例层；全部依赖构造注入，进度用回调上报
  workflow.go        ⭐ 工作流注册表（半元数据驱动）：任务类型定义 = 步骤链 + 依赖声明
                     （StepKind: parse/human/analyze/dataset）；创建任务时快照进
                     tasks.workflow（任务自描述，不受定义演进影响）；新增任务类型 =
                     加一条定义 + 复用/新增 kind 执行器（方向性决策见 PRODUCT §4 决策二）
  task.go            ⭐ TaskManager 任务门面：CRUD/编辑/触发/暂停/继续/完成/Recover +
                     运行登记表（每任务单写者 goroutine）+ 数据集浏览透传——httpgin 唯一任务入口
  runner.go          ⭐ 步骤执行器小接口（parse/analyze/dataset 服务结构即满足，测试注入假实现）
                     + 按 StepKind 分发执行 + 元数据驱动的门推进（advanceGate）/
                     触发校验（canTriggerStep）/ 暂停恢复；状态机转换先落库后发 Broker；
                     触发时**同步预置任务级状态**（见 §3.3）；token 事件 150ms 节流合并（见 §4.2）；
                     错误分类（ctx 取消→paused / 其他→门内重试或终态）
  broker.go          ⭐ 进程内事件扇出（非阻塞发布，订阅/退订锁内串行）——SSE 可重接的基础
  analyze.go         流式分析编排（agent 模式：文档经工具阅读 → write_work_items 分批产出 →
                     必要时 ask_human 问人；sink 空则降级单发直调：prompt 渲染→流式→宽松
                     恢复→非流式回退）→ 产出 AnalyzeOutcome（明细+会话 JSON+存档路径，不落库
                     ——持久化移交 TaskManager）+ Resume（从 agent_context 检查点重放 sink 续跑）
  analyze_profile.go ⭐ 任务类型 → agent 装配描述注册表（AnalyzeProfileOf）：指令头（{field_spec}
                     占位由产出 schema 渲染）+ 写入工具绑定 + 单发示例。提示词零固定模板：
                     字段规范段从 schema 生成（FieldSpec.Prompt 同源），新增任务类型 =
                     工作流 + 产出 schema + profile 三注册，装配零改动
  dialog.go          ⭐ DialogHub 人工交互桥：ask_human 工具阻塞登记 → SSE dialog 事件 →
                     HTTP Answer 投递；pending 随 SSE 快照下发（刷新恢复弹窗）；ctx 取消
                     即空回答收束（任务暂停检查点语义不变）
  dataset.go         ⭐ DatasetWriter 数据集生成用例：草稿 → 向量化（分批）→ 幂等写入数据集；
                     任务产物的落点，任务间衔接的载体；含 DraftInput/DraftSaveInput 草稿 DTO
  prompt.go          ⭐ 提示词动态装配渲染器（零固定模板）：renderFieldSpecSection（schema →
                     字段规范段，类型/枚举值域/必填自动标注 + FieldSpec.Prompt 提取说明）/
                     renderAnalyzeHead（profile.Role 的 {field_spec} 占位替换）/ renderClassicOutputFormat
                     （单发契约 + 示例：profile.Example 覆盖或 schema 骨架生成）/ renderAgentSystem
                     （+ DocumentedTool 工具指南）/ renderDocManifest（agent 首轮文档清单 + 首步指引）
  match.go           查重（两层匹配：归一化精确 + 向量语义，语料 = 需求数据集）
  parse.go settings.go overview.go
  bug/doc.go         ⭐ 第二波 bug 域完整设计（schema/用例流/关联落地方式）——做第二波先读它；
                     落地时以新 task 类型接入（见 §5.1），需求数据集为关联输入、Bug 数据集为产出
  tools/             ⭐ agent 过程工具（pi 工具模式，按运行构造，tools.BuildForRun）：read.go
                     （read_document 行号分页+续读提示+超长行硬拆）/ search.go（search_document
                     正则/字面量 grep 式输出+可行动截断提示）/ write.go（写入工具+DraftSink：
                     WriteSpec 绑定任务产出 schema，同 key 覆盖增量产出、replace_all 整体重写、
                     逐条校验即时回执；ReplayFrom 按同一 WriteSpec 从会话重放）/ ask.go（ask_human
                     经 HumanAsker 阻塞问人，options 候选单选）；splitLines 全包共享，行号口径一致
  agent/loop.go      pi 式 agent loop 骨架（Tool 接口 + 自然终止 + MaxIterations 安全阀 +
                     length 截断整批 fail + ToolOutput 的 output/details 拆分）；ctx 取消即
                     干净中止并返回已积累 Context——任务暂停检查点的载体

internal/infra/
  config/config.go   YAML+env 覆盖（反射走 env tag）、Validate（dsn 硬校验/其余降级 warns）、
                     FilledSecrets/CheckExampleLeak（安全自检）；example.yaml 内嵌（首启生成模板）
  database.go         GORM 连接（重试）+ 手写迁移器（内嵌 SQL，schema_migrations 表，幂等）
  migrations/         0001_init（projects/work_items 等，已被 0005 剪除）/ 0003_tasks（任务三表，
                       task_items 为需求草稿物理列）/ 0004_workflow（tasks.workflow 列）
                       / 0005_datasets（datasets/dataset_items 建表 + 任务衔接列 + DROP 平台语料表）
                       / 0006_dataset_generic（数据集通用底座：item_key/fingerprint/元数据列）
                       / 0007_archive（归档表）；研发阶段无数据搬迁，改表直接推倒重建
  repository/        两个仓储实现（GORM + pgvector；dataset_repo 的 Raw SQL 向量检索注意
                      **显式列映射**——嵌套结构体 Scan 会丢 fields 列，踩过坑）
  llm/               双协议适配器（均移植自 pi，偏离清单见 §4.6）：client.go 工厂按 provider 分发
                     openai.go——OpenAI 兼容 /chat/completions（reasoning 三字段防重复、
                       tool_calls 流式增量聚合、缺 finish_reason 时推断）
                     anthropic.go——Anthropic Messages 协议（SSE 状态机、thinking 签名回放、
                       连续 toolResult 合并为单条 user 消息）
  embedding/         OpenAI 兼容 /embeddings（批量、按 index 归位）
  parser/            parser.go（分发+docx 标准库 zip+XML）mineru.go（四步云端解析）xlsx.go（行级解析，第二波用）
  httpgin/           server.go（路由表）sse.go heartbeat.go handler_tasks.go（任务/工作流/数据集端点）
                     handler_misc.go handler_match.go
  database/migrations 见上

web/                 React 18 + AntD5 + ProLayout + TanStack Query + react-router
  src/hooks/useTaskEvents.ts ⭐ 任务 SSE 订阅：snapshot 整包写缓存 + task/step/items 补丁，
                     token/progress/tool_trace/dialog 只进页面本地态（不落缓存）；dialog 为
                     阻塞事件——pending 随 snapshot 恢复（刷新/重连弹窗不丢）、按 call_id 幂等；
                     **断线 3s 自动重连（重连重收快照）；snapshot 帧带 data 包装，与实时事件形状
                     统一**；卸载即退订
  src/api/sse.ts             POST SSE 解析器（fetch + ReadableStream）；**单帧异常只丢该帧不断流**
  src/api/tasks.ts           任务 API 封装（创建/列表/详情/编辑/暂停/继续/完成/步骤触发/草稿保存/数据集浏览）
  src/pages/Tasks.tsx        任务列表（状态筛选 + 生命周期操作）
  src/pages/Datasets.tsx     数据集浏览（结果集 + 条目明细 + 来源任务追溯）
  src/pages/tasks/           详情页 TaskDetail（头部+步骤时间线+按阶段工作区；analyze 步骤
                     标签按 settings.llm.agentMode 如实显示）+
                     panels/（ConfirmParsePanel / AnalysisPane（双区实时滚动+工具轨迹+人工
                     交互 Modal：候选单选或自由文本，可关闭保留重开入口防丢）/ MatchImportPanel）
                     + TaskNew（工作流预览，步骤标签同上）
  其余页面：Overview/Settings(Bugs 占位)；Settings 含「分析模式」展示（agent 工具驱动/单轮直调）
```

### 3.3 任务系统不变量（改 task/runner/broker 前必读）

- **执行脱离 HTTP**：步骤跑在 `TaskManager.spawn` 的 goroutine 里，持有独立可取消 ctx（`context.WithoutCancel` 派生 persistCtx 供收尾落库）；触发端点 fire-and-forget，进度走 `/events` 订阅。
- **触发同步预置（fire-and-forget 竞态根除）**：`triggerStep` 在 spawn 前**同步**把任务置为 `running + 目标步骤序号` 并落库 + 发布 task 事件——202 返回时 DB 已是新状态，客户端任意时刻的 GET 都拿不到旧状态；goroutine 内 beginStep 落库的是相同值（幂等）。spawn 被拒（并发触发）时回滚预置。
- **每任务单写者**：任务/步骤的 DB 写入只发生在步骤 goroutine 内（`running` 登记表保证）；生命周期操作（Pause 等）先取消、等 `<-done`、再重读 DB 定夺（取消落地前已自然完成 → 报「任务已完成，无法暂停」，不覆盖终态）。
- **persist-then-publish**：所有状态变更先落库再发 Broker；Broker 非阻塞发布（通道满丢帧，快照兜底）；SSE 端点**先订阅再回放快照**（消除竞态窗口），客户端断开只退订。
- **SSE 帧形状统一**：所有事件（含 snapshot）负载统一 `{"task_id","data":…}` 包装，前端统一 `payload = data?.data` 解包——**新增事件类型必须带 data 键**（形状不一致会杀流，踩过坑）。前端单帧解析/回调异常只丢该帧；断线 3s 自动重连重收快照。
- **token 节流合并**：token 事件逐 token 一帧会打爆 broker 64 缓冲（慢消费者丢帧→推理/正文面板空白），`execAnalyzeStep` 内按 150ms 窗口合并成批帧（阶段切换先 flush 旧段），分析结束兜底 flush。改 token 发布方式时保持节流语义。
- **暂停语义**：analyze=取消 loop ctx → 已积累 `port.Context` 序列化进 `agent_context` → paused；继续=回放 Context 调 `Analyze.Resume`。dataset=向量化分批间取消 → paused（building 数据集保留）；继续=复用同一数据集幂等重建条目。parse=取消重跑（幂等）。
- **步骤失败不杀任务**：requirement_import 步骤失败 → 回到对应人工门（awaiting）可重试；任务可手动完成（awaiting 态）进入终态。
- **人工交互（dialog）是阻塞事件**：ask_human 工具经 DialogHub 登记 pending 后阻塞等待 `POST /tasks/:id/dialog` 应答（loop 顺序执行 → 每任务至多一个 pending）。可靠性走快照而不是瞬时事件：pending 随 SSE snapshot 下发（刷新/重连恢复弹窗），Broker 丢帧也能恢复；ctx 取消（暂停）以 IsError 回执收束——loop 先追加回执再退出，会话保持合法，续跑后模型可重新发问。

## 4. 执行所必须的上下文

### 4.1 数据模型

| 表 | 要点 |
|----|------|
| `datasets` | 任务产出的结果集：type（requirement / bug…）、name、source_task_id（产生它的任务）、status（ready/building）、item_count；任务间衔接的载体 |
| `dataset_items` | 类型化条目：fields TEXT（JSON 文本，需求=草稿形状）+ **`embedding vector(1024)` 行内列** + HNSW 余弦索引——查重 / 关联匹配 / 后续任务输入的语料 |
| `tasks` | 长程流程生命周期载体：type（requirement_import）、status（pending→running→awaiting\|paused→running→succeeded\|failed）、current_step、workflow（定义快照 JSON）、input/output/agent_context 为 **TEXT（JSON 文本，非 JSONB——GORM 字符串直写免类型转换）**；output_dataset_id/input_dataset_id（任务衔接）、计数与错误信息 |
| `task_steps` | 步骤轨迹（task_id CASCADE + seq 索引）：status/name/detail/data（JSON 文本：工具轨迹/导入汇总）——详情页时间线与重放的底料 |
| `task_items` | 草稿明细（AI 分析产物，生成数据集前的编辑缓冲）：draft 各列 + state + status/error_message；`ReplaceTaskItems` 只替换未入数据集的行 |

**向量维度是硬约束**：迁移固定 1024（bge-m3），`config.Validate()` 会拦截 `embedding.dimensions != 1024`。换向量模型 = 改迁移 + 清库重建 + 改配置。

### 4.2 核心流程

**需求导入主链路（任务化后）**：

```
前端 /tasks/new → POST /api/tasks {type:requirement_import,title} 创建任务（播种 4 步骤）
  → POST /api/tasks/:id/parse (multipart) fire-and-forget：文件存 upload_dir；
    触发时同步预置任务 running+步骤1（见 §3.3），步骤 goroutine（TaskManager.spawn，
    独立可取消 ctx）内调 ParseService.Run：
    txt/md 直读；docx=zip 内 word/document.xml 逐 <w:p> 拼接 <w:t>（标准库）；
    pdf=MinerU 四步：申请预签名链接→PUT 裸字节（不带 Content-Type！）→轮询(5s/10min，
      进度→步骤 detail 落库+Broker 扇出)→下载 zip 取 full.md
    解析成功 → input.parsed_text 落库 → 步骤1 succeeded → 步骤2 awaiting → 任务 awaiting
    （上传暂存文件随即清理；暂停/失败可重试——原文件路径在 input 中）
→ 前端订阅 POST /api/tasks/:id/events（SSE：先订阅再快照回放 + 实时 step/task/items 事件；
  断线 3s 自动重连重收快照）
  → 详情页工作区进入「确认解析」面板：预览/编辑全文 + 额外要求 → 保存（PATCH）→ 开始分析
→ POST /api/tasks/:id/analyze fire-and-forget（触发同步预置 running+步骤3）
  app/analyze 按 TaskType 解析 AnalyzeProfile（指令头/产出 schema/写入绑定/单发示例）：
  【agent 模式（llm.agent_mode，主路径）】
  → SystemPrompt 动态装配 = profile 指令头（{field_spec} ← schema 渲染）+ 额外要求
    + 工具指南（DocumentedTool 同源）；首轮 user 消息 = 文档清单（文件名/行数/字数 +
    「必须先调 read_document」首步指引），原文不进上下文
  → agent.Loop（迭代上限 llm.agent_max_iterations，默认 32）：模型经 read_document
    （行号分页+续读提示）/ search_document（正则/字面量）自主阅读原文；
    write_work_items 分批产出草稿（WriteSpec 校验：schema 必填/枚举/数值，即时回执
    accepted/updated/rejected，模型修正重交；同 key 覆盖可修订）；ask_human 经 DialogHub
    阻塞问人（SSE dialog 事件 + HTTP 应答）
  → 终稿契约：产出 = DraftSink 累积（不再是「末条消息是 JSON 数组」）；末条消息只需简短总结
  → token 增量在 runner 内 150ms 节流合并后经 Broker 透传前端双区滚动（token 不落库——
    瞬时流；工具轨迹在步骤 data 落库，重放走快照）
  → sink 收束（sinkTail）：sink 空（含模型只聊天不写）→ 降级单发直调；loop 中断但已有
    产出 → 保留部分结果 + 告警进度；成功 → DraftSink.Items() 即草稿明细
  → 原文存档（demand_dir，SourcePath）→ 产出 AnalyzeOutcome（明细+会话 JSON+存档路径，
    不落库——持久化移交 TaskManager）
  → 步骤3 succeeded → 步骤4 awaiting → 任务 awaiting（生成数据集门）
  → 暂停：取消 ctx → loop 返回已积累 Context → agent_context 落库（检查点）→ 任务 paused
  → 继续：反序列化 Context → DraftSink.ReplayFrom 重放会话中全部写入调用重建草稿
    （会话即事实源，确定性重放）→ 续跑 loop
  → 分析失败：步骤3 failed → 任务回确认解析门（awaiting）可修正重试（清会话检查点）
  【单发直调（默认模式，也是 agent 降级目标）】
  → 一条 user 消息 = 指令头 + 单发输出契约（profile.Example 富示例或 schema 骨架）
    + 额外要求 + 文档全文 → llm.Stream 流式（thinking/answer 两相位）
  → 解析降级链: json.Unmarshal 标准解析 → logic.ExtractJSONArrayLenient（剥围栏→截取→修复
    截断）→ 流彻底失败时 llm.Complete 非流式重调一次（同一 prompt）；流中断但已有部分输出时
    优先宽松恢复部分结果
  → logic.NormalizeDrafts 白名单归一化 → 同上收束
→ 生成数据集门: 自动 POST /api/match/duplicates（语料 = 已有需求数据集，精确层 + 语义层）
  → 行内查重徽标 → 编辑行（title/priority/type/hours/assignee）→ 保存草稿（POST /tasks/:id/items）
  → 点生成: POST /api/tasks/:id/dataset {dataset_name} fire-and-forget：runner 创建 building 数据集
    → DatasetWriter 分批向量化 → ReplaceDatasetItems 幂等写入（断点续跑/失败重试复用同一数据集）
    → 数据集 ready + 任务 OutputDatasetID 回填 → 任务终态 succeeded
  → 手动完成: POST /api/tasks/:id/complete（awaiting 态把当前门步骤标 succeeded → 终态）
```

**查重（两层，`app/match.go`，语料 = 需求数据集）**：
1. **精确层**：`logic.NormalizeForExactMatch`（全角→半角、U+3000、小写、空白压缩）对全部需求数据集条目标题建索引，命中 score=1。理由：标题是「准标识符」，向量对稀有 token 不敏感；
2. **语义层**：仅对未命中项，批量 embedding（50/批）→ `SearchSimilarDatasetItems`（pgvector `<=>` 余弦距离）→ `logic.DistanceToScore`（1-d/2）→ 阈值 0.75（`match.duplicate_threshold` 可配）。embedding 未配置时精确层照跑（降级）。语义层向量文档格式与 DatasetWriter 一致（`Title: …\nDescription: …`，描述截 500 字）。

**数据集生成（`app/dataset.go` DatasetWriter）**：草稿 → 分批向量化（`embedding.batch_size`）→ `ReplaceDatasetItems` 事务重建（未发布数据集断点续跑 = 幂等重写）→ 数据集 ready。任务终态时 `output_dataset_id` 回填——**任务与任务通过数据集衔接**（bug 分析等后续任务以需求数据集为输入）。

### 4.3 API 速查（全部挂 /api，SSE 端点均为 POST）

| 端点 | 类型 | 说明 |
|------|------|------|
| `/tasks` `/tasks/:id` | JSON | 创建 {type,title} / 详情 {task,steps,items}（task.Workflow = 工作流定义快照） |
| `/tasks?status=&type=&limit=` | JSON | 列表 |
| `/workflows` | JSON | 任务类型目录（工作流元数据：步骤链 + 每步依赖声明），创建入口展示用 |
| `/tasks/:id` | JSON | PATCH 编辑 {title?,parsed_text?,special_requirements?}（awaiting/paused 可改） |
| `/tasks/:id/items` | JSON | 批量保存门内草稿 {items:[{id?,draft}]} |
| `/tasks/:id/parse` | multipart | fire-and-forget 上传解析（存 upload_dir，立即返回 {task_id}） |
| `/tasks/:id/analyze` | JSON | fire-and-forget AI 分析（暂停恢复走 AgentContext 检查点） |
| `/tasks/:id/dataset` | JSON | fire-and-forget 生成数据集 {mode: create\|merge\|upsert\|replace, dataset_id?, dataset_name?}（断点续跑幂等重建；预览走 /dataset/preview 分桶不落库） |
| `/tasks/:id/dialog` | JSON | 人工回答 agent 的提问 {call_id, answer}（ask_human 阻塞等待的出口；无 pending 或 call_id 不匹配 409） |
| `/tasks/:id/pause` `/resume` `/complete` | JSON | 生命周期：暂停（取消步骤 ctx）/ 继续（按暂停步骤重触发）/ 手动完成（awaiting→终态） |
| `/tasks/:id/events` | SSE | **快照回放 + 实时**：snapshot（含 dialog pending 恢复）/ task / step / items / progress / token{delta,phase}（150ms 合并帧）/ tool_trace{phase,call_id,name,args?,details?,is_error?} / dialog{phase:ask\|close, call_id, question?, options?, reason?} / error + 5s ping 心跳；断开只退订，任务照跑 |
| `/datasets` `/datasets/:id` | JSON | 数据集浏览（结果集 + 条目明细 + 来源任务追溯） |
| `/match/duplicates` | JSON | {items:[DraftInput]} → {results:[{index,match|null}]}（语料 = 需求数据集） |
| `/overview` | JSON | 概览（datasets/datasetItems/tasks + recentTasks/recentDatasets） |
| `/settings` `/settings/test-llm` | JSON | 脱敏视图/连通测试 |
| `/health` | JSON | 存活 |

SSE 事件负载的权威定义在 `infra/httpgin/handler_tasks.go` 与 `web/src/api/types.ts`——**两端同步改**；事件负载统一 `{"task_id","data":…}` 包装（含 snapshot，见 §3.3）。

### 4.4 密钥安全（四道防线，改安全逻辑必读）

1. `.gitignore`：`config.yaml` / `config.*.yaml` 全变体（example 除外）
2. `.githooks/pre-commit`（`make setup` 启用，本机已配）：真实配置文件名直接拦 + 暂存内容扫描；误报逃生 `git commit --no-verify`
3. `scripts/secret-check.sh`：敏感字段非空值 / 带密码 DSN 两类模式；白名单=环境变量名、点分代码标识符、占位符、用户名=密码的本地 DSN；`make test` 必跑
4. 启动自检：`config.CheckExampleLeak` 发现 example 模板被填真实密钥 → ERROR 告警提示轮换；`Config.FilledSecrets` 只打名单不打值

**密钥真泄漏了怎么办**：先平台轮换，再清 git 历史（filter-repo），不要只删文件。

### 4.5 踩坑记录（长期有效的坑，改相关代码前扫一眼）

**环境（本机）**：
- Go 代理已配 `goproxy.cn`；Docker 是 **Docker Desktop**，拉镜像走 `docker.m.daocloud.io/pgvector/pgvector:pg16` 后打回标准 tag
- 会话启动链可能残留 `GOROOT`（旧版本）→ vet/build 报 "package cmp is not in std"；`~/.zshrc` 已有 `unset GOROOT` 兜底，脚本/CI 场景用 `env -u GOROOT` 前缀
- 本机 `grep` 被 shell 函数包装为 **ugrep**，与 BSD grep 行为有差异（调试时 `which -a grep`）；无 `psql`/`timeout`——查库用 `docker exec reqflow-pg psql -U reqflow -d reqflow -c "…"`

**代码级**：
- **Go `\s` 不含全角空格 U+3000**（JS 才含）→ `normalize.go` 显式转换，有单测
- **`net/http` FileServer 会把 `/index.html` 请求 301 到 `./`** → `static_embed.go` 手写 `c.Data` 直出，别改回 FileFromFS
- **macOS bash 3.2**：`$()` 内嵌套 `case` 的 `)` 解析报错；`$VAR` 后紧跟全角字符会把高位字节当变量名一部分 → 一律写 `${VAR}`
- GORM `Create(&emptySlice)` 会报错 → 仓储层全部 `len==0` 提前 return；**UUID 列必须仓储层显式赋 `uuid.NewString()`**（GORM Create 带空串写入导致 `invalid input syntax for type uuid`）
- pgvector 参数化查询：`pgvector.NewVector([]float32)` 实现 Valuer，Raw SQL 直接当参数传
- LLM SSE 单行可能极大 → scanner.Buffer 扩到 8MB（`llm/client.go`）
- MinerU 四步解析：**PUT 预签名必须裸字节不带 Content-Type**（会 SignatureDoesNotMatch）
- 工具 Spec 的 Parameters 是 JSON Schema 字符串——手拼容易漏 `"properties":{` 的开括号，非法 JSON 会连带会话序列化与 LLM 请求一起挂；tools_test 有 `json.Valid` 校验防再犯

### 4.6 LLM 层与 pi 的传承（改 loop/协议时保持同步）

port/llm.go 消息模型、infra/llm 双适配器、app/agent loop 与过程工具均移植自 **pi**（https://github.com/earendil-works/pi，MIT License, Copyright (c) 2025 Mario Zechner，源码参考副本在 `/Users/xxxg/demo/pi`）。各文件头注有对应源文件映射。移植要点：

| ReqFlow 位置 | pi 出处 | 移植要点 |
|---|---|---|
| `port/llm.go` | `packages/ai/src/types.ts` | Context{SystemPrompt,Messages,Tools} 全量可序列化；Message 三角色 + text/thinking/toolCall 内容块；StopReason 六态；流事件协议 |
| `infra/llm/openai.go` | `api/openai-completions.ts` | reasoning_content/reasoning/reasoning_text 三字段取首个非空（防重复返回）；tool_calls 按 index 聚合；assistant 回放为纯字符串（块结构会被部分端点镜像导致递归嵌套）；空 content+空 tool_calls 的 assistant 跳过 |
| `infra/llm/anthropic.go` | `api/anthropic-messages.ts` | SSE 事件状态机；thinking 块签名回放（扩展思考+工具调用场景 API 强校验）；连续 toolResult 合并为单条 user 消息的 tool_result 块 |
| `app/agent/loop.go` | `packages/agent/src/agent-loop.ts` | 自然终止（无工具调用即停）；**length 截断的工具调用一律不执行、整批错误回执让模型重发**；error/aborted 短路保留已积累消息；ToolOutput 的 Output(LLM)/Details(UI) 拆分；terminate 语义 |
| `app/tools/read.go` | `packages/coding-agent/src/core/tools/read.ts` | 行号分页（与检索行号咬合）；截断附「用 offset=N 继续读取」行动性提示；单行超限硬拆 + 检索指引（我们无 shell 兜底） |
| `app/tools/search.go` | `packages/coding-agent/src/core/tools/grep.ts` | grep 式 `行号:内容` 纯文本输出；literal 转义 / ignore_case / context 行；命中超限「limit 翻倍或收窄」、行超长「用 read 看整行」的可行动提示 |
| `agent.DocumentedTool` + prompt 装配 | pi 的 promptSnippet/promptGuidelines + agent-session 系统提示组装 | 系统提示词的工具指南从实际工具集组装——工具增删提示词自动跟随（防漂移的结构性解法） |

**有意偏离 pi**（改 loop/协议时保持同步）：
1. 回调事件替代 async iterator；中止走 `ctx`（Go 惯例）
2. `Loop.MaxIterations` 安全阀——loop 层默认 8，分析 agent 模式经 `llm.agent_max_iterations` 提到默认 32（分批阅读大文档需多轮，50k 字 ≈ 10+ 读取轮）；pi 刻意无上限，生产导入必须兜底
3. 缺 finish_reason 的兼容端点按「有无工具调用」**推断**终止（pi 对声明支持 finish_reason 的端点直接报错；我们面向杂牌兼容端点从宽）
4. 不移植：模型注册表/厂商 compat 矩阵/steering 消息队列/beforeToolCall 钩子/prepareNextTurn 换模型/并行工具执行/deferred 响应——需要时按 pi 源码对应段落补

## 5. 接手后干什么（按优先级）

### 5.1 第二波主线：Bug 链路任务化

设计定稿在 `internal/app/bug/doc.go`，落地时**直接以新任务类型接入现有任务系统**（按 PRODUCT §3 范式 + §4 决策二：新类型 = 新步骤链 + 新工具集 + 复用生命周期底座），不再自建 SSE handler。闭环定位：**消费需求数据集 → 产出 Bug 数据集**。按此顺序做：

1. **迁移** `0008_bug.up.sql`（0004-0007 已占用）：`bug_batches`(id,file_name,source_path,status,created_at) / `bug_rows`(id,batch_id,raw_jsonb,编号,标题,描述,复现步骤等归一化字段,analyzed_priority(priority p0-p3),priority_rationale,status) / `bug_matches`(id,bug_row_id,candidate_work_item_id,score,match_type,rank(1-3),human_decision)
2. **port**：`BugRepo` 接口 + DTO
3. **infra/repository**：实现（照抄 task_repo.go 模式：row 结构进 repo.go + 转换器）
4. **app/bug 用例**：`ImportBatch`（`parser.ParseXLSXRows` 已就绪，表头→行映射，去空白/空行跳过/重名列去重已处理）/ `MatchBatch`（**以需求数据集为关联底料**：有编号→`NormalizeForExactMatch` 后与数据集条目标题精确匹配；无编号→复用两层匹配取 **top3**；`task.input_dataset_id` 指定消费哪个需求数据集，缺省查全部） / `ConfirmMatch`（人工确认/否决）/ `Prioritize`（批量 LLM 定级 P0-P3）/ `GenerateDataset`（复用 DatasetWriter 产出 Bug 数据集，关联需求以「关联需求: {标题}」写入字段）。**定级优先复用现有 analyze 步骤的 agent 模式**：注册 BugSchema（字段含 priority/rationale）+ bug 任务的 AnalyzeProfile（指令头换成定级语境，语料 = bug 行集渲染成文本），read/search/write/ask 四工具与提示词装配全部白拿；prompt 要点落在 schema 的 FieldSpec.Prompt（给出理由 rationale、P3 判定保守），不再手写输出契约
5. **任务接入**：`TaskManager` 增加 `bug_import` 任务类型——注册表播种步骤链（Excel 导入→匹配确认门→AI 定级→生成 Bug 数据集）+ 步骤 goroutine 包装（runner.go 同构，StepKind 复用 analyze/dataset/human）；上传/确认/重试语义对齐 requirement_import（失败回门可重试、暂停可续跑、逐条幂等）
6. **前端**：替换 `web/src/pages/Bugs.tsx` 占位 → 任务详情页新增工作区面板（上传→匹配确认表格（top3 候选单选/否决/标无效）→定级面板（可改档）→生成数据集），复用 TaskDetail 步骤时间线 + panels 模式
7. **顺手可做**：embedding 密钥池（多 key 轮询、429 冷却按 Retry-After、401 摘除——只在 `infra/embedding` 内改）；LLM refine 微调会话（分析会话已随任务落库，追加消息重放即得 refine，进程内 map 按任务前缀缓存即可）

### 5.2 agent 工具化演进

**红线（产品级，不可松动）**：读操作工具可自主调用；写持久存储（数据集生成 / 条目写入）不得成为 loop 的自主工具——生成数据集仍由人工在任务门内点击触发（PRODUCT §4 决策四）。write_work_items 写的是内存 DraftSink（草稿），落库仍走人工确认，红线未破。

**当前工具集**（四件套的行为细节见 §3.2 代码地图与 §4.2 流程图）：read_document / search_document / write_work_items（+ DraftSink）/ ask_human，按任务类型 profile 经 `tools.BuildForRun` 注入（WriteSpec 绑定产出 schema）；每个工具实现 `agent.DocumentedTool`，提示词自动进系统提示。

**真机验收持续项**（agent 模式已上线，`llm.agent_mode: true` + 真实 api_key）：
- 长文档（50k+ 字）完整跑通——`llm.agent_max_iterations` 默认 32 是否充足，不同模型对续读提示的跟随度
- write_work_items 回执被拒条目是否被稳定修正重交；ask_human 的提问频率是否克制（滥问 = 指令头/guidelines 需调）
- DeepSeek 等推理模型的 reasoning_content 以 thinking 相位展示；工具调用期间前端有明确进度感

**工具化演进方向**（扩展工具前先读 `tools/read.go` 与 `tools/write.go`）：
- 新工具要点：实现 `agent.DocumentedTool`（提示词自动进系统提示）；参数 Schema 保持极简且**内嵌真实限制常量**（描述与行为同源）；Output 优先纯文本（省 token），结构化回执才用紧凑 JSON；Details 返回人读摘要（前端工具轨迹）；所有截断附「怎么继续」的可行动提示（pi 模式）
- pi 的并行工具执行与 beforeToolCall 审批钩子暂不移植，需要时按 pi 源码 `agent-loop.ts` 对应段落补（见 §4.6 不移植清单）

### 5.3 技术债清单（如实）

| 项 | 影响 | 处置建议 |
|----|------|---------|
| 单发模式宽松恢复链单测偏薄 | agent 主路径已覆盖（全链路/降级/重放/问人往返），classic 的 lenient 恢复分支不全 | 补 classic 路径用例 |
| repository 层无测试 | schema 回归风险 | testcontainers-go 或对本机 docker PG 跑薄集成测试 |
| 查重阈值 0.75 固定 | 新需求被语义层误判「疑似重复」（实测 0.79 误报） | 按数据集类型/场景可配，或加「确认不是重复」负反馈 |
| 向量固定 1024 维 | 换模型要重建库 | 文档已写死流程；如需多维度考虑按维度分表 |
| refine 微调未做 | 分析结果只能重来 | 第二波（§5.1 顺手可做；分析会话已随任务落库，追加消息重放即得） |
| 后端无热重载 | 开发改代码要重启 | 可引入 air，非必须 |
| 前端单 chunk 1.5MB | 首载稍慢 | 按路由 code-split，非紧急 |
| token 增量不落库 | 分析中途重连后思考/正文双区从空开始（工具轨迹从步骤 data 回放，结果以明细为准） | 刻意取舍：防会话膨胀；如确需重放全文再按轮次落库 |
| 上传文件无清理 | 失败/暂停任务的 upload_dir 文件残留 | 终态清理 + 启动扫描兜底 |
| classic 模式续跑重放 | 单发模式暂停后恢复会重放流式调用（同 prompt 重新生成，幂等但耗 token） | 暂停多在 agent 模式（检查点续跑不重放已确认轮次） |
| **草稿字段袋化**（最大的一笔） | DraftItem 仍是 requirement 形状的 struct + task_items 物理列：新任务类型仍需写 struct/Normalize/ValuesOf，草稿字段无法按任务实例变化 | map 字段袋贯通（LLM map → sink 校验 → task_items JSONB → dataset fields），schema 三级解析（类型默认 → 实例覆盖 → 目标数据集继承）；配套 AnalyzeProfile 的 Tools/Corpus 泛化；migration 推倒重建 task_items |

### 5.4 多客户端上线补全（第三波，按顺序）

单实例多客户端当前即支持（架构证据见 PRODUCT §4 决策五）；**上线前**按此顺序补，每项独立可交付：

1. **认证授权**（硬门槛）：登录 + 会话（cookie/session），任务 / 数据集 / 触发端点全部校验；无认证时任何能访问端口的人可看全部任务与数据集、触发所有操作
2. **LLM 并发限流**（稳定性）：信号量限制并发 LLM / embedding 调用 + 排队——多客户端同时触发多任务会并发打上游，上游限流表现为「卡住 / 超时」（已实测撞到过一次慢调用贴近超时上限）；顺带把 `llm.timeout_ms`（当前 300s）在真实网络下评估是否上调
3. **草稿冲突**（质量）：`ReplaceTaskItems` 整批替换无版本控制，两个客户端同时编辑同一任务草稿后写覆盖前写——加版本号乐观锁，冲突显式报错
4. **多实例扩展**（远期，不在当前范围）：Broker 进程内扇出 / running 登记表 / 上传文件本地盘都是单实例绑定——分别换 Redis pub-sub / 分布式锁 / 对象存储

## 6. 运维备忘

- **DB**：`docker compose up -d`；数据卷 `reqflow_pgdata`；连接 `postgres://reqflow:reqflow@127.0.0.1:5432/reqflow`（本机无 psql，用 `docker exec reqflow-pg psql -U reqflow -d reqflow -c "…"`）
- **迁移**：启动时自动跑（`database.auto_migrate: true`），内嵌 SQL 幂等（schema_migrations 表）；新增迁移文件放 `internal/infra/database/migrations/NNNN_*.up.sql`（+ 可选 down）；**研发阶段无历史数据，改表直接推倒重建（DROP 表 + 删 schema_migrations 对应版本行）**
- **重建向量场景**：换 embedding 模型/维度 → 停服 → 清库或改迁移 → 重启 → 重新生成数据集（无全量重建入口，靠任务重跑）
- **SSE 客户端**：每个任务详情页持有一条长连接；token 事件已节流（~7 帧/秒），连接数 = 打开的详情页数，无级联放大
- **日志**：stdout（text/json 可配）；启动告警（配置降级）集中在头几行；SSE 连接断开/重连可据 http 日志的 events 请求 cost_ms 观察
- **升级发布**：`make build` → 替换二进制 + 保留 config.yaml 与 data/ → 重启（自动迁移）

## 7. 联系与上游

- 产品定义 / 方向性决策 / 路线图：`docs/PRODUCT.md` + 本文件
- pi 源码参考副本：`/Users/xxxg/demo/pi`（改 LLM 层/loop/工具时对照；消息模型、双协议适配器、agent loop 与过程工具的移植映射见 §4.6）
- MinerU API：mineru.net 精准解析 v4（限制单文件 ≤200MB、≤200 页）
