package repository

import (
	"strings"
	"testing"

	"reqflow/internal/domain/model"
)

/* ---- 动态索引计划（纯函数）：表达式形状回归即失败 —— 索引命中要求与查询表达式逐字一致 ---- */

func TestPlanIndexesFTSAndFilter(t *testing.T) {
	schema := model.DatasetSchema{
		Type: "requirement",
		Fields: []model.FieldSpec{
			{Key: "title", FTS: true, Filterable: true},
			{Key: "priority", Filterable: true},
			{Key: "description", FTS: true},
			{Key: "note"}, // 无标记 → 无索引
		},
	}
	want := planIndexes("ds-abc", schema, "simple")
	// title（fts+flt）2 个 + priority flt 1 个 + description fts 1 个
	if len(want) != 4 {
		t.Fatalf("期望 4 个动态索引，got %d: %v", len(want), want)
	}
	ftsTitle, ok := want[dsIndexName("ds-abc", "title", "fts")]
	if !ok {
		t.Fatalf("缺少 title FTS 索引: %v", want)
	}
	for _, want1 := range []string{
		"USING gin (to_tsvector('simple', fields ->> 'title'))",
		"WHERE dataset_id = 'ds-abc'",
	} {
		if !strings.Contains(ftsTitle, want1) {
			t.Fatalf("title FTS DDL 缺 %q:\n%s", want1, ftsTitle)
		}
	}
	// 查询侧（SearchDatasetItemsFTS）表达式必须与索引逐字一致
	if !strings.Contains(ftsTitle, "to_tsvector('simple', fields ->> 'title')") {
		t.Fatalf("索引表达式与查询表达式不一致:\n%s", ftsTitle)
	}
	flt, ok := want[dsIndexName("ds-abc", "priority", "flt")]
	if !ok || !strings.Contains(flt, "((fields ->> 'priority'))") {
		t.Fatalf("priority 筛选索引 DDL 异常: %s", flt)
	}
}

func TestPlanIndexesRejectsInvalidKey(t *testing.T) {
	schema := model.DatasetSchema{Fields: []model.FieldSpec{
		{Key: "bad key", FTS: true}, // 空格：非法标识
		{Key: "ok_key", FTS: true},
	}}
	want := planIndexes("ds-1", schema, "simple")
	if len(want) != 1 {
		t.Fatalf("非法 key 应被跳过，got %v", want)
	}
}

func TestPlanIndexesDeterministicNames(t *testing.T) {
	schema := model.DatasetSchema{Fields: []model.FieldSpec{{Key: "title", FTS: true}}}
	a := planIndexes("ds-1", schema, "simple")
	b := planIndexes("ds-1", schema, "simple")
	if len(a) != 1 || len(b) != 1 {
		t.Fatalf("计划数异常: %v %v", a, b)
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			t.Fatalf("同名 (数据集,字段,用途) 应派生同名索引: %s", k)
		}
	}
}

func TestPlanIndexesOtherDatasetIsolated(t *testing.T) {
	schema := model.DatasetSchema{Fields: []model.FieldSpec{{Key: "title", FTS: true}}}
	a := planIndexes("ds-1", schema, "simple")
	b := planIndexes("ds-2", schema, "simple")
	for k := range a {
		if _, ok := b[k]; ok {
			t.Fatalf("不同数据集的动态索引名应不同: %s", k)
		}
		if !strings.Contains(a[k], "WHERE dataset_id = 'ds-1'") {
			t.Fatalf("索引应带本数据集谓词: %s", a[k])
		}
	}
}
