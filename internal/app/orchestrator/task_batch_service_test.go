package orchestrator

import (
	"context"
	"strings"
	"testing"

	"reqflow/internal/domain/model"
)

func TestTaskBatchServiceCreatesIndependentTaskPerAsset(t *testing.T) {
	repo := &memoryOrchestratorRepo{}
	definitions := NewDefinitionService(repo, definitionTestRegistry(t), repo)
	definition, err := definitions.Create(context.Background(), activeDefinition())
	if err != nil {
		t.Fatal(err)
	}
	assets := &batchAssetStub{entries: []model.AssetSetEntry{
		{Asset: model.Asset{ID: "asset-1", Filename: "需求说明.docx"}},
		{Asset: model.Asset{ID: "asset-2", Filename: "产品清单.xlsx"}},
	}}
	service, err := NewTaskBatchService(definitions, assets)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := service.CreateExecutions(context.Background(), CreateBatchExecutionInput{
		DefinitionID:  definition.ID,
		Title:         "DH1 文档入库",
		SplitPortName: "documents",
		Bindings: []ResourceBindingInput{
			{PortName: "documents", ResourceType: string(model.ResourceAssetSet), ResourceID: "source-set"},
			{PortName: "target", ResourceType: string(model.ResourceDataset), ResourceID: "target-dataset"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if batch.Size != 2 || len(batch.Tasks) != 2 || batch.ID == "" {
		t.Fatalf("批次创建结果不完整: %+v", batch)
	}
	for i, task := range batch.Tasks {
		if task.BatchID != batch.ID || task.BatchOrdinal != i+1 || task.BatchSize != 2 {
			t.Fatalf("子任务批次信息错误: %+v", task)
		}
		if !strings.Contains(task.Title, assets.entries[i].Asset.Filename) {
			t.Fatalf("子任务标题未包含文件名: %s", task.Title)
		}
		if task.SourceAssetID != assets.entries[i].Asset.ID {
			t.Fatalf("子任务来源文件错误: %+v", task)
		}
		if got := repo.executions[i].Bindings[0].ResourceID; got != assets.createdSets[i].ID {
			t.Fatalf("子任务未绑定独立文件集: %s", got)
		}
		if got := repo.executions[i].Bindings[1].ResourceID; got != "target-dataset" {
			t.Fatalf("共享资源绑定被错误修改: %s", got)
		}
	}
}

type batchAssetStub struct {
	entries     []model.AssetSetEntry
	createdSets []model.AssetSet
}

func (s *batchAssetStub) GetAssetSet(context.Context, string) (*model.AssetSet, []model.AssetSetEntry, error) {
	return &model.AssetSet{ID: "source-set", WorkspaceID: "default", Name: "本次资料"}, s.entries, nil
}

func (s *batchAssetStub) CreateSingleAssetSet(_ context.Context, workspaceID, name, createdBy, assetID string) (*model.AssetSet, error) {
	set := model.AssetSet{ID: "single-" + assetID, WorkspaceID: workspaceID, Name: name, CreatedBy: createdBy}
	s.createdSets = append(s.createdSets, set)
	return &set, nil
}
