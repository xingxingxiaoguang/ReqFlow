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
// 扩展点：微调重分析（refine）在此服务上以会话 ID 复用消息序列实现，
// 第一波不开放；接入时新增 RunRefine 方法即可，不影响现有签名。
type AnalyzeService struct {
	llm        port.LLMClient
	records    port.ImportRepo
	demandDir  string
}

// NewAnalyzeService 构造用例。demandDir 为分析原文存档目录。
func NewAnalyzeService(llm port.LLMClient, records port.ImportRepo, demandDir string) *AnalyzeService {
	return &AnalyzeService{llm: llm, records: records, demandDir: demandDir}
}

// Run 执行流式分析。onToken 实时回调模型增量（thinking/answer 两相位），
// onProgress 上报阶段变化。
func (s *AnalyzeService) Run(
	ctx context.Context,
	fileName, text, specialReqs string,
	onProgress func(AnalyzeProgress),
	onToken func(AnalyzeDelta),
) (*AnalyzeResult, error) {
	if onProgress == nil {
		onProgress = func(AnalyzeProgress) {}
	}
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("待分析文本为空")
	}

	now := time.Now()
	prompt := renderAnalyzePrompt(text, now, specialReqs)

	onProgress(AnalyzeProgress{Stage: "analyzing", Message: "AI 正在拆解需求功能点…"})
	raw, streamErr := s.llm.StreamChat(ctx, prompt, func(d port.StreamDelta) {
		if onToken != nil {
			onToken(AnalyzeDelta{Phase: string(d.Phase), Text: d.Text})
		}
	})

	// 解析（流失败但有部分输出时先尝试宽松恢复部分结果）
	drafts := parseDrafts(raw)
	if drafts == nil {
		if streamErr != nil {
			return nil, fmt.Errorf("LLM 流式分析失败: %w", streamErr)
		}
		// 流式输出解析失败：同一提示词回退非流式一次
		onProgress(AnalyzeProgress{Stage: "analyzing", Message: "流式输出解析失败，正在回退非流式调用…"})
		fallback, err := s.llm.Chat(ctx, prompt)
		if err != nil {
			return nil, fmt.Errorf("LLM 分析失败: %w", err)
		}
		raw = fallback
		drafts = parseDrafts(fallback)
		if drafts == nil {
			return nil, fmt.Errorf("LLM 输出无法解析为工作项数组（原始输出前 200 字: %s）", truncateStr(raw, 200))
		}
	} else if streamErr != nil {
		// 流中断但已恢复出部分结果：继续，但记录告警
		onProgress(AnalyzeProgress{Stage: "analyzing", Message: fmt.Sprintf("流式中断，已从部分输出恢复 %d 个工作项", len(drafts))})
	}

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
	if err := s.records.CreateRecord(ctx, rec); err != nil {
		return nil, err
	}
	for i := range items {
		items[i].RecordID = rec.ID
	}
	if err := s.records.ReplaceRecordItems(ctx, rec.ID, items); err != nil {
		return nil, err
	}

	onProgress(AnalyzeProgress{Stage: "done", Message: fmt.Sprintf("分析完成，共 %d 个工作项", len(items))})
	return &AnalyzeResult{Record: rec, Items: items}, nil
}

/* ---- 私有 ---- */

func renderAnalyzePrompt(text string, now time.Time, special string) string {
	p := strings.ReplaceAll(analyzePromptTemplate, "{current_time}", now.Format(time.RFC3339))
	p = strings.ReplaceAll(p, "{special_requirements_section}", buildSpecialSection(special))
	return strings.ReplaceAll(p, "{text}", text)
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
