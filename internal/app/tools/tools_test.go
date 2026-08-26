package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

/* ---- port 接口 mock ---- */

type fakeProjects struct {
	items []model.Project
	err   error
}

func (f *fakeProjects) UpsertWithVectors(ctx context.Context, items []port.ProjectVector) error { return nil }
func (f *fakeProjects) ListAll(ctx context.Context) ([]model.Project, error)                  { return f.items, nil }
func (f *fakeProjects) ListActive(ctx context.Context) ([]model.Project, error) {
	return f.items, f.err
}
func (f *fakeProjects) Archive(ctx context.Context, ids []string) error { return nil }
func (f *fakeProjects) SearchSimilar(ctx context.Context, vec []float32, n int) ([]port.SimilarProject, error) {
	return nil, nil
}
func (f *fakeProjects) CountActive(ctx context.Context) (int64, error) { return 0, nil }

type fakeWorkItems struct {
	items []model.WorkItem
}

func (f *fakeWorkItems) UpsertWithVectors(ctx context.Context, items []port.WorkItemVector) error { return nil }
func (f *fakeWorkItems) ListAll(ctx context.Context) ([]model.WorkItem, error)                   { return f.items, nil }
func (f *fakeWorkItems) ListActive(ctx context.Context, fl port.WorkItemFilter) ([]model.WorkItem, int64, error) {
	var out []model.WorkItem
	for _, w := range f.items {
		if fl.ProjectID != "" && w.ProjectID != fl.ProjectID {
			continue
		}
		out = append(out, w)
	}
	if fl.Limit > 0 && len(out) > fl.Limit {
		out = out[:fl.Limit]
	}
	return out, int64(len(out)), nil
}
func (f *fakeWorkItems) Archive(ctx context.Context, ids []string) error { return nil }
func (f *fakeWorkItems) SearchSimilar(ctx context.Context, vec []float32, projectID string, n int) ([]port.SimilarWorkItem, error) {
	return nil, nil
}
func (f *fakeWorkItems) CountActive(ctx context.Context) (int64, error) { return 0, nil }

type fakeMeta struct{ types []model.MetaType }

func (f *fakeMeta) UpsertTypes(ctx context.Context, types []model.MetaType) error       { return nil }
func (f *fakeMeta) UpsertStates(ctx context.Context, states []model.MetaState) error   { return nil }
func (f *fakeMeta) UpsertPriorities(ctx context.Context, p []model.MetaPriority) error { return nil }
func (f *fakeMeta) ListTypes(ctx context.Context, projectID string) ([]model.MetaType, error) {
	return f.types, nil
}
func (f *fakeMeta) ListStates(ctx context.Context, projectID string) ([]model.MetaState, error) {
	return nil, nil
}
func (f *fakeMeta) ListPriorities(ctx context.Context, projectID string) ([]model.MetaPriority, error) {
	return nil, nil
}

type fakePlatform struct {
	members    []port.PlatformMember
	memberHits int
}

func (f *fakePlatform) Name() string                                                    { return "fake" }
func (f *fakePlatform) TestConnection(ctx context.Context) error                        { return nil }
func (f *fakePlatform) ListProjects(ctx context.Context) ([]port.PlatformProject, error) { return nil, nil }
func (f *fakePlatform) ListWorkItems(ctx context.Context, projectID string) ([]port.PlatformWorkItem, error) {
	return nil, nil
}
func (f *fakePlatform) ListProjectMembers(ctx context.Context, projectID string) ([]port.PlatformMember, error) {
	f.memberHits++
	return f.members, nil
}
func (f *fakePlatform) CreateProject(ctx context.Context, in port.CreateProjectInput) (*port.PlatformProject, error) {
	return nil, errors.New("只读红线：测试平台不应被写入")
}
func (f *fakePlatform) CreateWorkItem(ctx context.Context, in port.CreateWorkItemInput) (*port.CreatedWorkItem, error) {
	return nil, errors.New("只读红线：测试平台不应被写入")
}
func (f *fakePlatform) ListTypes(ctx context.Context, projectID string) ([]model.MetaType, error) {
	return nil, nil
}
func (f *fakePlatform) ListStates(ctx context.Context, projectID, typeID string) ([]model.MetaState, error) {
	return nil, nil
}
func (f *fakePlatform) ListPriorities(ctx context.Context, projectID string) ([]model.MetaPriority, error) {
	return nil, nil
}

func call(name string, args string) port.ToolCall {
	return port.ToolCall{ID: "c1", Name: name, Arguments: json.RawMessage(args)}
}

func decodeHits(t *testing.T, output string) []map[string]any {
	t.Helper()
	var hits []map[string]any
	if err := json.Unmarshal([]byte(output), &hits); err != nil {
		t.Fatalf("Output 非合法 JSON 数组: %v (%s)", err, output)
	}
	return hits
}

/* ---- search_projects ---- */

func TestSearchProjectsExactAndFuzzy(t *testing.T) {
	tool := &searchProjectsTool{projects: &fakeProjects{items: []model.Project{
		{ID: "p1", Name: "用户中心"},
		{ID: "p2", Name: "用户中心 V2"},
		{ID: "p3", Name: "支付网关"},
	}}}

	// 全角/大小写/空白归一化后精确命中；同名 V2 项目作为模糊命中一并给出（模型可见）
	out := tool.Execute(context.Background(), call("search_projects", `{"name":"用户中心"}`), nil)
	if out.IsError {
		t.Fatalf("不应报错: %s", out.Output)
	}
	hits := decodeHits(t, out.Output)
	if len(hits) != 2 {
		t.Fatalf("命中 = %v", hits)
	}
	if hits[0]["id"] != "p1" || hits[0]["match"] != "exact" {
		t.Fatalf("精确命中应置顶: %v", hits)
	}
	if hits[1]["id"] != "p2" || hits[1]["match"] != "fuzzy" {
		t.Fatalf("同名 V2 应为模糊命中: %v", hits)
	}

	// 片段 → 双向包含模糊命中
	out = tool.Execute(context.Background(), call("search_projects", `{"name":"支付"}`), nil)
	hits = decodeHits(t, out.Output)
	if len(hits) != 1 || hits[0]["id"] != "p3" || hits[0]["match"] != "fuzzy" {
		t.Fatalf("模糊命中 = %v", hits)
	}

	// 无命中：返回引导语而非错误（模型按新项目处理）
	out = tool.Execute(context.Background(), call("search_projects", `{"name":"不存在"}`), nil)
	if out.IsError || !strings.Contains(out.Output, "新项目") {
		t.Fatalf("无命中应返回引导语: %s", out.Output)
	}
}

func TestSearchProjectsRepoError(t *testing.T) {
	tool := &searchProjectsTool{projects: &fakeProjects{err: errors.New("db down")}}
	out := tool.Execute(context.Background(), call("search_projects", `{"name":"x"}`), nil)
	if !out.IsError || !strings.Contains(out.Output, "db down") {
		t.Fatalf("仓储错误应按错误回执: %+v", out)
	}
}

/* ---- search_work_items ---- */

func TestSearchWorkItemsByIdentifierAndTitle(t *testing.T) {
	tool := &searchWorkItemsTool{items: &fakeWorkItems{items: []model.WorkItem{
		{ID: "w1", ProjectID: "p1", Identifier: "WI-100", Title: "实现用户登录", Kind: "story"},
		{ID: "w2", ProjectID: "p1", Identifier: "WI-101", Title: "实现用户注册", Kind: "story"},
		{ID: "w3", ProjectID: "p2", Identifier: "WI-200", Title: "实现用户登录", Kind: "task"}, // 其它项目
	}}}

	// 编号精确（忽略大小写与空白）
	out := tool.Execute(context.Background(), call("search_work_items", `{"project_id":"p1","title":"wi-100"}`), nil)
	hits := decodeHits(t, out.Output)
	if len(hits) != 1 || hits[0]["id"] != "w1" || hits[0]["match"] != "exact" {
		t.Fatalf("编号命中 = %v", hits)
	}

	// 标题模糊 + 项目隔离（p3 不含 w3）
	out = tool.Execute(context.Background(), call("search_work_items", `{"project_id":"p1","title":"登录"}`), nil)
	hits = decodeHits(t, out.Output)
	if len(hits) != 1 || hits[0]["id"] != "w1" || hits[0]["match"] != "fuzzy" {
		t.Fatalf("标题模糊命中 = %v", hits)
	}

	out = tool.Execute(context.Background(), call("search_work_items", `{"project_id":"p1","title":"全新的"}`), nil)
	if out.IsError || out.Output != "未发现重复" {
		t.Fatalf("无重复应明确返回: %s", out.Output)
	}
}

/* ---- get_work_item_types ---- */

func TestWorkItemTypes(t *testing.T) {
	tool := &workItemTypesTool{meta: &fakeMeta{types: []model.MetaType{
		{ID: "t1", ProjectID: "p1", Name: "用户故事", Group: "story"},
		{ID: "t2", ProjectID: "p1", Name: "任务", Group: "task"},
	}}}
	out := tool.Execute(context.Background(), call("get_work_item_types", `{"project_id":"p1"}`), nil)
	if out.IsError {
		t.Fatalf("不应报错: %s", out.Output)
	}
	hits := decodeHits(t, out.Output)
	if len(hits) != 2 || hits[0]["group"] != "story" {
		t.Fatalf("类型列表 = %v", hits)
	}
	if !strings.Contains(out.Details, "用户故事(story)") {
		t.Fatalf("Details 应为人读摘要: %s", out.Details)
	}
}

/* ---- get_project_members（含缓存） ---- */

func TestProjectMembersCached(t *testing.T) {
	platform := &fakePlatform{members: []port.PlatformMember{
		{ID: "m1", Name: "zhang", DisplayName: "张三"},
	}}
	tool := &projectMembersTool{cache: &memberCache{platform: platform}}

	for i := 0; i < 3; i++ {
		out := tool.Execute(context.Background(), call("get_project_members", `{"project_id":"p1"}`), nil)
		if out.IsError {
			t.Fatalf("不应报错: %s", out.Output)
		}
		hits := decodeHits(t, out.Output)
		if len(hits) != 1 || hits[0]["display_name"] != "张三" {
			t.Fatalf("成员 = %v", hits)
		}
	}
	if platform.memberHits != 1 {
		t.Fatalf("进程内缓存应只拉取一次，实际 %d 次", platform.memberHits)
	}
}

/* ---- list_recent_work_items ---- */

func TestRecentWorkItemsLimitClamp(t *testing.T) {
	items := make([]model.WorkItem, 80)
	for i := range items {
		items[i] = model.WorkItem{ID: string(rune('a' + i)), ProjectID: "p1", Title: "条目", Identifier: "WI"}
	}
	tool := &recentWorkItemsTool{items: &fakeWorkItems{items: items}}

	// 默认 10
	out := tool.Execute(context.Background(), call("list_recent_work_items", `{"project_id":"p1"}`), nil)
	if out.IsError || len(decodeHits(t, out.Output)) != 10 {
		t.Fatalf("默认条数应 10: %s", out.Output)
	}
	// 上限 50
	out = tool.Execute(context.Background(), call("list_recent_work_items", `{"project_id":"p1","limit":500}`), nil)
	if out.IsError || len(decodeHits(t, out.Output)) != 50 {
		t.Fatalf("条数上限应 50")
	}
}

/* ---- Build ---- */

func TestBuildFiveReadOnlyTools(t *testing.T) {
	toolset := Build(Deps{
		Projects:  &fakeProjects{},
		WorkItems: &fakeWorkItems{},
		Meta:      &fakeMeta{},
		Platform:  &fakePlatform{},
	})
	want := []string{"search_projects", "search_work_items", "get_work_item_types", "get_project_members", "list_recent_work_items"}
	if len(toolset) != len(want) {
		t.Fatalf("工具数 = %d", len(toolset))
	}
	for i, name := range want {
		if toolset[i].Spec().Name != name {
			t.Fatalf("工具[%d] = %s, want %s", i, toolset[i].Spec().Name, name)
		}
		if !json.Valid(toolset[i].Spec().Parameters) {
			t.Fatalf("%s 的 Parameters 非法 JSON", name)
		}
	}
}
