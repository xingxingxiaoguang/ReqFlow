package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"reqflow/internal/app/agent"
	"reqflow/internal/app/tools"
	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

// ErrTaskPaused 步骤被暂停（工作 ctx 取消）——TaskManager 据此把任务标为 paused，
// 产出携带已积累的会话检查点（AgentContext）供续跑。
var ErrTaskPaused = errors.New("任务已暂停")

// agentDefaultMaxIterations agent 模式默认迭代上限。分批阅读大文档会消耗多轮
// （50k 字文档 ≈ 10+ 读取轮 + 写入轮），loop 的 8 轮通用安全阀对过程型 agent 太紧。
const agentDefaultMaxIterations = 32

// AnalyzeProgress 分析阶段进度。
type AnalyzeProgress struct {
	Stage   string // analyzing | done
	Message string
}

// AnalyzeDelta 模型输出增量（app 层形状，port 类型不外泄）。
// Phase: "thinking"（推理过程，仅展示）| "answer"（正文，进 JSON 解析缓冲）。
type AnalyzeDelta struct {
	Phase string
	Text  string
}

// AnalyzeToolEvent agent 模式的工具调用轨迹（httpgin 直接作为 SSE tool 事件负载）。
// Phase: "start"（携带 Args）| "end"（携带 Details/IsError）。
type AnalyzeToolEvent struct {
	Phase   string `json:"phase"`
	CallID  string `json:"call_id"`
	Name    string `json:"name"`
	Args    string `json:"args,omitempty"`
	Details string `json:"details,omitempty"`
	IsError bool   `json:"is_error,omitempty"`
}

// AnalyzeOutcome 一轮分析的产出（不含持久化——由 TaskManager 落库）。
type AnalyzeOutcome struct {
	Items        []model.TaskItem // 归一化后的草稿明细（status=pending）
	AgentContext string           // 会话 JSON（port.Context 序列化；暂停时即检查点）
	SourcePath   string           // 原文存档路径（仅成功产出时非空）
}

// AnalyzeInput 一次分析的完整入参（TaskManager 从任务状态组装）。
type AnalyzeInput struct {
	TaskID   string
	TaskType string // 任务类型 → AnalyzeProfileOf 装配（提示词/工具/校验）；空回退 requirement
	FileName string
	Text     string           // 待分析全文（agent 模式下原文不进上下文，经工具阅读）
	Special  string           // 用户附加要求
	Dialog   tools.HumanAsker // 人工交互桥（TaskManager 注入 DialogHub；单测可 nil）
}

// AnalyzeService 需求文档 LLM 分析用例。
//
// agent 模式（llm.agent_mode，pi 工具模式）：文档不进首轮消息，agent 经
// read_document/search_document 自主阅读，经 write_work_items 分批产出草稿
// （终稿契约从「最后一条消息是 JSON 数组」变为写入工具累积），关键决策点可
// ask_human 阻塞问人。loop 失败且无任何产出时降级回单发直调，功能可用性优先。
//
// 单发直调（默认模式，也是降级目标）：流式输出 → 宽松恢复 → 非流式回退。
// 流中断且已积累内容时，优先从部分输出恢复。
//
// 持久化职责在 TaskManager（任务步骤/明细/会话落库）；本用例只产出
// AnalyzeOutcome。暂停（ctx 取消）时返回 ErrTaskPaused + 已积累会话检查点，
// TaskManager 落库后经 Resume 续跑（agent 模式从会话重放草稿状态）。
type AnalyzeService struct {
	llm       port.LLMClient
	demandDir string
	agentCfg  *agent.Config // nil = 单发直调
}

// NewAnalyzeService 构造用例。demandDir 为分析原文存档目录。
func NewAnalyzeService(llm port.LLMClient, demandDir string) *AnalyzeService {
	return &AnalyzeService{llm: llm, demandDir: demandDir}
}

// EnableAgentMode 开启 agent 模式。maxIterations <= 0 时用默认值（32）。
// 工具集按运行构造（文档/草稿 sink/交互桥都是运行期状态），不再静态注入。
func (s *AnalyzeService) EnableAgentMode(maxIterations int) {
	if maxIterations <= 0 {
		maxIterations = agentDefaultMaxIterations
	}
	s.agentCfg = &agent.Config{MaxIterations: maxIterations}
}

// Run 执行分析。onToken 实时回调模型增量（thinking/answer 两相位），
// onProgress 上报阶段变化，onTool 上报 agent 工具调用轨迹（单发模式不触发）。
func (s *AnalyzeService) Run(
	ctx context.Context,
	in AnalyzeInput,
	onProgress func(AnalyzeProgress),
	onToken func(AnalyzeDelta),
	onTool func(AnalyzeToolEvent),
) (*AnalyzeOutcome, error) {
	if onProgress == nil {
		onProgress = func(AnalyzeProgress) {}
	}
	if strings.TrimSpace(in.Text) == "" {
		return nil, fmt.Errorf("待分析文本为空")
	}

	now := time.Now()

	if s.agentCfg != nil {
		out, handled, err := s.runAgent(ctx, in, now, onProgress, onToken, onTool)
		if handled {
			return s.done(out, err, onProgress)
		}
		// agent 链路失败且无任何可恢复输出：降级单发直调
		onProgress(AnalyzeProgress{Stage: "analyzing", Message: "agent 分析链路失败，已回退单发直调…"})
	}

	out, err := s.runClassic(ctx, in, now, onProgress, onToken)
	return s.done(out, err, onProgress)
}

// Resume 从已落库会话（检查点）续跑分析。cc 由 TaskManager 从 task.AgentContext
// 反序列化：agent 模式从会话重放草稿状态后继续 loop；单发模式重放流式调用（幂等）。
// 续跑彻底失败（无输出）时与 Run 同构降级单发。
func (s *AnalyzeService) Resume(
	ctx context.Context,
	cc *port.Context,
	in AnalyzeInput,
	onProgress func(AnalyzeProgress),
	onToken func(AnalyzeDelta),
	onTool func(AnalyzeToolEvent),
) (*AnalyzeOutcome, error) {
	if onProgress == nil {
		onProgress = func(AnalyzeProgress) {}
	}
	if cc == nil {
		return s.Run(ctx, in, onProgress, onToken, onTool)
	}
	if strings.TrimSpace(in.Text) == "" {
		return nil, fmt.Errorf("待分析文本为空")
	}

	now := time.Now()

	profile, err := profileFor(in.TaskType)
	if err != nil {
		return nil, err
	}

	if s.agentCfg != nil {
		sink := tools.NewDraftSink()
		sink.ReplayFrom(cc.Messages, profile.Write) // 会话即事实源：重放全部写入调用重建草稿状态
		toolset := buildToolset(in, sink, profile)
		onProgress(AnalyzeProgress{Stage: "analyzing", Message: "AI 正在继续拆解需求功能点…"})
		loop := agent.New(s.llm, toolset, *s.agentCfg)
		finalCtx, runErr := loop.Run(ctx, cc, tokenMapper(onToken), toolEventMapper(onTool))
		out, handled, err := s.sinkTail(ctx, finalCtx, runErr, sink, in, now, onProgress)
		if handled {
			return s.done(out, err, onProgress)
		}
		onProgress(AnalyzeProgress{Stage: "analyzing", Message: "agent 续跑失败，已回退单发直调…"})
		out, err = s.runClassic(ctx, in, now, onProgress, onToken)
		return s.done(out, err, onProgress)
	}

	// 单发模式：检查点会话（含完整 prompt）重放
	onProgress(AnalyzeProgress{Stage: "analyzing", Message: "AI 正在拆解需求功能点…"})
	msg, streamErr := s.llm.Stream(ctx, cc, tokenMapper(onToken))
	raw := ""
	if msg != nil {
		raw = msg.Text()
		cc.Messages = append(cc.Messages, *msg)
	}
	drafts := parseDrafts(raw)
	if drafts == nil {
		if streamErr != nil {
			if ctx.Err() != nil {
				return s.checkpoint(in, now, cc)
			}
			return nil, fmt.Errorf("LLM 流式分析失败: %w", streamErr)
		}
		onProgress(AnalyzeProgress{Stage: "analyzing", Message: "流式输出解析失败，正在回退非流式调用…"})
		fallback, err := s.llm.Complete(ctx, cc)
		if err != nil {
			if ctx.Err() != nil {
				return s.checkpoint(in, now, cc)
			}
			return nil, fmt.Errorf("LLM 分析失败: %w", err)
		}
		raw = fallback.Text()
		drafts = parseDrafts(raw)
		if drafts == nil {
			return nil, fmt.Errorf("LLM 输出无法解析为工作项数组（原始输出前 200 字: %s）", truncateStr(raw, 200))
		}
		cc.Messages = append(cc.Messages, *fallback)
	} else if streamErr != nil {
		onProgress(AnalyzeProgress{Stage: "analyzing", Message: fmt.Sprintf("流式中断，已从部分输出恢复 %d 个工作项", len(drafts))})
	}
	return s.finalize(in, logic.NormalizeDrafts(drafts, now), now, cc)
}

// runAgent agent 模式主路径。handled=false 表示链路彻底失败应降级单发；
// 其余情况（成功 / 部分产出 / 暂停检查点）由本路径自行收束。
func (s *AnalyzeService) runAgent(
	ctx context.Context,
	in AnalyzeInput,
	now time.Time,
	onProgress func(AnalyzeProgress),
	onToken func(AnalyzeDelta),
	onTool func(AnalyzeToolEvent),
) (*AnalyzeOutcome, bool, error) {
	profile, err := profileFor(in.TaskType)
	if err != nil {
		return nil, true, err
	}
	sink := tools.NewDraftSink()
	toolset := buildToolset(in, sink, profile)
	cc := &port.Context{
		SystemPrompt: renderAgentSystem(now, in.Special, toolset, profile),
		Messages:     []port.Message{port.NewUserMessage(renderDocManifest(tools.DocSource{FileName: in.FileName, Text: in.Text}))},
	}

	onProgress(AnalyzeProgress{Stage: "analyzing", Message: "AI 正在拆解需求功能点（agent 模式：自主阅读文档并分批产出草稿）…"})
	loop := agent.New(s.llm, toolset, *s.agentCfg)
	finalCtx, runErr := loop.Run(ctx, cc, tokenMapper(onToken), toolEventMapper(onTool))
	return s.sinkTail(ctx, finalCtx, runErr, sink, in, now, onProgress)
}

// buildToolset 构造本次运行的工具集（文档/草稿 sink/交互桥按运行注入；
// 写入工具按 profile 的产出 schema 绑定）。
func buildToolset(in AnalyzeInput, sink *tools.DraftSink, profile AnalyzeProfile) []agent.Tool {
	return tools.BuildForRun(tools.RunDeps{
		Doc:    tools.DocSource{FileName: in.FileName, Text: in.Text},
		Sink:   sink,
		TaskID: in.TaskID,
		Ask:    in.Dialog,
		Write:  profile.Write,
	})
}

// sinkTail agent loop 后的统一收尾：草稿来自写入工具的累积（终稿契约不再是 JSON 文本）。
// 暂停（ctx 取消）→ 检查点产出 + ErrTaskPaused（Resume 时 ReplayFrom 重建草稿）；
// sink 空 → handled=false 交外层降级单发；sink 非空 + loop 出错 → 保留产出并告警。
func (s *AnalyzeService) sinkTail(
	ctx context.Context,
	finalCtx *port.Context,
	runErr error,
	sink *tools.DraftSink,
	in AnalyzeInput,
	now time.Time,
	onProgress func(AnalyzeProgress),
) (*AnalyzeOutcome, bool, error) {
	if ctx.Err() != nil {
		// 暂停检查点：会话已积累（消息只在完整轮次后追加），序列化供续跑。
		// 已写入 sink 的草稿不随检查点走——会话里的写入调用可确定性重放。
		out, err := s.finalize(in, nil, now, finalCtx)
		if err != nil {
			return nil, true, err
		}
		return out, true, ErrTaskPaused
	}

	items := sink.Items()
	if len(items) == 0 {
		if runErr != nil {
			return nil, false, runErr // 无产出：交给外层降级单发
		}
		return nil, false, fmt.Errorf("agent 未产出任何草稿（未调用 write_work_items）")
	}
	if runErr != nil {
		// loop 中断但已有产出：保留部分结果，记录告警
		onProgress(AnalyzeProgress{Stage: "analyzing", Message: fmt.Sprintf("agent 链路中断，已保留写入的 %d 个工作项", len(items))})
	}
	out, err := s.finalize(in, items, now, finalCtx)
	return out, true, err
}

// runClassic 单发直调主路径（默认模式，也是 agent 链路失败时的降级目标）。
func (s *AnalyzeService) runClassic(
	ctx context.Context,
	in AnalyzeInput,
	now time.Time,
	onProgress func(AnalyzeProgress),
	onToken func(AnalyzeDelta),
) (*AnalyzeOutcome, error) {
	profile, err := profileFor(in.TaskType)
	if err != nil {
		return nil, err
	}
	// pi Context：单发提取 = 一条 user 消息；会话同样产出（续跑/refine 的载体）
	llmCtx := &port.Context{Messages: []port.Message{port.NewUserMessage(renderAnalyzePrompt(in.Text, now, in.Special, profile))}}

	onProgress(AnalyzeProgress{Stage: "analyzing", Message: "AI 正在拆解需求功能点…"})
	msg, streamErr := s.llm.Stream(ctx, llmCtx, tokenMapper(onToken))
	raw := ""
	if msg != nil {
		raw = msg.Text()
		llmCtx.Messages = append(llmCtx.Messages, *msg)
	}

	// 解析（流失败但有部分输出时先尝试宽松恢复部分结果）
	drafts := parseDrafts(raw)
	if drafts == nil {
		if streamErr != nil {
			if ctx.Err() != nil {
				return s.checkpoint(in, now, llmCtx)
			}
			return nil, fmt.Errorf("LLM 流式分析失败: %w", streamErr)
		}
		// 流式输出解析失败：同一提示词回退非流式一次
		onProgress(AnalyzeProgress{Stage: "analyzing", Message: "流式输出解析失败，正在回退非流式调用…"})
		fallback, err := s.llm.Complete(ctx, llmCtx)
		if err != nil {
			if ctx.Err() != nil {
				return s.checkpoint(in, now, llmCtx)
			}
			return nil, fmt.Errorf("LLM 分析失败: %w", err)
		}
		raw = fallback.Text()
		drafts = parseDrafts(raw)
		if drafts == nil {
			return nil, fmt.Errorf("LLM 输出无法解析为工作项数组（原始输出前 200 字: %s）", truncateStr(raw, 200))
		}
		llmCtx.Messages = append(llmCtx.Messages, *fallback)
	} else if streamErr != nil {
		// 流中断但已恢复出部分结果：继续，但记录告警
		onProgress(AnalyzeProgress{Stage: "analyzing", Message: fmt.Sprintf("流式中断，已从部分输出恢复 %d 个工作项", len(drafts))})
	}

	return s.finalize(in, logic.NormalizeDrafts(drafts, now), now, llmCtx)
}

// checkpoint 暂停检查点产出：序列化已积累会话（续跑载体），不解析草稿。
func (s *AnalyzeService) checkpoint(in AnalyzeInput, now time.Time, llmCtx *port.Context) (*AnalyzeOutcome, error) {
	out, err := s.finalize(in, nil, now, llmCtx)
	if err != nil {
		return nil, err
	}
	return out, ErrTaskPaused
}

// finalize 原文存档 → 组装产出（items 应已归一化；不落库，由 TaskManager 持久化）。
func (s *AnalyzeService) finalize(in AnalyzeInput, items []model.DraftItem, now time.Time, llmCtx *port.Context) (*AnalyzeOutcome, error) {
	out := &AnalyzeOutcome{Items: make([]model.TaskItem, len(items))}
	for i, d := range items {
		out.Items[i] = model.TaskItem{DraftItem: d, Status: model.ItemStatusPending}
	}

	// 原文存档只在成功产出时落盘（检查点不写，避免暂停产生孤儿文件）
	if len(items) > 0 {
		sp, err := s.saveDemand(in.FileName, in.Text)
		if err != nil {
			return nil, err
		}
		out.SourcePath = sp
	}

	// 会话序列化（Context 即会话）：暂停续跑与 refine 微调的统一载体
	if sessionJSON, err := json.Marshal(llmCtx); err == nil {
		out.AgentContext = string(sessionJSON)
	}
	return out, nil
}

// done 成功收尾的统一进度上报。
func (s *AnalyzeService) done(out *AnalyzeOutcome, err error, onProgress func(AnalyzeProgress)) (*AnalyzeOutcome, error) {
	if err == nil && out != nil {
		onProgress(AnalyzeProgress{Stage: "done", Message: fmt.Sprintf("分析完成，共 %d 个工作项", len(out.Items))})
	}
	return out, err
}

/* ---- 私有 ---- */

// tokenMapper 流事件 → AnalyzeDelta（thinking/answer 两相位）。
func tokenMapper(onToken func(AnalyzeDelta)) func(port.AssistantEvent) {
	return func(ev port.AssistantEvent) {
		if onToken == nil {
			return
		}
		switch ev.Type {
		case port.EventThinkingDelta:
			onToken(AnalyzeDelta{Phase: "thinking", Text: ev.Delta})
		case port.EventTextDelta:
			onToken(AnalyzeDelta{Phase: "answer", Text: ev.Delta})
		}
	}
}

// toolEventMapper loop 事件 → AnalyzeToolEvent（仅工具执行两类；参数截断只做展示）。
func toolEventMapper(onTool func(AnalyzeToolEvent)) func(agent.Event) {
	return func(ev agent.Event) {
		if onTool == nil {
			return
		}
		switch ev.Type {
		case "tool_execution_start":
			onTool(AnalyzeToolEvent{Phase: "start", CallID: ev.ToolCallID, Name: ev.ToolName, Args: truncateStr(string(ev.Args), 200)})
		case "tool_execution_end":
			onTool(AnalyzeToolEvent{Phase: "end", CallID: ev.ToolCallID, Name: ev.ToolName, Details: ev.Output.Details, IsError: ev.Output.IsError})
		}
	}
}

// parseDrafts 标准解析失败时走宽松恢复；两者都无法得到非空数组返回 nil。
func parseDrafts(raw string) []any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var arr []any
	if err := json.Unmarshal([]byte(raw), &arr); err == nil && len(arr) > 0 {
		return arr
	}
	return logic.ExtractJSONArrayLenient(raw)
}

var unsafeFileName = regexp.MustCompile(`[\\/:*?"<>|\s]+`)

// saveDemand 存档用户确认后的分析原文（文件名净化 + 时间戳防冲突）。
func (s *AnalyzeService) saveDemand(fileName, text string) (string, error) {
	if s.demandDir == "" {
		return "", nil
	}
	base := unsafeFileName.ReplaceAllString(strings.TrimSuffix(fileName, filepath.Ext(fileName)), "_")
	if base == "" {
		base = "未命名文档"
	}
	if len(base) > 80 {
		base = base[:80]
	}
	name := fmt.Sprintf("%s-%d.txt", base, time.Now().UnixMilli())
	if err := os.MkdirAll(s.demandDir, 0o755); err != nil {
		return "", err
	}
	full := filepath.Join(s.demandDir, name)
	if err := os.WriteFile(full, []byte(text), 0o644); err != nil {
		return "", err
	}
	return full, nil
}

func truncateStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
