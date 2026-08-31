package app

import (
	"context"
	"testing"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

/* ---- 数据集管理与字段定义受控编辑（字段定义归属数据集实例） ---- */

// fakeDatasetIndexer 记录同步/回收调用的索引桩（验证 schema 变更触达索引管理器）。
type fakeDatasetIndexer struct {
	synced  []string
	dropped []string
}

func (f *fakeDatasetIndexer) SyncIndexes(_ context.Context, datasetID string, _ model.DatasetSchema) error {
	f.synced = append(f.synced, datasetID)
	return nil
}
func (f *fakeDatasetIndexer) DropIndexes(_ context.Context, datasetID string) error {
	f.dropped = append(f.dropped, datasetID)
	return nil
}

func newAdminFixture(t *testing.T) (*DatasetAdminService, *memDatasets, *fakeDatasetIndexer, *memMetadata) {
	t.Helper()
	datasets := newMemDatasets()
	indexer := &fakeDatasetIndexer{}
	audit := &memMetadata{}
	svc := NewDatasetAdminService(datasets, indexer, audit)
	return svc, datasets, indexer, audit
}

func TestCreateDatasetFromTemplate(t *testing.T) {
	svc, datasets, indexer, _ := newAdminFixture(t)

	ds, err := svc.CreateDataset(context.Background(), CreateDatasetInput{
		Name: "订单中心需求集", Type: model.DatasetTypeRequirement,
	})
	if err != nil {
		t.Fatalf("CreateDataset: %v", err)
	}
	if ds.Status != model.DatasetStatusReady || ds.ItemCount != 0 {
		t.Fatalf("新建数据集应为 ready 空集: %+v", ds)
	}
	// 字段定义从类型模板固化到数据集行（新流程：真相源随数据集走）
	got, ok := model.ParseDatasetSchema(ds.Schema)
	if !ok || got.Type != model.DatasetTypeRequirement || len(got.Fields) != len(model.RequirementSchema().Fields) {
		t.Fatalf("数据集行应携带模板字段定义: %.200s", ds.Schema)
	}
	if len(indexer.synced) != 1 || indexer.synced[0] != ds.ID {
		t.Fatalf("创建应同步动态索引: %v", indexer.synced)
	}
	if _, err := datasets.GetDataset(context.Background(), ds.ID); err != nil {
		t.Fatalf("数据集未落库: %v", err)
	}
}

func TestCreateDatasetWithCustomSchema(t *testing.T) {
	svc, _, _, _ := newAdminFixture(t)
	sc := model.DatasetSchema{Type: "whatever", Label: "自定义", Fields: []model.FieldSpec{
		{Key: "finding", Label: "发现", Type: model.FieldString, Required: true, InKey: true},
	}}
	ds, err := svc.CreateDataset(context.Background(), CreateDatasetInput{
		Name: "评审发现集", Type: "review_note", Schema: &sc,
	})
	if err != nil {
		t.Fatalf("CreateDataset: %v", err)
	}
	// 类型一致性由服务端强制（入参的 whatever 被覆写）
	if ds.Type != "review_note" {
		t.Fatalf("type = %s", ds.Type)
	}
	got, _ := model.ParseDatasetSchema(ds.Schema)
	if got.Type != "review_note" {
		t.Fatalf("schema.type 应强制回数据集类型: %+v", got)
	}
}

func TestUpdateDatasetSchemaGuardAndSave(t *testing.T) {
	svc, datasets, indexer, audit := newAdminFixture(t)
	ctx := context.Background()
	ds, err := svc.CreateDataset(ctx, CreateDatasetInput{Name: "需求集", Type: model.DatasetTypeRequirement})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	current, _ := model.ParseDatasetSchema(ds.Schema)

	// 空数据集自由编辑：删除 InKey 字段也放行（守卫保护的是存量条目，不是字段设计）
	free := current
	free.Fields = free.Fields[1:]
	if rep, err := svc.UpdateDatasetSchema(ctx, ds.ID, DatasetSchemaUpdateInput{Schema: free}); err != nil || rep.Blocked {
		t.Fatalf("空数据集应自由编辑（删 InKey 放行）: rep=%+v err=%v", rep, err)
	}
	after0, _ := datasets.GetDataset(ctx, ds.ID)
	if after0.SchemaVersion != 2 {
		t.Fatalf("空集自由编辑版本应递增: %d", after0.SchemaVersion)
	}

	// 落一条条目后守卫生效：❌ 清空字段（含删 InKey）硬拦截
	if err := datasets.UpsertDatasetItems(ctx, ds.ID, "11111111-1111-1111-1111-111111111111", []port.DatasetItemVector{
		{DatasetItem: model.DatasetItem{Fields: `{"title":"需求A"}`, ItemKey: "k1", Fingerprint: "f1"}},
	}, port.UpsertInsertMissing); err != nil {
		t.Fatalf("seed item: %v", err)
	}
	// 假仓储不自动回填 item_count（真实链路由写入器写后回填），手动对齐
	dsRow, _ := datasets.GetDataset(ctx, ds.ID)
	dsRow.ItemCount = 1
	_ = datasets.UpdateDataset(ctx, dsRow)
	blocked := free
	blocked.Fields = free.Fields[1:] // 再去掉 project_name（剩余唯一 InKey）→ 形状层直接拒绝
	if rep, err := svc.UpdateDatasetSchema(ctx, ds.ID, DatasetSchemaUpdateInput{Schema: blocked}); err == nil {
		t.Fatalf("有条目后删除 InKey 字段应被拒绝: rep=%+v", rep)
	}
	got, _ := datasets.GetDataset(ctx, ds.ID)
	if got.SchemaVersion != 2 {
		t.Fatalf("拦截后版本不应递增: %d", got.SchemaVersion)
	}

	// ⚠️ 新增必填字段：需 confirm_risky（基于数据集当前形状，而非原始模板）
	risky := free
	risky.Fields = append(append([]model.FieldSpec{}, free.Fields...),
		model.FieldSpec{Key: "acceptance", Label: "验收标准", Type: model.FieldString, Required: true})
	if rep, err := svc.UpdateDatasetSchema(ctx, ds.ID, DatasetSchemaUpdateInput{Schema: risky}); err == nil || !rep.NeedsConfirm {
		t.Fatalf("新增必填应需确认: rep=%+v err=%v", rep, err)
	}
	if _, err := svc.UpdateDatasetSchema(ctx, ds.ID, DatasetSchemaUpdateInput{Schema: risky}); err == nil {
		t.Fatal("未带 confirm_risky 不应落库")
	}
	rep, err := svc.UpdateDatasetSchema(ctx, ds.ID, DatasetSchemaUpdateInput{Schema: risky, ConfirmRisky: true, Summary: "加验收标准"})
	if err != nil {
		t.Fatalf("confirm 后保存失败: %v", err)
	}
	if rep.Blocked {
		t.Fatalf("保存后不应有 ❌: %+v", rep)
	}

	// 落库校验：版本递增 + 载荷更新 + 审计 + 索引同步
	after, _ := datasets.GetDataset(ctx, ds.ID)
	if after.SchemaVersion != 3 {
		t.Fatalf("版本应递增到 3: %d", after.SchemaVersion)
	}
	parsed, ok := model.ParseDatasetSchema(after.Schema)
	_, hasNew := parsed.Field("acceptance")
	if !ok || !hasNew {
		t.Fatalf("新字段未落库: %.200s", after.Schema)
	}
	if len(audit.audits) != 2 || audit.audits[1].Key != ds.ID || audit.audits[1].ToVersion != 3 {
		t.Fatalf("审计 = %+v", audit.audits)
	}
	if len(indexer.synced) != 3 {
		t.Fatalf("编辑后应再次同步索引: %v", indexer.synced)
	}
}

func TestUpdateDatasetSchemaAllowsDivergence(t *testing.T) {
	// 核心诉求：同类型的不同数据集字段定义可各自演进（互不影响）
	svc, datasets, _, _ := newAdminFixture(t)
	ctx := context.Background()
	dsA, err := svc.CreateDataset(ctx, CreateDatasetInput{Name: "集 A", Type: model.DatasetTypeRequirement})
	if err != nil {
		t.Fatalf("seed A: %v", err)
	}
	dsB, err := svc.CreateDataset(ctx, CreateDatasetInput{Name: "集 B", Type: model.DatasetTypeRequirement})
	if err != nil {
		t.Fatalf("seed B: %v", err)
	}
	currentA, _ := model.ParseDatasetSchema(dsA.Schema)
	edited := currentA
	edited.Fields = append(append([]model.FieldSpec{}, currentA.Fields...),
		model.FieldSpec{Key: "region", Label: "区域", Type: model.FieldString, Filterable: true})
	if _, err := svc.UpdateDatasetSchema(ctx, dsA.ID, DatasetSchemaUpdateInput{Schema: edited}); err != nil {
		t.Fatalf("A 编辑失败（✅ 新增可选应直接放行）: %v", err)
	}
	a, _ := datasets.GetDataset(ctx, dsA.ID)
	b, _ := datasets.GetDataset(ctx, dsB.ID)
	pa, _ := model.ParseDatasetSchema(a.Schema)
	pb, _ := model.ParseDatasetSchema(b.Schema)
	_, aHas := pa.Field("region")
	_, bHas := pb.Field("region")
	if !aHas {
		t.Fatal("A 应含新字段")
	}
	if bHas {
		t.Fatal("B 不应被 A 的编辑波及（字段定义归属各自数据集）")
	}
	if a.SchemaVersion != 2 || b.SchemaVersion != 1 {
		t.Fatalf("版本应各自独立: A=%d B=%d", a.SchemaVersion, b.SchemaVersion)
	}
}
