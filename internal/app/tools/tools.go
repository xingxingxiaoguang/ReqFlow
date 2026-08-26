// Package tools 提供需求分析 agent 的只读查询工具（HANDOVER §12.3 工具候选清单）。
//
// 设计约定：
//   - 只读红线：本包不得出现任何平台写操作（CreateProject/CreateWorkItem）；
//     导入仍由人工确认后的 /api/import 流程执行——AI 是草稿机不是审批者
//   - 数据源优先本地同步缓存仓储（快、不占平台 API 配额），
//     PlatformClient 直查仅 get_project_members 一处（仓储无成员表），带进程内缓存
//   - Output 返回紧凑 JSON（模型消费）；Details 返回人读摘要（前端工具轨迹展示用）——
//     pi 的 output/details 拆分
//   - 参数 Schema 保持极简（string/int 为主），枚举值不查库、构造期固化
//   - 查询错误按 IsError 回执（模型可自行纠正或放弃该信息），不中断 loop
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"reqflow/internal/app/agent"
	"reqflow/internal/domain/logic"
	"reqflow/internal/port"
)

// Deps 工具集依赖（组装点注入 port 实现）。
type Deps struct {
	Projects  port.ProjectRepo
	WorkItems port.WorkItemRepo
	Meta      port.MetaRepo
	Platform  port.PlatformClient
}

// Build 构造全部只读工具（顺序即注入 Context.Tools 的顺序）。
func Build(d Deps) []agent.Tool {
	members := &memberCache{platform: d.Platform}
	return []agent.Tool{
		&searchProjectsTool{projects: d.Projects},
		&searchWorkItemsTool{items: d.WorkItems},
		&workItemTypesTool{meta: d.Meta},
		&projectMembersTool{cache: members},
		&recentWorkItemsTool{items: d.WorkItems},
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

/* ---- search_projects：草稿项目名 → 真实项目 ---- */

type searchProjectsTool struct{ projects port.ProjectRepo }

func (t *searchProjectsTool) Spec() port.ToolSpec {
	return port.ToolSpec{
		Name:        "search_projects",
		Description: "按名称搜索协作平台项目（本地同步缓存），返回真实项目 ID/名称/描述。把需求文档中的项目名对应到真实项目时使用。",
		Parameters: json.RawMessage(`{"type":"object","properties":{` +
			`"name":{"type":"string","description":"项目名或其片段"}},` +
			`"required":["name"]}`),
	}
}

type projectHit struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Match       string `json:"match"` // exact | fuzzy
}

func (t *searchProjectsTool) Execute(ctx context.Context, call port.ToolCall, _ func(string)) agent.ToolOutput {
	var args struct {
		Name string `json:"name"`
	}
	if err := decodeArgs(call, &args); err != nil {
		return errOutput("参数解析失败: %v", err)
	}
	name := strings.TrimSpace(args.Name)
	if name == "" {
		return errOutput("name 不能为空")
	}
	projects, err := t.projects.ListActive(ctx)
	if err != nil {
		return errOutput("查询项目失败: %v", err)
	}

	key := logic.NormalizeForExactMatch(name)
	var exact, fuzzy []projectHit
	for _, p := range projects {
		pn := logic.NormalizeForExactMatch(p.Name)
		if pn == key && key != "" {
			exact = append(exact, projectHit{ID: p.ID, Name: p.Name, Description: truncateRunes(p.Description, 80), Match: "exact"})
			continue
		}
		if len(fuzzy) < 5 && containsEither(pn, key) {
			fuzzy = append(fuzzy, projectHit{ID: p.ID, Name: p.Name, Description: truncateRunes(p.Description, 80), Match: "fuzzy"})
		}
	}
	hits := append(exact, fuzzy...)
	if len(hits) > 5 {
		hits = hits[:5]
	}
	if len(hits) == 0 {
		return agent.ToolOutput{
			Output:  fmt.Sprintf("未找到与 %q 匹配的项目。该名称可能是新项目，草稿 project_name 保留原名即可。", name),
			Details: fmt.Sprintf("search_projects(%s)：无命中", name),
		}
	}
	var names []string
	for _, h := range hits {
		names = append(names, h.Name)
	}
	return agent.ToolOutput{
		Output:  compactJSON(hits),
		Details: fmt.Sprintf("search_projects(%s)：命中 %s", name, strings.Join(names, "、")),
	}
}

/* ---- search_work_items：同项目查重自查 ---- */

type searchWorkItemsTool struct{ items port.WorkItemRepo }

func (t *searchWorkItemsTool) Spec() port.ToolSpec {
	return port.ToolSpec{
		Name:        "search_work_items",
		Description: "在指定项目内按标题或编号（如 WI-123）搜索已存在的工作项，用于导入前自查重复。",
		Parameters: json.RawMessage(`{"type":"object","properties":{` +
			`"project_id":{"type":"string","description":"项目 ID（search_projects 返回的 id）"},` +
			`"title":{"type":"string","description":"工作项标题或编号"}},` +
			`"required":["project_id","title"]}`),
	}
}

type workItemHit struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier,omitempty"`
	Title      string `json:"title"`
	Kind       string `json:"kind,omitempty"`
	Match      string `json:"match"` // exact | fuzzy
}

func (t *searchWorkItemsTool) Execute(ctx context.Context, call port.ToolCall, _ func(string)) agent.ToolOutput {
	var args struct {
		ProjectID string `json:"project_id"`
		Title     string `json:"title"`
	}
	if err := decodeArgs(call, &args); err != nil {
		return errOutput("参数解析失败: %v", err)
	}
	args.ProjectID = strings.TrimSpace(args.ProjectID)
	args.Title = strings.TrimSpace(args.Title)
	if args.ProjectID == "" || args.Title == "" {
		return errOutput("project_id 与 title 均不能为空")
	}
	synced, _, err := t.items.ListActive(ctx, port.WorkItemFilter{ProjectID: args.ProjectID, Limit: 10000})
	if err != nil {
		return errOutput("查询工作项失败: %v", err)
	}

	key := logic.NormalizeForExactMatch(args.Title)
	var hits []workItemHit
	for _, w := range synced {
		match := ""
		if w.Identifier != "" && strings.EqualFold(strings.TrimSpace(w.Identifier), args.Title) {
			match = "exact"
		} else if logic.NormalizeForExactMatch(w.Title) == key && key != "" {
			match = "exact"
		} else if containsEither(logic.NormalizeForExactMatch(w.Title), key) {
			match = "fuzzy"
		}
		if match != "" {
			hits = append(hits, workItemHit{ID: w.ID, Identifier: w.Identifier, Title: w.Title, Kind: w.Kind, Match: match})
			if len(hits) >= 5 {
				break
			}
		}
	}
	if len(hits) == 0 {
		return agent.ToolOutput{
			Output:  "未发现重复",
			Details: fmt.Sprintf("search_work_items(%s)：无命中", truncateRunes(args.Title, 30)),
		}
	}
	var labels []string
	for _, h := range hits {
		if h.Identifier != "" {
			labels = append(labels, h.Identifier)
		} else {
			labels = append(labels, truncateRunes(h.Title, 20))
		}
	}
	return agent.ToolOutput{
		Output:  compactJSON(hits),
		Details: fmt.Sprintf("search_work_items(%s)：疑似重复 %s", truncateRunes(args.Title, 30), strings.Join(labels, "、")),
	}
}

/* ---- get_work_item_types：类型名 → UUID 自检 ---- */

type workItemTypesTool struct{ meta port.MetaRepo }

func (t *workItemTypesTool) Spec() port.ToolSpec {
	return port.ToolSpec{
		Name:        "get_work_item_types",
		Description: "获取指定项目的工作项类型列表（UUID、名称、分组）。核实草稿 type_id 是否真实存在时使用。",
		Parameters: json.RawMessage(`{"type":"object","properties":{` +
			`"project_id":{"type":"string","description":"项目 ID"}},` +
			`"required":["project_id"]}`),
	}
}

func (t *workItemTypesTool) Execute(ctx context.Context, call port.ToolCall, _ func(string)) agent.ToolOutput {
	var args struct {
		ProjectID string `json:"project_id"`
	}
	if err := decodeArgs(call, &args); err != nil {
		return errOutput("参数解析失败: %v", err)
	}
	if args.ProjectID = strings.TrimSpace(args.ProjectID); args.ProjectID == "" {
		return errOutput("project_id 不能为空")
	}
	types, err := t.meta.ListTypes(ctx, args.ProjectID)
	if err != nil {
		return errOutput("查询工作项类型失败: %v", err)
	}
	type typeHit struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		Group string `json:"group"`
	}
	hits := make([]typeHit, len(types))
	var labels []string
	for i, tt := range types {
		hits[i] = typeHit{ID: tt.ID, Name: tt.Name, Group: tt.Group}
		labels = append(labels, fmt.Sprintf("%s(%s)", tt.Name, tt.Group))
	}
	return agent.ToolOutput{
		Output:  compactJSON(hits),
		Details: fmt.Sprintf("get_work_item_types：%s", strings.Join(labels, "、")),
	}
}

/* ---- get_project_members：负责人姓名解析（PlatformClient 直查 + 进程内缓存） ---- */

type projectMembersTool struct{ cache *memberCache }

func (t *projectMembersTool) Spec() port.ToolSpec {
	return port.ToolSpec{
		Name:        "get_project_members",
		Description: "获取指定项目的成员列表（ID、姓名、显示名）。核实草稿 assignee_name 负责人是否真实存在时使用。",
		Parameters: json.RawMessage(`{"type":"object","properties":{` +
			`"project_id":{"type":"string","description":"项目 ID"}},` +
			`"required":["project_id"]}`),
	}
}

func (t *projectMembersTool) Execute(ctx context.Context, call port.ToolCall, _ func(string)) agent.ToolOutput {
	var args struct {
		ProjectID string `json:"project_id"`
	}
	if err := decodeArgs(call, &args); err != nil {
		return errOutput("参数解析失败: %v", err)
	}
	if args.ProjectID = strings.TrimSpace(args.ProjectID); args.ProjectID == "" {
		return errOutput("project_id 不能为空")
	}
	members, err := t.cache.get(ctx, args.ProjectID)
	if err != nil {
		return errOutput("查询项目成员失败: %v", err)
	}
	type memberHit struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		DisplayName string `json:"display_name,omitempty"`
	}
	hits := make([]memberHit, 0, len(members))
	var names []string
	for _, m := range members {
		hits = append(hits, memberHit{ID: m.ID, Name: m.Name, DisplayName: m.DisplayName})
		if len(names) < 8 {
			names = append(names, m.DisplayName)
		}
	}
	if len(members) > 8 {
		names = append(names, fmt.Sprintf("等 %d 人", len(members)))
	}
	return agent.ToolOutput{
		Output:  compactJSON(hits),
		Details: fmt.Sprintf("get_project_members：%s", strings.Join(names, "、")),
	}
}

// memberCache 成员进程内缓存。成员列表同步频率远低于分析频率，缓存整轮分析内
// 不再重复拉取；不设 TTL（进程重启即失效，单工作区场景足够）。
type memberCache struct {
	platform port.PlatformClient
	mu       sync.Mutex
	store    map[string][]port.PlatformMember
}

func (c *memberCache) get(ctx context.Context, projectID string) ([]port.PlatformMember, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.store == nil {
		c.store = map[string][]port.PlatformMember{}
	}
	if v, ok := c.store[projectID]; ok {
		return v, nil
	}
	members, err := c.platform.ListProjectMembers(ctx, projectID)
	if err != nil {
		return nil, err
	}
	c.store[projectID] = members
	return members, nil
}

/* ---- list_recent_work_items：项目语料现状 ---- */

type recentWorkItemsTool struct{ items port.WorkItemRepo }

func (t *recentWorkItemsTool) Spec() port.ToolSpec {
	return port.ToolSpec{
		Name:        "list_recent_work_items",
		Description: "列出指定项目最近同步的工作项（编号、标题、类型），了解项目现有工作项的表述习惯，让草稿描述更贴实际。",
		Parameters: json.RawMessage(`{"type":"object","properties":{` +
			`"project_id":{"type":"string","description":"项目 ID"},` +
			`"limit":{"type":"integer","description":"返回条数，默认 10，最大 50"}},` +
			`"required":["project_id"]}`),
	}
}

func (t *recentWorkItemsTool) Execute(ctx context.Context, call port.ToolCall, _ func(string)) agent.ToolOutput {
	var args struct {
		ProjectID string `json:"project_id"`
		Limit     int    `json:"limit"`
	}
	if err := decodeArgs(call, &args); err != nil {
		return errOutput("参数解析失败: %v", err)
	}
	if args.ProjectID = strings.TrimSpace(args.ProjectID); args.ProjectID == "" {
		return errOutput("project_id 不能为空")
	}
	if args.Limit <= 0 {
		args.Limit = 10
	}
	if args.Limit > 50 {
		args.Limit = 50
	}
	items, _, err := t.items.ListActive(ctx, port.WorkItemFilter{ProjectID: args.ProjectID, Limit: args.Limit})
	if err != nil {
		return errOutput("查询工作项失败: %v", err)
	}
	type itemHit struct {
		Identifier string `json:"identifier,omitempty"`
		Title      string `json:"title"`
		Kind       string `json:"kind,omitempty"`
		UpdatedAt  string `json:"updated_at,omitempty"`
	}
	hits := make([]itemHit, len(items))
	for i, w := range items {
		hits[i] = itemHit{Identifier: w.Identifier, Title: w.Title, Kind: w.Kind, UpdatedAt: w.RemoteUpdatedAt}
	}
	return agent.ToolOutput{
		Output:  compactJSON(hits),
		Details: fmt.Sprintf("list_recent_work_items：返回 %d 条", len(hits)),
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
