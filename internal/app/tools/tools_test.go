package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"reqflow/internal/app/agent"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

/* ---- port 接口 mock ---- */

type fakeDatasets struct {
	datasets []model.Dataset
	items    []model.DatasetItem // 全部需求条目（ListDatasetItemsByType 语料）
	byID     map[string]model.Dataset
}

func newFakeDatasets(datasets []model.Dataset, items []model.DatasetItem) *fakeDatasets {
	f := &fakeDatasets{datasets: datasets, items: items, byID: map[string]model.Dataset{}}
	for _, d := range datasets {
		f.byID[d.ID] = d
	}
	return f
}

func (f *fakeDatasets) CreateDataset(ctx context.Context, d *model.Dataset) error { return nil }
func (f *fakeDatasets) UpdateDataset(ctx context.Context, d *model.Dataset) error { return nil }
func (f *fakeDatasets) ListDatasets(ctx context.Context, typ string, limit int) ([]model.Dataset, error) {
	var out []model.Dataset
	for _, d := range f.datasets {
		if typ != "" && d.Type != typ {
			continue
		}
		out = append(out, d)
	}
	return out, nil
}
func (f *fakeDatasets) GetDataset(ctx context.Context, id string) (*model.Dataset, error) {
	if d, ok := f.byID[id]; ok {
		return &d, nil
	}
	return nil, fmt.Errorf("数据集不存在")
}
func (f *fakeDatasets) CountDatasets(ctx context.Context, typ string) (int64, error)     { return 0, nil }
func (f *fakeDatasets) CountDatasetItems(ctx context.Context, typ string) (int64, error) { return 0, nil }
func (f *fakeDatasets) ReplaceDatasetItems(ctx context.Context, id string, items []port.DatasetItemVector) error {
	return nil
}
func (f *fakeDatasets) ListDatasetItemsByType(ctx context.Context, typ string) ([]model.DatasetItem, error) {
	return f.items, nil
}
func (f *fakeDatasets) ListDatasetItems(ctx context.Context, id string, limit int) ([]model.DatasetItem, error) {
	return f.items, nil
}
func (f *fakeDatasets) SearchSimilarDatasetItems(ctx context.Context, vec []float32, typ string, n int) ([]port.SimilarDatasetItem, error) {
	return nil, nil
}

/* ---- 桩 ---- */

type stubTool struct {
	fired int
}

func (t *stubTool) Spec() port.ToolSpec {
	return port.ToolSpec{Name: "search_requirements", Description: "桩", Parameters: json.RawMessage(`{"type":"object"}`)}
}
func (t *stubTool) Execute(ctx context.Context, call port.ToolCall, onProgress func(string)) agent.ToolOutput {
	t.fired++
	return agent.ToolOutput{Output: `[]`, Details: "桩"}
}

func fieldsOf(title, project string) string {
	b, _ := json.Marshal(model.DraftItem{Title: title, ProjectName: project, Description: "描述"})
	return string(b)
}

/* ---- 用例 ---- */

func TestSearchRequirementsTool(t *testing.T) {
	ds := newFakeDatasets(
		[]model.Dataset{{ID: "ds-1", Type: model.DatasetTypeRequirement, Name: "订单中心需求集", ItemCount: 2}},
		[]model.DatasetItem{
			{ID: "it-1", DatasetID: "ds-1", Fields: fieldsOf("订单列表增加批量导出", "订单中心")},
			{ID: "it-2", DatasetID: "ds-1", Fields: fieldsOf("支付回调重试机制", "订单中心")},
		},
	)
	ts := Build(Deps{Datasets: ds})

	// 精确命中
	out := executeTool(t, ts, "search_requirements", `{"title":"订单列表增加批量导出"}`)
	if out.IsError || !strings.Contains(out.Output, `"match":"exact"`) {
		t.Fatalf("精确命中失败: %+v", out)
	}
	if !strings.Contains(out.Output, "订单中心需求集") {
		t.Fatalf("Output 应含数据集名: %s", out.Output)
	}

	// 模糊命中（标题片段）
	out = executeTool(t, ts, "search_requirements", `{"title":"批量导出"}`)
	if out.IsError || !strings.Contains(out.Output, `"match":"fuzzy"`) {
		t.Fatalf("模糊命中失败: %+v", out)
	}

	// 无命中
	out = executeTool(t, ts, "search_requirements", `{"title":"发票抬头管理"}`)
	if out.IsError || !strings.Contains(out.Output, "未发现重复") {
		t.Fatalf("无命中应提示: %+v", out)
	}

	// 参数缺失
	out = executeTool(t, ts, "search_requirements", `{}`)
	if !out.IsError {
		t.Fatalf("缺参数应报错: %+v", out)
	}
}

func TestRecentRequirementsTool(t *testing.T) {
	ds := newFakeDatasets(
		[]model.Dataset{{ID: "ds-1", Type: model.DatasetTypeRequirement, Name: "订单中心需求集"}},
		[]model.DatasetItem{
			{ID: "it-1", DatasetID: "ds-1", Fields: fieldsOf("订单列表增加批量导出", "订单中心")},
			{ID: "it-2", DatasetID: "ds-1", Fields: fieldsOf("支付回调重试机制", "订单中心")},
		},
	)
	out := executeTool(t, Build(Deps{Datasets: ds}), "list_recent_requirements", `{"limit":1}`)
	if out.IsError {
		t.Fatalf("list_recent_requirements: %+v", out)
	}
	var hits []map[string]any
	_ = json.Unmarshal([]byte(out.Output), &hits)
	if len(hits) != 1 {
		t.Fatalf("limit=1 应返回 1 条: %s", out.Output)
	}
}

func TestSearchDatasetsTool(t *testing.T) {
	ds := newFakeDatasets(
		[]model.Dataset{
			{ID: "ds-1", Type: model.DatasetTypeRequirement, Name: "订单中心需求集", ItemCount: 3},
			{ID: "ds-2", Type: model.DatasetTypeRequirement, Name: "会员体系需求集", ItemCount: 5},
		},
		nil,
	)
	out := executeTool(t, Build(Deps{Datasets: ds}), "search_datasets", `{"name":"订单"}`)
	if out.IsError || !strings.Contains(out.Output, "订单中心需求集") || strings.Contains(out.Output, "会员体系") {
		t.Fatalf("按名过滤失败: %+v", out)
	}
	out = executeTool(t, Build(Deps{Datasets: ds}), "search_datasets", `{}`)
	if out.IsError || !strings.Contains(out.Output, "会员体系需求集") {
		t.Fatalf("空名应返回全部: %+v", out)
	}
}

func executeTool(t *testing.T, ts []agent.Tool, name, args string) agent.ToolOutput {
	t.Helper()
	for _, tool := range ts {
		if tool.Spec().Name == name {
			return tool.Execute(context.Background(), port.ToolCall{ID: "c1", Name: name, Arguments: json.RawMessage(args)}, nil)
		}
	}
	t.Fatalf("工具 %s 未注册", name)
	return agent.ToolOutput{}
}
