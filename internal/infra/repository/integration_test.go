//go:build integration

// 仓储层集成冒烟（默认不跑，保持 go test 纯净）：
//
//	go test -tags integration ./internal/infra/repository/ -run TestIntegration -v
//
// 依赖本机 docker PG（docker-compose.yml）：验证 0011 迁移落地、GORM 字符串 ↔ JSONB
// 双向读写、动态表达式索引建删与 FTS 检索命中——TEXT→JSONB 的驱动行为只有真机能钉住。
package repository

import (
	"context"
	"encoding/json"
	"testing"

	"reqflow/internal/domain/model"
	"reqflow/internal/infra/database"
	"reqflow/internal/port"
)

func testDSN() string {
	return "postgres://reqflow:reqflow@127.0.0.1:5432/reqflow?sslmode=disable"
}

func TestIntegrationDatasetJSONBAndFTS(t *testing.T) {
	ctx := context.Background()
	db, err := database.Connect(testDSN(), 3, 500)
	if err != nil {
		t.Skipf("本地 PG 不可用，跳过集成冒烟: %v", err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	repo := NewDatasetRepo(db, "simple")
	indexer := NewDatasetIndexer(db, "simple")

	schema := model.DatasetSchema{
		Type: "smoke_note", Label: "冒烟", Version: 1,
		Fields: []model.FieldSpec{
			{Key: "title", Label: "标题", Type: model.FieldString, Required: true, FTS: true, InKey: true},
			{Key: "body", Label: "正文", Type: model.FieldText, FTS: true},
			{Key: "level", Label: "级别", Type: model.FieldEnum, Enum: []string{"p0", "p1"}, Filterable: true},
		},
	}
	payload, _ := json.Marshal(schema)

	ds := &model.Dataset{Name: "冒烟数据集", Type: "smoke_note", Status: model.DatasetStatusReady,
		SchemaVersion: 1, Schema: string(payload)}
	if err := repo.CreateDataset(ctx, ds); err != nil {
		t.Fatalf("CreateDataset（JSONB 写入）: %v", err)
	}

	// 读回：JSONB → string 往返不失真
	got, err := repo.GetDataset(ctx, ds.ID)
	if err != nil {
		t.Fatalf("GetDataset: %v", err)
	}
	back, ok := model.ParseDatasetSchema(got.Schema)
	if !ok || back.Type != "smoke_note" || len(back.Fields) != 3 {
		t.Fatalf("JSONB 往返失真: %.200s", got.Schema)
	}

	// 动态索引建立：title/body FTS + level 筛选
	if err := indexer.SyncIndexes(ctx, ds.ID, schema); err != nil {
		t.Fatalf("SyncIndexes: %v", err)
	}
	countDynamic := func() int {
		t.Helper()
		var n int
		if err := db.Raw(`SELECT count(*) FROM pg_indexes WHERE schemaname = current_schema()
			AND tablename='dataset_items' AND indexname LIKE 'dsidx_%' AND indexdef LIKE ?`, "%"+ds.ID+"%").Scan(&n).Error; err != nil {
			t.Fatalf("查 pg_indexes: %v", err)
		}
		return n
	}
	if n := countDynamic(); n != 3 {
		t.Fatalf("动态索引数 = %d（期望 3）", n)
	}

	// 条目写入（raw SQL 带 ?::jsonb 显式转换的路径）+ FTS 检索命中
	// simple 分词按空白切（中文整句一词）：用 ASCII 词验证索引命中路径；
	// 中文全文检索需 fts.ts_config=zhparser 等（HANDOVER/README 已注明）
	vecs := []port.DatasetItemVector{
		{DatasetItem: model.DatasetItem{ID: "it-1", DatasetID: ds.ID,
			Fields: `{"title":"实现登录功能","body":"支持 email 验证码注册","level":"p0"}`, ItemKey: "k1", Fingerprint: "f1"}},
		{DatasetItem: model.DatasetItem{ID: "it-2", DatasetID: ds.ID,
			Fields: `{"title":"导出报表","body":"支持 Excel 导出","level":"p1"}`, ItemKey: "k2", Fingerprint: "f2"}},
	}
	if err := repo.UpsertDatasetItems(ctx, ds.ID, "11111111-1111-1111-1111-111111111111", vecs, port.UpsertInsertMissing); err != nil {
		t.Fatalf("UpsertDatasetItems: %v", err)
	}
	hits, err := repo.SearchDatasetItemsFTS(ctx, ds.ID, []string{"title", "body"}, "email", 50)
	if err != nil {
		t.Fatalf("SearchDatasetFTS: %v", err)
	}
	// upsert 不传 ID（由 gen_random_uuid 生成），按 ItemKey 断言
	if len(hits) != 1 || hits[0].ItemKey != "k1" {
		t.Fatalf("FTS 应只命中 k1: %+v", hits)
	}

	// 受控 schema 更新 + 索引 diff（body FTS 出列 → 剩 title fts + level flt 共 2 个）
	schema.Fields = []model.FieldSpec{schema.Fields[0], schema.Fields[2]}
	if err := indexer.SyncIndexes(ctx, ds.ID, schema); err != nil {
		t.Fatalf("SyncIndexes(编辑后): %v", err)
	}
	if n := countDynamic(); n != 2 {
		t.Fatalf("编辑后动态索引数 = %d（期望 2）", n)
	}

	// 回收
	if err := indexer.DropIndexes(ctx, ds.ID); err != nil {
		t.Fatalf("DropIndexes: %v", err)
	}
	if n := countDynamic(); n != 0 {
		t.Fatalf("回收后动态索引数 = %d（期望 0）", n)
	}

	// 清理冒烟数据（dataset_items 随 ON DELETE CASCADE 级联）
	if err := db.Exec(`DELETE FROM datasets WHERE id = ?`, ds.ID).Error; err != nil {
		t.Fatalf("清理冒烟数据: %v", err)
	}
}
