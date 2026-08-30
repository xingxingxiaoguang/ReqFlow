//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	apporchestrator "reqflow/internal/app/orchestrator"
	apppipeline "reqflow/internal/app/pipeline"
	appretrieval "reqflow/internal/app/retrieval"
	"reqflow/internal/domain/model"
	"reqflow/internal/infra/database"
	"reqflow/internal/port"
)

func TestIntegrationRetrievalBuildIncrementalAndPgvectorSearch(t *testing.T) {
	ctx := context.Background()
	db, err := database.Connect(testDSN(), 3, 500)
	if err != nil {
		t.Skipf("本地 PG 不可用，跳过集成测试: %v", err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	repo := NewPipelineRepo(db)
	datasets := apppipeline.NewDatasetService(repo)
	lexical := newIntegrationLexical()
	service, err := appretrieval.NewService(repo, lexical, integrationEmbedder{}, integrationReranker{},
		appretrieval.Options{EmbeddingModel: "BAAI/bge-m3", PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	executor, _ := appretrieval.NewBuildExecutor(service)
	registry, _ := apporchestrator.NewRegistry(executor)
	definitions := apporchestrator.NewDefinitionService(repo, registry, repo)
	scheduler := apporchestrator.NewScheduler(repo)
	worker, _ := apporchestrator.NewWorker(repo, registry, scheduler, apporchestrator.WorkerOptions{
		LeaseDuration: 2 * time.Second, PollInterval: 10 * time.Millisecond,
		RecoveryInterval: 100 * time.Millisecond, ReconcileLimit: 20,
	})
	runtime, _ := apporchestrator.NewRuntimeService(repo, scheduler, worker)
	workerCtx, cancelWorker := context.WithCancel(ctx)
	workerDone := make(chan error, 1)
	go func() { workerDone <- worker.Run(workerCtx) }()
	t.Cleanup(func() {
		cancelWorker()
		select {
		case <-workerDone:
		case <-time.After(2 * time.Second):
		}
	})

	schema, err := datasets.CreateSchema(ctx, apppipeline.CreateSchemaInput{Name: "Stage E Query",
		JSONSchema: json.RawMessage(`{"type":"object","properties":{
			"key":{"type":"string"},"title":{"type":"string"},"aliases":{"type":"array","items":{"type":"string"}},
			"definition":{"type":"string"},"product_family":{"type":"string"}
		},"required":["key","title","aliases","definition","product_family"],"additionalProperties":false}`)})
	if err != nil {
		t.Fatal(err)
	}
	dataset, err := datasets.CreateDataset(ctx, apppipeline.CreateDatasetInput{Name: "Stage E Query",
		Purpose: model.DatasetPurposeQuery, SchemaID: schema.ID, KeyFields: []string{"key"}})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := service.CreateProfile(ctx, appretrieval.CreateProfileInput{WorkspaceID: "default",
		Name: "Stage E Retrieval", DatasetSchemaID: schema.ID,
		Lexical: model.LexicalConfig{Fields: map[string]float64{"title": 3, "aliases": 2, "definition": 1}, Analyzer: "standard"},
		Vector: model.VectorConfig{Fields: []string{"title", "definition"}, ChunkSize: 100,
			ChunkOverlap: 20, EmbeddingModel: "platform_default"},
		FilterFields: []string{"product_family"},
		Fusion:       model.FusionConfig{Method: "rrf", RankConstant: 60, LexicalCandidates: 20, VectorCandidates: 20}})
	if err != nil {
		t.Fatal(err)
	}
	definition := createIntegrationRetrievalDefinition(t, ctx, definitions, profile.ID)
	var taskIDs []string
	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM retrieval_snapshots WHERE retrieval_profile_id = ?`, profile.ID).Error
		for _, taskID := range taskIDs {
			_ = db.Exec(`DELETE FROM tasks WHERE id = ?`, taskID).Error
		}
		_ = db.Exec(`DELETE FROM task_definitions WHERE id = ?`, definition.ID).Error
		_ = db.Exec(`DELETE FROM datasets WHERE id = ?`, dataset.ID).Error
		_ = db.Exec(`DELETE FROM retrieval_profiles WHERE id = ?`, profile.ID).Error
		_ = db.Exec(`DELETE FROM dataset_schemas WHERE id = ?`, schema.ID).Error
	})

	commitIntegrationRetrievalItem(t, ctx, datasets, dataset.ID, "A", "过温保护", "温度超过阈值自动关断", "X100")
	commitIntegrationRetrievalItem(t, ctx, datasets, dataset.ID, "B", "欠压保护", "电压过低时禁止启动", "X200")
	firstTask := createAndStartRetrievalTask(t, ctx, definitions, runtime, definition.ID, dataset.ID, "Retrieval 1")
	taskIDs = append(taskIDs, firstTask.ID)
	waitIntegrationTaskStatus(t, ctx, repo, firstTask.ID, model.TaskStatusSucceeded)
	firstExecution, err := repo.GetTaskExecution(ctx, firstTask.ID)
	if err != nil || len(firstExecution.StepOutputs) != 1 {
		t.Fatalf("retrieval.build 输出异常: %+v err=%v", firstExecution, err)
	}
	firstSnapshot, err := repo.GetRetrievalSnapshot(ctx, firstExecution.StepOutputs[0].ResourceID)
	if err != nil || firstSnapshot.Status != model.RetrievalSnapshotActive ||
		firstSnapshot.LexicalCount != 2 || firstSnapshot.VectorCount != 2 {
		t.Fatalf("首个 Snapshot 未激活: %+v err=%v", firstSnapshot, err)
	}

	vectorHits, err := repo.SearchRetrievalChunks(ctx, port.VectorSearchRequest{DatasetID: dataset.ID,
		RetrievalProfileID: profile.ID, QueryEmbedding: integrationVector("关断"), SourceSeq: 2,
		Filters: map[string][]string{"product_family": {"X100"}}, Limit: 10})
	if err != nil || len(vectorHits) != 1 {
		t.Fatalf("pgvector + filter 查询失败: hits=%+v err=%v", vectorHits, err)
	}

	commitIntegrationRetrievalItem(t, ctx, datasets, dataset.ID, "C", "短路保护", "短路后立即关断", "X100")
	lexical.resetBuildCount()
	secondTask := createAndStartRetrievalTask(t, ctx, definitions, runtime, definition.ID, dataset.ID, "Retrieval 2")
	taskIDs = append(taskIDs, secondTask.ID)
	waitIntegrationTaskStatus(t, ctx, repo, secondTask.ID, model.TaskStatusSucceeded)
	if lexical.buildCount() != 1 {
		t.Fatalf("第二次 Snapshot 应只写新增 lexical document: got %d", lexical.buildCount())
	}
	secondExecution, _ := repo.GetTaskExecution(ctx, secondTask.ID)
	secondSnapshot, _ := repo.GetRetrievalSnapshot(ctx, secondExecution.StepOutputs[0].ResourceID)
	if secondSnapshot.SourceSeq != 3 || secondSnapshot.LexicalCount != 3 || secondSnapshot.VectorCount != 3 {
		t.Fatalf("增量 Snapshot 计数异常: %+v", secondSnapshot)
	}
}

func createIntegrationRetrievalDefinition(t *testing.T, ctx context.Context,
	definitions *apporchestrator.DefinitionService, profileID string) *model.TaskDefinition {
	t.Helper()
	config, _ := json.Marshal(map[string]any{"retrieval_profile_id": profileID})
	definition, err := definitions.Create(ctx, model.TaskDefinition{Key: fmt.Sprintf("stage_e_%d", time.Now().UnixNano()),
		Name: "Stage E Retrieval Build", Status: model.TaskDefinitionActive,
		InputPorts:     map[string]model.PortDefinition{"dataset": {ResourceType: model.ResourceDatasetBoundary, Required: true}},
		OutputPorts:    map[string]model.PortDefinition{"snapshot": {ResourceType: model.ResourceRetrievalSnapshot}},
		OutputBindings: map[string]string{"snapshot": "$step.build.snapshot"},
		Steps: []model.StepDefinition{{ID: "build", Name: "构建混合检索快照", Kind: model.StepKindRetrievalBuild,
			Inputs:  map[string]string{"dataset": "$task.dataset"},
			Outputs: map[string]model.ResourceType{"snapshot": model.ResourceRetrievalSnapshot}, Config: config}}})
	if err != nil {
		t.Fatal(err)
	}
	return definition
}

func createAndStartRetrievalTask(t *testing.T, ctx context.Context,
	definitions *apporchestrator.DefinitionService, runtime *apporchestrator.RuntimeService,
	definitionID, datasetID, title string) *model.Task {
	t.Helper()
	task, err := definitions.CreateTask(ctx, apporchestrator.CreateTaskInput{DefinitionID: definitionID,
		Title: title, Bindings: []model.TaskResourceBinding{{PortName: "dataset",
			ResourceType: model.ResourceDatasetBoundary, ResourceID: datasetID}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	return task
}

func commitIntegrationRetrievalItem(t *testing.T, ctx context.Context, datasets *apppipeline.DatasetService,
	datasetID, key, title, definition, family string) {
	t.Helper()
	batch, err := datasets.CreateBatch(ctx, apppipeline.CreateBatchInput{DatasetID: datasetID})
	if err != nil {
		t.Fatal(err)
	}
	_, err = datasets.CommitBatch(ctx, batch.ID, []apppipeline.BatchItemInput{{Fields: map[string]any{
		"key": key, "title": title, "aliases": []any{title}, "definition": definition,
		"product_family": family,
	}, Provenance: model.ItemProvenance{}}})
	if err != nil {
		t.Fatal(err)
	}
}

type integrationEmbedder struct{}

func (integrationEmbedder) Available() bool { return true }
func (integrationEmbedder) Generate(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, text := range texts {
		out[i] = integrationVector(text)
	}
	return out, nil
}

func integrationVector(text string) []float32 {
	vector := make([]float32, 1024)
	for i, b := range []byte(text) {
		vector[i%len(vector)] += float32(b%31 + 1)
	}
	return vector
}

type integrationReranker struct{}

func (integrationReranker) Available() bool { return true }
func (integrationReranker) Rerank(_ context.Context, _ string, documents []string, topN int) ([]port.RerankResult, error) {
	if topN > len(documents) {
		topN = len(documents)
	}
	out := make([]port.RerankResult, topN)
	for i := range out {
		out[i] = port.RerankResult{Index: i, Score: 1 - float64(i)/100}
	}
	return out, nil
}

type integrationLexical struct {
	mu            sync.Mutex
	documents     map[string]port.LexicalDocument
	recentlyBuilt int
}

func newIntegrationLexical() *integrationLexical {
	return &integrationLexical{documents: map[string]port.LexicalDocument{}}
}
func (*integrationLexical) Available() bool { return true }
func (l *integrationLexical) Build(_ context.Context, req port.LexicalBuildRequest) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.recentlyBuilt += len(req.Documents)
	for _, document := range req.Documents {
		l.documents[document.DatasetItemID] = document
	}
	return nil
}
func (l *integrationLexical) Count(_ context.Context, _ string, sourceSeq int64) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	count := 0
	for _, document := range l.documents {
		if document.SourceSeq <= sourceSeq {
			count++
		}
	}
	return count, nil
}
func (l *integrationLexical) Search(_ context.Context, req port.LexicalSearchRequest) ([]port.RankedHit, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []port.RankedHit
	for _, document := range l.documents {
		if document.SourceSeq <= req.SourceSeq && strings.Contains(fmt.Sprint(document.Fields), req.Query) {
			out = append(out, port.RankedHit{DatasetItemID: document.DatasetItemID, Score: 1})
		}
	}
	for i := range out {
		out[i].Rank = i + 1
	}
	return out, nil
}
func (l *integrationLexical) resetBuildCount() { l.mu.Lock(); l.recentlyBuilt = 0; l.mu.Unlock() }
func (l *integrationLexical) buildCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.recentlyBuilt
}
