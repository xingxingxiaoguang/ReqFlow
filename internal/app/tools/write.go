package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"reqflow/internal/app/agent"
	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

/* ---- DraftSink：草稿累积器 ---- */

// WriteSpec 写入工具与任务产出 schema 的绑定（按任务类型的 profile 实例化）。
// 校验（ValidateValues）与归一化（NormalizeValues）都由此驱动——写入契约与 schema 同源。
type WriteSpec struct {
	Name   string              // 工具名（会话重放识别）
	Schema model.DatasetSchema // 产出 schema（校验 + 归一化 + 提示词渲染）
}

// DefaultWriteSpec requirement 默认绑定（RunDeps/ReplayFrom 零值兜底）。
func DefaultWriteSpec() WriteSpec {
	return WriteSpec{
		Name:   "write_work_items",
		Schema: model.RequirementSchema(),
	}
}

func (w WriteSpec) orDefault() WriteSpec {
	if w.Name == "" || w.Schema.Type == "" {
		return DefaultWriteSpec()
	}
	return w
}

// DraftSink 写入工具的写入目标（内存态，不落任何持久存储）。
// 草稿为 schema 字段袋（map）；key = ItemKeyOf(schema, values)——与数据集条目
// 身份同一函数同一口径（"同 key 同条目"），同 key 覆盖早前写入（模型可修订），
// 首插顺序保留。会话重放可完整重建（ReplayFrom）。
type DraftSink struct {
	order []string
	items map[string]map[string]any
}

func NewDraftSink() *DraftSink {
	return &DraftSink{items: map[string]map[string]any{}}
}

// Upsert 写入一条字段袋：key 已存在则覆盖并返回 false（修订），否则追加并返回 true（新增）。
// loop 顺序执行工具，无并发写。
func (s *DraftSink) Upsert(schema model.DatasetSchema, values map[string]any) bool {
	k := logic.ItemKeyOf(schema, values)
	_, exists := s.items[k]
	if !exists {
		s.order = append(s.order, k)
	}
	s.items[k] = values
	return !exists
}

// ReplaceAll 清空后整体写入（写入工具的整体重写语义）。
func (s *DraftSink) ReplaceAll(schema model.DatasetSchema, items []map[string]any) {
	s.order = s.order[:0]
	s.items = map[string]map[string]any{}
	for _, v := range items {
		s.Upsert(schema, v)
	}
}

// Items 按首插顺序返回全部草稿（字段袋）。
func (s *DraftSink) Items() []map[string]any {
	out := make([]map[string]any, 0, len(s.order))
	for _, k := range s.order {
		out = append(out, s.items[k])
	}
	return out
}

func (s *DraftSink) Len() int { return len(s.order) }

// ReplayFrom 按发生顺序重放会话中全部写入调用，重建草稿状态。
// 会话即事实源：assistant 消息的 toolCall 块携带完整参数，重放确定性成立。
// w 为该任务的写入绑定（工具名/schema/归一化，与执行时同一 profile）。
func (s *DraftSink) ReplayFrom(msgs []port.Message, w WriteSpec) {
	w = w.orDefault()
	for _, m := range msgs {
		if m.Role != port.RoleAssistant {
			continue
		}
		for _, b := range m.Content {
			if b.Type != port.BlockToolCall || b.ToolCall == nil || b.ToolCall.Name != w.Name {
				continue
			}
			s.applyArgs(b.ToolCall.Arguments, w, time.Now())
		}
	}
}

/* ---- 写入工具 ---- */

// writeWorkItemsTool 分批产出结构化草稿（本次分析的最终产出全部经此工具提交）。
type writeWorkItemsTool struct {
	sink *DraftSink
	spec WriteSpec
}

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
	schema := t.spec.Schema
	return port.ToolSpec{
		Name:        t.spec.Name,
		Description: "分批写入工作项草稿（本次分析的最终产出全部经此工具提交，不要在最终回复里输出 JSON）。items 为草稿对象数组，字段遵循系统提示的草稿字段规范（产出 schema：" + schema.Label + "）；同一批可包含多条。回执报告每条的接受/修订/拒绝情况，被拒条目修正后重新提交即可（主键字段相同的草稿再次写入会覆盖早前版本）。",
		Parameters: json.RawMessage(`{"type":"object","properties":{` +
			`"items":{"type":"array","description":"工作项草稿数组（字段见系统提示的草稿字段规范）","items":{"type":"object"}},` +
			`"replace_all":{"type":"boolean","description":"true 时先清空已写入的全部草稿再写入本批（整体重写）"}` +
			`,"required":["items"]}}`),
	}
}

func (t *writeWorkItemsTool) Execute(_ context.Context, call port.ToolCall, _ func(string)) agent.ToolOutput {
	if t.sink == nil {
		return errOutput("草稿累积器未初始化")
	}
	r, err := applyWriteArgs(t.sink, call.Arguments, time.Now(), t.spec)
	if err != nil {
		return errOutput("%v", err)
	}
	isErr := len(r.Rejected) > 0 && r.Accepted+r.Updated == 0 // 全拒才算失败；部分拒绝靠回执引导修正
	detail := fmt.Sprintf("%s：新增 %d、修订 %d（累计 %d）", t.spec.Name, r.Accepted, r.Updated, r.TotalInDraft)
	if len(r.Rejected) > 0 {
		detail += fmt.Sprintf("，被拒 %d", len(r.Rejected))
	}
	return agent.ToolOutput{Output: compactJSON(r), Details: detail, IsError: isErr}
}

func (t *writeWorkItemsTool) PromptSnippet() string {
	return t.spec.Name + "：分批写入工作项草稿（本次分析的最终产出全部经此工具提交）"
}

func (t *writeWorkItemsTool) PromptGuidelines() []string {
	return []string{
		"边读边写：消化一批就写入一批，避免拖到最后一次性输出超长内容",
		"回执中 rejected 的条目须修正后重新提交；主键字段相同的草稿再次写入会覆盖早前版本，可用于修订",
		"全部草稿写入完成后，最终回复只需简短总结（条数、项目分组、存疑点），不要再输出 JSON",
	}
}

// applyWriteArgs 解析并应用一次写入调用（工具执行与会话重放共用）。
func applyWriteArgs(sink *DraftSink, raw json.RawMessage, now time.Time, w WriteSpec) (*writeReceipt, error) {
	w = w.orDefault()
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
		sink.ReplaceAll(w.Schema, nil)
	}
	r := &writeReceipt{}
	for i, item := range args.Items {
		if err := validateDraft(item, w.Schema); err != nil {
			r.Rejected = append(r.Rejected, rejectDetail{Index: i, Error: err.Error()})
			continue
		}
		if sink.Upsert(w.Schema, logic.NormalizeValues(w.Schema, item, now)) {
			r.Accepted++
		} else {
			r.Updated++
		}
	}
	r.TotalInDraft = sink.Len()
	return r, nil
}

func (s *DraftSink) applyArgs(raw json.RawMessage, w WriteSpec, now time.Time) {
	_, _ = applyWriteArgs(s, raw, now, w) // 重放：忽略回执与参数级错误（与执行时一致地跳过）
}

// validateDraft 写入前校验（按任务产出 schema 的必填/枚举/数值规则，给模型即时反馈）。
func validateDraft(item map[string]any, schema model.DatasetSchema) error {
	if err := logic.ValidateValues(schema, item); err != nil {
		return err
	}
	for _, f := range schema.Fields {
		if f.Type != model.FieldNumber {
			continue
		}
		if v, ok := item[f.Key]; ok && v != nil {
			if n, parsed := logic.AsNumber(v); !parsed || math.IsNaN(n) || math.IsInf(n, 0) || n <= 0 {
				return fmt.Errorf("%s 必须为正数", f.Label)
			}
		}
	}
	return nil
}
