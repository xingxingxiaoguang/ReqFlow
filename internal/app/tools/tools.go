// Package tools 提供需求分析 agent 的只读查询工具。
//
// 设计约定：
//   - 只读红线：本包不得出现任何写操作；数据集的生成仍由人工确认后的任务步骤执行——
//     AI 是草稿机不是审批者
//   - 数据源：本地需求数据集（DatasetRepo，查重/关联匹配/语料现状的底料）
//   - Output 返回紧凑 JSON（模型消费）；Details 返回人读摘要（前端工具轨迹展示用）——
//     pi 的 output/details 拆分
//   - 参数 Schema 保持极简（string/int 为主）
//   - 查询错误按 IsError 回执（模型可自行纠正或放弃该信息），不中断 loop
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reqflow/internal/app/agent"
	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

// Deps 工具集依赖（组装点注入 port 实现）。
type Deps struct {
	Datasets port.DatasetRepo
}

// Build 构造全部只读工具（顺序即注入 Context.Tools 的顺序）。
func Build(d Deps) []agent.Tool {
	return []agent.Tool{
		&searchRequirementsTool{datasets: d.Datasets},
		&recentRequirementsTool{datasets: d.Datasets},
		&searchDatasetsTool{datasets: d.Datasets},
	}
}

/* ---- 公共辅助 ---- */

func decodeArgs(call port.ToolCall, dst any) error {
	if len(call.Arguments) == 0 {
		return nil
	}
	return json.Unmarshal(call.Arguments, dst)
}

func errOutput(format string, a ...any) agent.ToolOutput {
	return agent.ToolOutput{Output: fmt.Sprintf(format, a...), IsError: true}
}

// compactJSON 序列化为无缩进 JSON（Output 一律紧凑，控制上下文膨胀）。
func compactJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// requirementFields 需求数据集条目的字段形状（与草稿对齐）。
type requirementFields struct {
	ProjectName string `json:"project_name"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

func parseRequirementFields(fields string) requirementFields {
	var f requirementFields
	_ = json.Unmarshal([]byte(fields), &f)
	return f
}

// requirementItems 需求数据集全部条目（跨数据集，查重语料）。
func requirementItems(ctx context.Context, d port.DatasetRepo) ([]model.DatasetItem, error) {
	return d.ListDatasetItemsByType(ctx, model.DatasetTypeRequirement)
}

/* ---- search_requirements：需求数据集查重自查 ---- */

type searchRequirementsTool struct{ datasets port.DatasetRepo }

func (t *searchRequirementsTool) Spec() port.ToolSpec {
	return port.ToolSpec{
		Name:        "search_requirements",
		Description: "在已有需求数据集中按标题搜索需求条目（含其所属数据集），用于草稿导入前自查重复——若命中相似需求，应调整描述与已有需求区分或标注关联。",
		Parameters: json.RawMessage(`{"type":"object","properties":{` +
			`"title":{"type":"string","description":"需求标题或其片段"}},` +
			`"required":["title"]}`),
	}
}

type requirementHit struct {
	DatasetName string `json:"dataset_name,omitempty"`
	Title       string `json:"title"`
	Match       string `json:"match"` // exact | fuzzy
}

func (t *searchRequirementsTool) Execute(ctx context.Context, call port.ToolCall, _ func(string)) agent.ToolOutput {
	var args struct {
		Title string `json:"title"`
	}
	if err := decodeArgs(call, &args); err != nil {
		return errOutput("参数解析失败: %v", err)
	}
	title := strings.TrimSpace(args.Title)
	if title == "" {
		return errOutput("title 不能为空")
	}
	items, err := requirementItems(ctx, t.datasets)
	if err != nil {
		return errOutput("查询需求数据集失败: %v", err)
	}

	key := logic.NormalizeForExactMatch(title)
	var hits []requirementHit
	for _, it := range items {
		f := parseRequirementFields(it.Fields)
		match := ""
		if logic.NormalizeForExactMatch(f.Title) == key && key != "" {
			match = "exact"
		} else if containsEither(logic.NormalizeForExactMatch(f.Title), key) {
			match = "fuzzy"
		}
		if match != "" {
			ds := ""
			if dsModel, err := t.datasets.GetDataset(ctx, it.DatasetID); err == nil {
				ds = dsModel.Name
			}
			hits = append(hits, requirementHit{DatasetName: ds, Title: f.Title, Match: match})
			if len(hits) >= 5 {
				break
			}
		}
	}
	if len(hits) == 0 {
		return agent.ToolOutput{
			Output:  "未发现重复",
			Details: fmt.Sprintf("search_requirements(%s)：无命中", truncateRunes(title, 30)),
		}
	}
	var labels []string
	for _, h := range hits {
		labels = append(labels, truncateRunes(h.Title, 20))
	}
	return agent.ToolOutput{
		Output:  compactJSON(hits),
		Details: fmt.Sprintf("search_requirements(%s)：疑似重复 %s", truncateRunes(title, 30), strings.Join(labels, "、")),
	}
}

/* ---- list_recent_requirements：需求语料现状 ---- */

type recentRequirementsTool struct{ datasets port.DatasetRepo }

func (t *recentRequirementsTool) Spec() port.ToolSpec {
	return port.ToolSpec{
		Name:        "list_recent_requirements",
		Description: "列出已有需求数据集中最近的需求条目（标题、所属数据集、项目分组），了解既有需求的表述习惯，让草稿描述更贴实际。",
		Parameters: json.RawMessage(`{"type":"object","properties":{` +
			`"limit":{"type":"integer","description":"返回条数，默认 10，最大 50"}}}`),
	}
}

func (t *recentRequirementsTool) Execute(ctx context.Context, call port.ToolCall, _ func(string)) agent.ToolOutput {
	var args struct {
		Limit int `json:"limit"`
	}
	if err := decodeArgs(call, &args); err != nil {
		return errOutput("参数解析失败: %v", err)
	}
	if args.Limit <= 0 {
		args.Limit = 10
	}
	if args.Limit > 50 {
		args.Limit = 50
	}
	items, err := requirementItems(ctx, t.datasets)
	if err != nil {
		return errOutput("查询需求数据集失败: %v", err)
	}
	if len(items) > args.Limit {
		items = items[len(items)-args.Limit:]
	}
	type itemHit struct {
		DatasetName string `json:"dataset_name,omitempty"`
		ProjectName string `json:"project_name,omitempty"`
		Title       string `json:"title"`
	}
	hits := make([]itemHit, 0, len(items))
	for _, it := range items {
		f := parseRequirementFields(it.Fields)
		ds := ""
		if dsModel, err := t.datasets.GetDataset(ctx, it.DatasetID); err == nil {
			ds = dsModel.Name
		}
		hits = append(hits, itemHit{DatasetName: ds, ProjectName: f.ProjectName, Title: f.Title})
	}
	return agent.ToolOutput{
		Output:  compactJSON(hits),
		Details: fmt.Sprintf("list_recent_requirements：返回 %d 条", len(hits)),
	}
}

/* ---- search_datasets：数据集格局 ---- */

type searchDatasetsTool struct{ datasets port.DatasetRepo }

func (t *searchDatasetsTool) Spec() port.ToolSpec {
	return port.ToolSpec{
		Name:        "search_datasets",
		Description: "列出已有需求数据集（名称、条目数），了解需求数据的归档格局；为草稿选择归属数据集或判断是否需要新建数据集时使用。",
		Parameters: json.RawMessage(`{"type":"object","properties":{` +
			`"name":{"type":"string","description":"数据集名称或其片段，空则返回全部"}}}`),
	}
}

func (t *searchDatasetsTool) Execute(ctx context.Context, call port.ToolCall, _ func(string)) agent.ToolOutput {
	var args struct {
		Name string `json:"name"`
	}
	if err := decodeArgs(call, &args); err != nil {
		return errOutput("参数解析失败: %v", err)
	}
	datasets, err := t.datasets.ListDatasets(ctx, model.DatasetTypeRequirement, 100)
	if err != nil {
		return errOutput("查询数据集失败: %v", err)
	}
	type datasetHit struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		ItemCount int    `json:"item_count"`
	}
	key := logic.NormalizeForExactMatch(strings.TrimSpace(args.Name))
	var hits []datasetHit
	for _, d := range datasets {
		if key != "" && !containsEither(logic.NormalizeForExactMatch(d.Name), key) {
			continue
		}
		hits = append(hits, datasetHit{ID: d.ID, Name: d.Name, ItemCount: d.ItemCount})
	}
	if len(hits) == 0 {
		return agent.ToolOutput{
			Output:  "当前没有已生成的需求数据集",
			Details: "search_datasets：无命中",
		}
	}
	var names []string
	for _, h := range hits {
		names = append(names, fmt.Sprintf("%s(%d条)", h.Name, h.ItemCount))
	}
	return agent.ToolOutput{
		Output:  compactJSON(hits),
		Details: fmt.Sprintf("search_datasets：%s", strings.Join(names, "、")),
	}
}

/* ---- 私有辅助 ---- */

// containsEither 双向包含（归一化后）；a/b 任一为空返回 false。
func containsEither(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.Contains(a, b) || strings.Contains(b, a)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
