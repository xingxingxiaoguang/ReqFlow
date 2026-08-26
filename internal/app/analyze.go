package app

import (
	"context"
	"encoding/json"
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

// AnalyzeResult 一轮分析的产出：导入记录 + 结构化草稿明细。
type AnalyzeResult struct {
	Record *model.ImportRecord
	Items  []model.ImportRecordItem
}

// AnalyzeService 需求文档 LLM 分析用例：流式输出 → 宽松恢复 → 白名单归一化 → 落库。
//
// 解析降级链（对齐 PingCraft 实践，避免整段重跑的 token 成本翻倍）：
// 标准解析 → 剥围栏/截取数组/修复截断的宽松恢复 → 非流式回退。
// 流中断且已积累内容时，优先从部分输出恢复。
//
// agent 模式（llm.agent_mode，HANDOVER §12）：EnableAgentMode 注入工具后，
// 分析从单发直调升级为「分析 → 自主查证 → 终稿」的 agent.Loop；
// loop 失败且无任何输出时降级回单发直调，保证功能可用性优先。
//
// 扩展点：微调重分析（refine）以落库的 AgentContext（Context 的 JSON 序列化）
// 为载体追加消息重放实现，第一波不开放。
type AnalyzeService struct {
	llm       port.LLMClient
	records   port.ImportRepo
	demandDir string
	loop      *agent.Loop // nil = 单发直调
}

// NewAnalyzeService 构造用例。demandDir 为分析原文存档目录。
func NewAnalyzeService(llm port.LLMClient, records port.ImportRepo, demandDir string) *AnalyzeService {
	return &AnalyzeService{llm: llm, records: records, demandDir: demandDir}
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
) (*AnalyzeResult, error) {
	if onProgress == nil {
		onProgress = func(AnalyzeProgress) {}
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("待分析文本为空")
	}

	now := time.Now()

	done := func(res *AnalyzeResult, err error) (*AnalyzeResult, error) {
		if err == nil {
			onProgress(AnalyzeProgress{Stage: "done", Message: fmt.Sprintf("分析完成，共 %d 个工作项", len(res.Items))})
		}
		return res, err
	}

	if s.loop != nil {
		res, handled, err := s.runAgent(ctx, fileName, text, specialReqs, now, onProgress, onToken, onTool)
		if handled {
			return done(res, err)
		}
		// agent 链路失败且无任何可恢复输出：降级单发直调
		onProgress(AnalyzeProgress{Stage: "analyzing", Message: "agent 分析链路失败，已回退单发直调…"})
	}

	return done(s.runClassic(ctx, fileName, text, specialReqs, now, onProgress, onToken))
}

// runAgent agent 模式主路径。handled=false 表示链路彻底失败应降级单发；
// 其余情况（成功 / 部分恢复 / 可回退的解析失败）由本路径自行收束。
func (s *AnalyzeService) runAgent(
	ctx context.Context,
	fileName, text, specialReqs string,
	now time.Time,
	onProgress func(AnalyzeProgress),
	onToken func(AnalyzeDelta),
	onTool func(AnalyzeToolEvent),
) (*AnalyzeResult, bool, error) {
	llmCtx := &port.Context{
		SystemPrompt: renderAgentSystem(now, specialReqs),
		Messages:     []port.Message{port.NewUserMessage(renderDocMessage(text))},
	}

	onProgress(AnalyzeProgress{Stage: "analyzing", Message: "AI 正在拆解需求功能点（agent 模式：可自主查证项目与工作项）…"})
	finalCtx, runErr := s.loop.Run(ctx, llmCtx, tokenMapper(onToken), toolEventMapper(onTool))

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

	res, err := s.persistResult(ctx, fileName, text, drafts, now, finalCtx)
	return res, true, err
}

// runClassic 单发直调主路径（默认模式，也是 agent 链路失败时的降级目标）。
func (s *AnalyzeService) runClassic(
	ctx context.Context,
	fileName, text, specialReqs string,
	now time.Time,
	onProgress func(AnalyzeProgress),
	onToken func(AnalyzeDelta),
) (*AnalyzeResult, error) {
	// pi Context：单发提取 = 一条 user 消息；会话同样落库（refine 的载体）
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
			return nil, fmt.Errorf("LLM 流式分析失败: %w", streamErr)
		}
		// 流式输出解析失败：同一提示词回退非流式一次
		onProgress(AnalyzeProgress{Stage: "analyzing", Message: "流式输出解析失败，正在回退非流式调用…"})
		fallback, err := s.llm.Complete(ctx, llmCtx)
		if err != nil {
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

	return s.persistResult(ctx, fileName, text, drafts, now, llmCtx)
}

// persistResult 白名单归一化 → 原文存档 → 记录（含会话序列化）与明细落库。
func (s *AnalyzeService) persistResult(
	ctx context.Context,
	fileName, text string,
	drafts []any,
	now time.Time,
	llmCtx *port.Context,
) (*AnalyzeResult, error) {
	items := make([]model.ImportRecordItem, len(drafts))
	for i, d := range logic.NormalizeDrafts(drafts, now) {
		items[i] = model.ImportRecordItem{DraftItem: d, Status: model.ItemStatusPending}
	}

	// 原文存档 + 记录落库
	sourcePath, err := s.saveDemand(fileName, text)
	if err != nil {
		return nil, err
	}
	rec := &model.ImportRecord{
		FileName:         fileName,
		OriginalFilePath: sourcePath,
		Status:           model.RecordStatusAnalyzed,
		ItemsCount:       len(items),
	}
	// 会话序列化（Context 即会话）：refine 微调与换模型续跑的统一载体
	if sessionJSON, err := json.Marshal(llmCtx); err == nil {
		rec.AgentContext = string(sessionJSON)
	}
	if err := s.records.CreateRecord(ctx, rec); err != nil {
		return nil, err
	}
	for i := range items {
		items[i].RecordID = rec.ID
	}
	if err := s.records.ReplaceRecordItems(ctx, rec.ID, items); err != nil {
		return nil, err
	}
	return &AnalyzeResult{Record: rec, Items: items}, nil
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
