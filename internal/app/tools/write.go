package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"reqflow/internal/app/agent"
	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

/* ---- DraftSink：草稿累积器 ---- */

// DraftSink write_work_items 的写入目标（内存态，不落任何持久存储）。
// key = 归一化 title + 归一化 project_name（对齐数据集 ItemKey 语义：同组同名视为同一条），
// 同 key 覆盖早前写入（模型可修订），首插顺序保留。会话重放可完整重建（ReplayFrom）。
type DraftSink struct {
	order []string
	items map[string]model.DraftItem
}

func NewDraftSink() *DraftSink {
	return &DraftSink{items: map[string]model.DraftItem{}}
}

func draftKey(d model.DraftItem) string {
	return logic.NormalizeForExactMatch(d.Title) + "\x1f" + logic.NormalizeForExactMatch(d.ProjectName)
}

// Upsert 写入一条：key 已存在则覆盖并返回 false（修订），否则追加并返回 true（新增）。
// loop 顺序执行工具，无并发写。
func (s *DraftSink) Upsert(d model.DraftItem) bool {
	k := draftKey(d)
	_, exists := s.items[k]
	if !exists {
		s.order = append(s.order, k)
	}
	s.items[k] = d
	return !exists
}

// ReplaceAll 清空后整体写入（pi write 的整体重写语义）。
func (s *DraftSink) ReplaceAll(ds []model.DraftItem) {
	s.order = s.order[:0]
	s.items = map[string]model.DraftItem{}
	for _, d := range ds {
		s.Upsert(d)
	}
}

// Items 按首插顺序返回全部草稿。
func (s *DraftSink) Items() []model.DraftItem {
	out := make([]model.DraftItem, 0, len(s.order))
	for _, k := range s.order {
		out = append(out, s.items[k])
	}
	return out
}

func (s *DraftSink) Len() int { return len(s.order) }

// ReplayFrom 按发生顺序重放会话中全部 write_work_items 调用，重建草稿状态。
// 会话即事实源：assistant 消息的 toolCall 块携带完整参数，重放确定性成立。
func (s *DraftSink) ReplayFrom(msgs []port.Message) {
	for _, m := range msgs {
		if m.Role != port.RoleAssistant {
			continue
		}
		for _, b := range m.Content {
			if b.Type != port.BlockToolCall || b.ToolCall == nil || b.ToolCall.Name != "write_work_items" {
				continue
			}
			s.applyArgs(b.ToolCall.Arguments, time.Now())
		}
	}
}

/* ---- 写入工具 ---- */

// writeWorkItemsTool 分批产出结构化草稿（本次分析的最终产出全部经此工具提交）。
type writeWorkItemsTool struct{ sink *DraftSink }

type writeReceipt struct {
	Accepted     int            `json:"accepted"`
	Updated      int            `json:"updated"`
	TotalInDraft int            `json:"total_in_draft"`
	Rejected     []rejectDetail `json:"rejected,omitempty"`
}

type rejectDetail struct {
	Index int    `json:"index"`
	Error string `json:"error"`
}

func (t *writeWorkItemsTool) Spec() port.ToolSpec {
	return port.ToolSpec{
		Name:        "write_work_items",
		Description: "分批写入工作项草稿（本次分析的最终产出全部经此工具提交，不要在最终回复里输出 JSON）。items 为草稿对象数组，字段遵循系统提示的输出要求；同一批可包含多条。回执报告每条的接受/修订/拒绝情况，被拒条目修正后重新提交即可（同项目同名的草稿再次写入会覆盖早前版本）。",
		Parameters: json.RawMessage(`{"type":"object","properties":{` +
			`"items":{"type":"array","description":"工作项草稿数组（字段见系统提示的输出要求）","items":{"type":"object"}},` +
			`"replace_all":{"type":"boolean","description":"true 时先清空已写入的全部草稿再写入本批（整体重写）"}` +
			`,"required":["items"]}}`),
	}
}

func (t *writeWorkItemsTool) Execute(_ context.Context, call port.ToolCall, _ func(string)) agent.ToolOutput {
	if t.sink == nil {
		return errOutput("草稿累积器未初始化")
	}
	r, err := applyWriteArgs(t.sink, call.Arguments, time.Now())
	if err != nil {
		return errOutput("%v", err)
	}
	isErr := len(r.Rejected) > 0 && r.Accepted+r.Updated == 0 // 全拒才算失败；部分拒绝靠回执引导修正
	detail := fmt.Sprintf("write_work_items：新增 %d、修订 %d（累计 %d）", r.Accepted, r.Updated, r.TotalInDraft)
	if len(r.Rejected) > 0 {
		detail += fmt.Sprintf("，被拒 %d", len(r.Rejected))
	}
	return agent.ToolOutput{Output: compactJSON(r), Details: detail, IsError: isErr}
}

func (t *writeWorkItemsTool) PromptSnippet() string {
	return "write_work_items：分批写入工作项草稿（本次分析的最终产出全部经此工具提交）"
}

func (t *writeWorkItemsTool) PromptGuidelines() []string {
	return []string{
		"边读边写：消化一批就写入一批，避免拖到最后一次性输出超长内容",
		"回执中 rejected 的条目须修正后重新提交；同项目同名的草稿再次写入会覆盖早前版本，可用于修订",
		"全部草稿写入完成后，最终回复只需简短总结（条数、项目分组、存疑点），不要再输出 JSON",
	}
}

// applyWriteArgs 解析并应用一次写入调用（工具执行与会话重放共用）。
func applyWriteArgs(sink *DraftSink, raw json.RawMessage, now time.Time) (*writeReceipt, error) {
	var args struct {
		Items      []map[string]any `json:"items"`
		ReplaceAll bool             `json:"replace_all"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, fmt.Errorf("参数解析失败: %v", err)
		}
	}
	if len(args.Items) == 0 {
		return nil, fmt.Errorf("items 不能为空")
	}
	if args.ReplaceAll {
		sink.ReplaceAll(nil)
	}
	r := &writeReceipt{}
	for i, item := range args.Items {
		if err := validateDraft(item); err != nil {
			r.Rejected = append(r.Rejected, rejectDetail{Index: i, Error: err.Error()})
			continue
		}
		if sink.Upsert(logic.NormalizeDraft(item, now)) {
			r.Accepted++
		} else {
			r.Updated++
		}
	}
	r.TotalInDraft = sink.Len()
	return r, nil
}

func (s *DraftSink) applyArgs(raw json.RawMessage, now time.Time) {
	_, _ = applyWriteArgs(s, raw, now) // 重放：忽略回执与参数级错误（与执行时一致地跳过）
}

// validateDraft 写入前校验（复用数据集 schema 的必填/枚举规则，给模型即时反馈）。
func validateDraft(item map[string]any) error {
	if err := logic.ValidateValues(model.RequirementSchema(), item); err != nil {
		return err
	}
	if v, ok := item["estimated_hours"]; ok && v != nil {
		if n, parsed := asNumber(v); !parsed || math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 {
			return fmt.Errorf("estimated_hours 必须为正数（小时）")
		}
	}
	return nil
}

// asNumber 数字参数宽松解析（JSON number 或可解析字符串，与 NormalizeDraft 口径一致）。
func asNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case string:
		var f float64
		if _, err := fmt.Sscanf(strings.TrimSpace(n), "%g", &f); err == nil {
			return f, true
		}
	}
	return 0, false
}
