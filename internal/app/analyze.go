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
	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

// ErrTaskPaused 步骤被暂停（工作 ctx 取消）——TaskManager 据此把任务标为 paused，
// 产出携带已积累的会话检查点（AgentContext）供续跑。
var ErrTaskPaused = errors.New("任务已暂停")

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

// AnalyzeService 需求文档 LLM 分析用例：流式输出 → 宽松恢复 → 白名单归一化。
//
// 解析降级链（对齐 PingCraft 实践，避免整段重跑的 token 成本翻倍）：
// 标准解析 → 剥围栏/截取数组/修复截断的宽松恢复 → 非流式回退。
// 流中断且已积累内容时，优先从部分输出恢复。
//
// agent 模式（llm.agent_mode，HANDOVER §12）：EnableAgentMode 注入工具后，
// 分析从单发直调升级为「分析 → 自主查证 → 终稿」的 agent.Loop；
// loop 失败且无任何输出时降级回单发直调，保证功能可用性优先。
//
// 持久化职责已移交 TaskManager（任务步骤/明细/会话落库）；本用例只产出
// AnalyzeOutcome。暂停（ctx 取消）时返回 ErrTaskPaused + 已积累会话检查点，
// TaskManager 落库后经 Resume 续跑。
type AnalyzeService struct {
	llm       port.LLMClient
	demandDir string
	loop      *agent.Loop // nil = 单发直调
}

// NewAnalyzeService 构造用例。demandDir 为分析原文存档目录。
func NewAnalyzeService(llm port.LLMClient, demandDir string) *AnalyzeService {
	return &AnalyzeService{llm: llm, demandDir: demandDir}
}

// EnableAgentMode 注入 agent loop 工具集（llm.agent_mode 开启时由组装点调用）。
// maxIterations <= 0 时用 loop 默认值（8）。
func (s *AnalyzeService) EnableAgentMode(tools []agent.Tool, maxIterations int) {
	s.loop = agent.New(s.llm, tools, agent.Config{MaxIterations: maxIterations})
}

// Run 执行流式分析。onToken 实时回调模型增量（thinking/answer 两相位），
// onProgress 上报阶段变化，onTool 上报 agent 工具调用轨迹（单发模式不触发）。
func (s *AnalyzeService) Run(
	ctx context.Context,
	fileName, text, specialReqs string,
	onProgress func(AnalyzeProgress),
	onToken func(AnalyzeDelta),
	onTool func(AnalyzeToolEvent),
) (*AnalyzeOutcome, error) {
	if onProgress == nil {
		onProgress = func(AnalyzeProgress) {}
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("待分析文本为空")
	}

	now := time.Now()

	if s.loop != nil {
		out, handled, err := s.runAgent(ctx, fileName, text, specialReqs, now, onProgress, onToken, onTool)
		if handled {
			return s.done(out, err, onProgress)
		}
		// agent 链路失败且无任何可恢复输出：降级单发直调
		onProgress(AnalyzeProgress{Stage: "analyzing", Message: "agent 分析链路失败，已回退单发直调…"})
	}

	out, err := s.runClassic(ctx, fileName, text, specialReqs, now, onProgress, onToken)
	return s.done(out, err, onProgress)
}

// Resume 从已落库会话（检查点）续跑分析。cc 由 TaskManager 从 task.AgentContext
// 反序列化：agent 模式从检查点继续 loop；单发模式重放流式调用（幂等）。
// 续跑彻底失败（无输出）时与 Run 同构降级单发。
func (s *AnalyzeService) Resume(
	ctx context.Context,
	cc *port.Context,
	fileName, text, specialReqs string,
	onProgress func(AnalyzeProgress),
	onToken func(AnalyzeDelta),
	onTool func(AnalyzeToolEvent),
) (*AnalyzeOutcome, error) {
	if onProgress == nil {
		onProgress = func(AnalyzeProgress) {}
	}
	if cc == nil {
		return s.Run(ctx, fileName, text, specialReqs, onProgress, onToken, onTool)
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("待分析文本为空")
	}

	now := time.Now()

	if s.loop != nil {
		onProgress(AnalyzeProgress{Stage: "analyzing", Message: "AI 正在继续拆解需求功能点…"})
		finalCtx, runErr := s.loop.Run(ctx, cc, tokenMapper(onToken), toolEventMapper(onTool))
		out, handled, err := s.loopTail(ctx, finalCtx, runErr, fileName, text, now, onProgress)
		if handled {
			return s.done(out, err, onProgress)
		}
		onProgress(AnalyzeProgress{Stage: "analyzing", Message: "agent 续跑失败，已回退单发直调…"})
		out, err = s.runClassic(ctx, fileName, text, specialReqs, now, onProgress, onToken)
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
				return s.checkpoint(fileName, text, now, cc)
			}
			return nil, fmt.Errorf("LLM 流式分析失败: %w", streamErr)
		}
		onProgress(AnalyzeProgress{Stage: "analyzing", Message: "流式输出解析失败，正在回退非流式调用…"})
		fallback, err := s.llm.Complete(ctx, cc)
		if err != nil {
			if ctx.Err() != nil {
				return s.checkpoint(fileName, text, now, cc)
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
	return s.finalize(fileName, text, drafts, now, cc)
}

// runAgent agent 模式主路径。handled=false 表示链路彻底失败应降级单发；
// 其余情况（成功 / 部分恢复 / 暂停检查点 / 可回退的解析失败）由本路径自行收束。
func (s *AnalyzeService) runAgent(
	ctx context.Context,
	fileName, text, specialReqs string,
	now time.Time,
	onProgress func(AnalyzeProgress),
	onToken func(AnalyzeDelta),
	onTool func(AnalyzeToolEvent),
) (*AnalyzeOutcome, bool, error) {
	llmCtx := &port.Context{
		SystemPrompt: renderAgentSystem(now, specialReqs),
		Messages:     []port.Message{port.NewUserMessage(renderDocMessage(text))},
	}

	onProgress(AnalyzeProgress{Stage: "analyzing", Message: "AI 正在拆解需求功能点（agent 模式：可自主查证项目与工作项）…"})
	finalCtx, runErr := s.loop.Run(ctx, llmCtx, tokenMapper(onToken), toolEventMapper(onTool))
	return s.loopTail(ctx, finalCtx, runErr, fileName, text, now, onProgress)
}

// loopTail agent loop 后的统一收尾：解析终稿 → 失败时非流式回退一次 → 组装产出。
// 暂停（ctx 取消）时返回检查点产出 + ErrTaskPaused；无任何输出时 handled=false 交外层降级。
func (s *AnalyzeService) loopTail(
	ctx context.Context,
	finalCtx *port.Context,
	runErr error,
	fileName, text string,
	now time.Time,
	onProgress func(AnalyzeProgress),
) (*AnalyzeOutcome, bool, error) {
	if ctx.Err() != nil {
		// 暂停检查点：会话已积累（消息只在完整轮次后追加），序列化供续跑
		out, err := s.finalize(fileName, text, nil, now, finalCtx)
		if err != nil {
			return nil, true, err
		}
		return out, true, ErrTaskPaused
	}

	raw := lastAssistantText(finalCtx)
	drafts := parseDrafts(raw)
	if drafts == nil {
		if raw == "" && runErr != nil {
			return nil, false, runErr // 无输出：交给外层降级单发
		}
		if runErr == nil {
			// 终稿解析失败：与单发模式同构，带完整会话非流式回退一次
			onProgress(AnalyzeProgress{Stage: "analyzing", Message: "流式输出解析失败，正在回退非流式调用…"})
			fallback, err := s.llm.Complete(ctx, finalCtx)
			if err != nil {
				if ctx.Err() != nil {
					out, err := s.finalize(fileName, text, nil, now, finalCtx)
					if err != nil {
						return nil, true, err
					}
					return out, true, ErrTaskPaused
				}
				return nil, true, fmt.Errorf("LLM 分析失败: %w", err)
			}
			drafts = parseDrafts(fallback.Text())
			if drafts == nil {
				return nil, true, fmt.Errorf("LLM 输出无法解析为工作项数组（原始输出前 200 字: %s）", truncateStr(fallback.Text(), 200))
			}
			finalCtx.Messages = append(finalCtx.Messages, *fallback)
		} else {
			// 有部分输出但宽松恢复失败：如实报错（含 loop 失败原因）
			return nil, true, fmt.Errorf("agent 分析失败: %w（部分输出前 200 字: %s）", runErr, truncateStr(raw, 200))
		}
	} else if runErr != nil {
		// loop 中断但已恢复出部分结果：继续，但记录告警
		onProgress(AnalyzeProgress{Stage: "analyzing", Message: fmt.Sprintf("agent 链路中断，已从部分输出恢复 %d 个工作项", len(drafts))})
	}

	out, err := s.finalize(fileName, text, drafts, now, finalCtx)
	return out, true, err
}

// runClassic 单发直调主路径（默认模式，也是 agent 链路失败时的降级目标）。
func (s *AnalyzeService) runClassic(
	ctx context.Context,
	fileName, text, specialReqs string,
	now time.Time,
	onProgress func(AnalyzeProgress),
	onToken func(AnalyzeDelta),
) (*AnalyzeOutcome, error) {
	// pi Context：单发提取 = 一条 user 消息；会话同样产出（续跑/refine 的载体）
	llmCtx := &port.Context{Messages: []port.Message{port.NewUserMessage(renderAnalyzePrompt(text, now, specialReqs))}}

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
				return s.checkpoint(fileName, text, now, llmCtx)
			}
			return nil, fmt.Errorf("LLM 流式分析失败: %w", streamErr)
		}
		// 流式输出解析失败：同一提示词回退非流式一次
		onProgress(AnalyzeProgress{Stage: "analyzing", Message: "流式输出解析失败，正在回退非流式调用…"})
		fallback, err := s.llm.Complete(ctx, llmCtx)
		if err != nil {
			if ctx.Err() != nil {
				return s.checkpoint(fileName, text, now, llmCtx)
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

	return s.finalize(fileName, text, drafts, now, llmCtx)
}

// checkpoint 暂停检查点产出：序列化已积累会话（续跑载体），不解析草稿。
func (s *AnalyzeService) checkpoint(fileName, text string, now time.Time, llmCtx *port.Context) (*AnalyzeOutcome, error) {
	out, err := s.finalize(fileName, text, nil, now, llmCtx)
	if err != nil {
		return nil, err
	}
	return out, ErrTaskPaused
}

// finalize 白名单归一化 → 原文存档 → 组装产出（不落库；由 TaskManager 持久化）。
func (s *AnalyzeService) finalize(fileName, text string, drafts []any, now time.Time, llmCtx *port.Context) (*AnalyzeOutcome, error) {
	items := make([]model.TaskItem, len(drafts))
	for i, d := range logic.NormalizeDrafts(drafts, now) {
		items[i] = model.TaskItem{DraftItem: d, Status: model.ItemStatusPending}
	}

	// 原文存档只在成功产出时落盘（检查点不写，避免暂停产生孤儿文件）
	sourcePath := ""
	if len(drafts) > 0 {
		sp, err := s.saveDemand(fileName, text)
		if err != nil {
			return nil, err
		}
		sourcePath = sp
	}

	// 会话序列化（Context 即会话）：暂停续跑与 refine 微调的统一载体
	out := &AnalyzeOutcome{Items: items, SourcePath: sourcePath}
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

// lastAssistantText 取会话中最后一条 assistant 消息的正文（agent 终稿）。
func lastAssistantText(cc *port.Context) string {
	if cc == nil {
		return ""
	}
	for i := len(cc.Messages) - 1; i >= 0; i-- {
		if cc.Messages[i].Role == port.RoleAssistant {
			return cc.Messages[i].Text()
		}
	}
	return ""
}

// renderAnalyzePrompt 单发模式：指令头 + 文档节拼为完整 prompt（一条 user 消息）。
func renderAnalyzePrompt(text string, now time.Time, special string) string {
	head := renderPromptHead(now, special)
	return head + "\n\n" + strings.ReplaceAll(analyzeDocSection, "{text}", text)
}

// renderPromptHead 渲染指令头（填充时间与额外要求占位符）。
func renderPromptHead(now time.Time, special string) string {
	p := strings.ReplaceAll(analyzePromptHead, "{current_time}", now.Format(time.RFC3339))
	return strings.ReplaceAll(p, "{special_requirements_section}", buildSpecialSection(special))
}

// renderAgentSystem agent 模式 SystemPrompt：指令头 + 工具使用指南。
func renderAgentSystem(now time.Time, special string) string {
	return renderPromptHead(now, special) + "\n\n" + agentToolGuidance
}

// renderDocMessage 文档节（agent 模式下独占首轮 user 消息，原文只出现一次）。
func renderDocMessage(text string) string {
	return strings.ReplaceAll(analyzeDocSection, "{text}", text)
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
