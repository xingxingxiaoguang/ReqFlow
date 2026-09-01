package retrieval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"reqflow/internal/app/agent"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

func TestBuildSnapshotIncrementalAndCoverageGate(t *testing.T) {
	ctx := context.Background()
	repo := newRetrievalMemoryRepo()
	schema := retrievalTestSchema()
	repo.schemas[schema.ID] = &schema
	dataset := &model.Dataset{ID: "dataset-1", WorkspaceID: "default", SchemaID: schema.ID,
		Status: model.DatasetStatusActive, CurrentSeq: 2}
	repo.datasets[dataset.ID] = dataset
	repo.items[dataset.ID] = []model.DatasetItem{
		retrievalTestItem("item-1", dataset.ID, 1, "Apple shutdown", "high temperature"),
		retrievalTestItem("item-2", dataset.ID, 2, "Banana alarm", "low voltage"),
	}
	lexical := newFakeLexical()
	embedder := fakeRetrievalEmbedder{available: true}
	service, err := NewService(repo, lexical, embedder, &fakeReranker{available: true},
		Options{EmbeddingModel: "BAAI/bge-m3", PageSize: 1})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := service.CreateProfile(ctx, retrievalTestProfileInput(schema.ID))
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.BuildSnapshot(ctx, BuildInput{DatasetID: dataset.ID,
		RetrievalProfileID: profile.ID, SourceSeq: 2, StepRunID: "step-1", ProducerAttempt: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != model.RetrievalSnapshotActive || first.LexicalCount != 2 || first.VectorCount != 2 {
		t.Fatalf("首个 Snapshot 异常: %+v", first)
	}
	if lexical.builtDocuments != 2 {
		t.Fatalf("首批 lexical documents = %d, want 2", lexical.builtDocuments)
	}

	dataset.CurrentSeq = 3
	repo.items[dataset.ID] = append(repo.items[dataset.ID],
		retrievalTestItem("item-3", dataset.ID, 3, "Cherry thermal", "automatic shutdown"))
	lexical.builtDocuments = 0
	second, err := service.BuildSnapshot(ctx, BuildInput{DatasetID: dataset.ID,
		RetrievalProfileID: profile.ID, SourceSeq: 3, StepRunID: "step-2", ProducerAttempt: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if lexical.builtDocuments != 1 {
		t.Fatalf("增量构建 documents = %d, want 1", lexical.builtDocuments)
	}
	if second.Status != model.RetrievalSnapshotActive || second.SourceSeq != 3 || second.LexicalCount != 3 {
		t.Fatalf("第二个 Snapshot 异常: %+v", second)
	}

	lexical.countAdjustment = -1
	dataset.CurrentSeq = 4
	repo.items[dataset.ID] = append(repo.items[dataset.ID],
		retrievalTestItem("item-4", dataset.ID, 4, "Date sensor", "sensor failure"))
	failed, err := service.BuildSnapshot(ctx, BuildInput{DatasetID: dataset.ID,
		RetrievalProfileID: profile.ID, SourceSeq: 4, StepRunID: "step-3", ProducerAttempt: 1}, nil)
	if err == nil || !strings.Contains(err.Error(), "覆盖不完整") {
		t.Fatalf("覆盖不完整应失败，got snapshot=%+v err=%v", failed, err)
	}
	stored := repo.snapshotByStep("step-3")
	if stored == nil || stored.Status != model.RetrievalSnapshotFailed {
		t.Fatalf("失败 Snapshot 未落 failed: %+v", stored)
	}
}

func TestSearchRuntimeStrategyAndRerank(t *testing.T) {
	ctx := context.Background()
	repo := newRetrievalMemoryRepo()
	schema := retrievalTestSchema()
	repo.schemas[schema.ID] = &schema
	dataset := &model.Dataset{ID: "dataset-search", WorkspaceID: "default", SchemaID: schema.ID,
		Status: model.DatasetStatusActive, CurrentSeq: 2}
	repo.datasets[dataset.ID] = dataset
	repo.items[dataset.ID] = []model.DatasetItem{
		retrievalTestItem("item-a", dataset.ID, 1, "Apple", "thermal shutdown"),
		retrievalTestItem("item-b", dataset.ID, 2, "Banana", "power alarm"),
	}
	lexical := newFakeLexical()
	lexical.searchHits = []port.RankedHit{{DatasetItemID: "item-a", Rank: 1, Score: 12},
		{DatasetItemID: "item-b", Rank: 2, Score: 9}}
	repo.vectorHits = []port.RankedHit{{DatasetItemID: "item-b", ChunkID: "chunk-b", Text: "power alarm", Rank: 1, Score: .91},
		{DatasetItemID: "item-a", ChunkID: "chunk-a", Text: "thermal shutdown", Rank: 2, Score: .82}}
	reranker := &fakeReranker{available: true, results: []port.RerankResult{{Index: 1, Score: .95}, {Index: 0, Score: .4}}}
	service, _ := NewService(repo, lexical, fakeRetrievalEmbedder{available: true}, reranker,
		Options{EmbeddingModel: "BAAI/bge-m3"})
	profile, err := service.CreateProfile(ctx, retrievalTestProfileInput(schema.ID))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &model.RetrievalSnapshot{ID: "snapshot-search", DatasetID: dataset.ID,
		RetrievalProfileID: profile.ID, SourceSeq: 2, Status: model.RetrievalSnapshotActive,
		LexicalRef: "index-search"}
	repo.snapshots[snapshot.ID] = snapshot

	response, err := service.Search(ctx, SearchRequest{RetrievalSnapshotID: snapshot.ID, Query: "shutdown",
		Strategy: model.RetrievalSearchStrategy{Mode: model.RetrievalModeHybrid,
			LexicalWeight: 8, SemanticWeight: 2, RecallLimit: 10, TopK: 2,
			RerankEnabled: true, RerankTopN: 2}})
	if err != nil {
		t.Fatal(err)
	}
	if response.Strategy.LexicalWeight != .8 || response.Strategy.SemanticWeight != .2 {
		t.Fatalf("权重未归一化: %+v", response.Strategy)
	}
	if len(response.Hits) != 2 || response.Hits[0].DatasetItemID != "item-b" || response.Hits[0].RerankScore == nil {
		t.Fatalf("rerank 未重排候选: %+v", response.Hits)
	}
	if reranker.topN != 2 || len(reranker.documents) != 2 {
		t.Fatalf("rerank 参数未透传: topN=%d docs=%d", reranker.topN, len(reranker.documents))
	}
	response, err = service.Search(ctx, SearchRequest{RetrievalSnapshotID: snapshot.ID, Query: "shutdown",
		Strategy: model.RetrievalSearchStrategy{Mode: model.RetrievalModeHybrid,
			LexicalWeight: 8, SemanticWeight: 2, RecallLimit: 10, TopK: 2,
			RerankEnabled: true, RerankTopN: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Hits) != 1 || response.Hits[0].DatasetItemID != "item-b" {
		t.Fatalf("rerank_top_n 未限制最终结果: %+v", response.Hits)
	}
	// 阈值必须作用于 rerank 后的最终分数：rerank 分 0.95/0.4，阈值 0.5 只保留 0.95。
	response, err = service.Search(ctx, SearchRequest{RetrievalSnapshotID: snapshot.ID, Query: "shutdown",
		Strategy: model.RetrievalSearchStrategy{Mode: model.RetrievalModeHybrid,
			LexicalWeight: 8, SemanticWeight: 2, RecallLimit: 10, TopK: 2,
			RerankEnabled: true, RerankTopN: 2, ScoreThreshold: 0.5}})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Hits) != 1 || response.Hits[0].DatasetItemID != "item-b" ||
		response.Hits[0].Score < 0.9 || response.Hits[0].RerankScore == nil {
		t.Fatalf("score_threshold 未过滤 rerank 后的最终分数: %+v", response.Hits)
	}

	_, err = service.Search(ctx, SearchRequest{RetrievalSnapshotID: snapshot.ID, Query: "x",
		Filters:  map[string][]string{"forbidden": {"x"}},
		Strategy: model.RetrievalSearchStrategy{Mode: model.RetrievalModeLexical, RecallLimit: 5, TopK: 2}})
	if err == nil || !strings.Contains(err.Error(), "白名单") {
		t.Fatalf("非法 filter 应拒绝: %v", err)
	}

	// 默认不开 rerank：最终分保持原始 RRF 量纲（≈0.01x），不调用 reranker，阈值被归零。
	quietReranker := &fakeReranker{available: true}
	quietService, _ := NewService(repo, lexical, fakeRetrievalEmbedder{available: true}, quietReranker,
		Options{EmbeddingModel: "BAAI/bge-m3"})
	response, err = quietService.Search(ctx, SearchRequest{RetrievalSnapshotID: snapshot.ID, Query: "shutdown",
		Strategy: model.RetrievalSearchStrategy{Mode: model.RetrievalModeHybrid,
			LexicalWeight: 8, SemanticWeight: 2, RecallLimit: 10, TopK: 2, ScoreThreshold: 0.5}})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Hits) != 2 || quietReranker.documents != nil {
		t.Fatalf("未开 rerank 不应调用 reranker，也不应被阈值误杀: hits=%d reranked=%v",
			len(response.Hits), quietReranker.documents)
	}
	if response.Hits[0].Score <= 0 || response.Hits[0].Score > 0.05 {
		t.Fatalf("融合分应保持原始 RRF 量纲: %v", response.Hits[0].Score)
	}
	if response.Strategy.RerankEnabled || response.Strategy.ScoreThreshold != 0 {
		t.Fatalf("无 rerank 时策略应回退: %+v", response.Strategy)
	}

	// reranker 未配置：显式开启 rerank 也应降级为纯融合，而不是让搜索失败。
	offlineService, _ := NewService(repo, lexical, fakeRetrievalEmbedder{available: true},
		&fakeReranker{available: false}, Options{EmbeddingModel: "BAAI/bge-m3"})
	response, err = offlineService.Search(ctx, SearchRequest{RetrievalSnapshotID: snapshot.ID, Query: "shutdown",
		Strategy: model.RetrievalSearchStrategy{Mode: model.RetrievalModeHybrid,
			LexicalWeight: 8, SemanticWeight: 2, RecallLimit: 10, TopK: 2,
			RerankEnabled: true, RerankTopN: 2, ScoreThreshold: 0.5}})
	if err != nil {
		t.Fatalf("reranker 缺失应降级而不是失败: %v", err)
	}
	if len(response.Hits) != 2 || response.Strategy.RerankEnabled || response.Strategy.ScoreThreshold != 0 {
		t.Fatalf("降级后应返回全量融合结果并回显降级策略: hits=%d strategy=%+v",
			len(response.Hits), response.Strategy)
	}
}

func TestKnowledgeToolsEnforceLogicalScopeAndAudit(t *testing.T) {
	ctx := context.Background()
	repo := newRetrievalMemoryRepo()
	schema := retrievalTestSchema()
	repo.schemas[schema.ID] = &schema
	dataset := &model.Dataset{ID: "dataset-scope", WorkspaceID: "default", SchemaID: schema.ID,
		Status: model.DatasetStatusActive, CurrentSeq: 1}
	repo.datasets[dataset.ID] = dataset
	repo.items[dataset.ID] = []model.DatasetItem{retrievalTestItem("item-scope", dataset.ID, 1, "Scoped", "knowledge")}
	lexical := newFakeLexical()
	service, _ := NewService(repo, lexical, fakeRetrievalEmbedder{available: true},
		&fakeReranker{available: true}, Options{EmbeddingModel: "BAAI/bge-m3"})
	profile, _ := service.CreateProfile(ctx, retrievalTestProfileInput(schema.ID))
	snapshot := &model.RetrievalSnapshot{ID: "snapshot-scope", DatasetID: dataset.ID,
		RetrievalProfileID: profile.ID, SourceSeq: 1, Status: model.RetrievalSnapshotActive,
		LexicalRef: "index-scope"}
	repo.snapshots[snapshot.ID] = snapshot
	tools, err := service.BuildKnowledgeTools(ctx, KnowledgeScope{ID: "task-scope", WorkspaceID: "default",
		Sources: map[string]KnowledgeSource{"product_specs": {Name: "product_specs", SnapshotID: snapshot.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	var searchTool interface {
		Execute(context.Context, port.ToolCall, func(string)) agent.ToolOutput
	}
	for _, tool := range tools {
		if tool.Spec().Name == "search_knowledge" {
			searchTool = tool
		}
	}
	if searchTool == nil {
		t.Fatal("缺少 search_knowledge")
	}
	out := searchTool.Execute(ctx, port.ToolCall{Name: "search_knowledge",
		Arguments: json.RawMessage(`{"source":"secret_dataset","query":"x"}`)}, nil)
	if !out.IsError || !strings.Contains(out.Output, "不在当前 KnowledgeScope") {
		t.Fatalf("越权 source 未拒绝: %+v", out)
	}
	if len(repo.audits) != 1 || repo.audits[0].SourceName != "secret_dataset" || repo.audits[0].ErrorMessage == "" {
		t.Fatalf("越权调用未审计: %+v", repo.audits)
	}
}

func retrievalTestSchema() model.DatasetSchemaDefinition {
	return model.DatasetSchemaDefinition{ID: "schema-retrieval", WorkspaceID: "default", SchemaHash: "schema-hash",
		JSONSchema: json.RawMessage(`{"type":"object","properties":{
			"title":{"type":"string"},"aliases":{"type":"array","items":{"type":"string"}},
			"definition":{"type":"string"},"product_family":{"type":"string"}
		},"required":["title","definition"],"additionalProperties":false}`)}
}

func retrievalTestProfileInput(schemaID string) CreateProfileInput {
	return CreateProfileInput{WorkspaceID: "default", Name: "default", DatasetSchemaID: schemaID,
		Lexical: model.LexicalConfig{Fields: map[string]float64{"title": 3, "aliases": 2, "definition": 1}, Analyzer: "standard"},
		Vector: model.VectorConfig{Fields: []string{"title", "definition"}, ChunkSize: 100,
			ChunkOverlap: 20, EmbeddingModel: "platform_default"},
		FilterFields: []string{"product_family"},
		Fusion:       model.FusionConfig{Method: "rrf", RankConstant: 60, LexicalCandidates: 10, VectorCandidates: 10}}
}

func retrievalTestItem(id, datasetID string, seq int64, title, definition string) model.DatasetItem {
	fields, _ := json.Marshal(map[string]any{"title": title, "aliases": []string{title},
		"definition": definition, "product_family": "X100"})
	return model.DatasetItem{ID: id, DatasetID: datasetID, CommitSeq: seq, Fields: string(fields), Provenance: `{}`}
}

type fakeRetrievalEmbedder struct{ available bool }

func (f fakeRetrievalEmbedder) Available() bool { return f.available }
func (f fakeRetrievalEmbedder) Generate(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i := range out {
		out[i] = []float32{float32(i + 1), 0, 0}
	}
	return out, nil
}

type fakeReranker struct {
	available bool
	results   []port.RerankResult
	topN      int
	documents []string
}

func (f *fakeReranker) Available() bool { return f.available }
func (f *fakeReranker) Rerank(_ context.Context, _ string, documents []string, topN int) ([]port.RerankResult, error) {
	f.topN, f.documents = topN, append([]string(nil), documents...)
	if len(f.results) > 0 {
		return append([]port.RerankResult(nil), f.results...), nil
	}
	out := make([]port.RerankResult, 0, topN)
	for i := 0; i < topN && i < len(documents); i++ {
		out = append(out, port.RerankResult{Index: i, Score: 1 - float64(i)/10})
	}
	return out, nil
}

type fakeLexical struct {
	documents       map[string]port.LexicalDocument
	searchHits      []port.RankedHit
	builtDocuments  int
	countAdjustment int
}

func newFakeLexical() *fakeLexical   { return &fakeLexical{documents: map[string]port.LexicalDocument{}} }
func (*fakeLexical) Available() bool { return true }
func (f *fakeLexical) Build(_ context.Context, req port.LexicalBuildRequest) error {
	f.builtDocuments += len(req.Documents)
	for _, document := range req.Documents {
		f.documents[document.DatasetItemID] = document
	}
	return nil
}
func (f *fakeLexical) Count(_ context.Context, _ string, sourceSeq int64) (int, error) {
	count := 0
	for _, document := range f.documents {
		if document.SourceSeq <= sourceSeq {
			count++
		}
	}
	return count + f.countAdjustment, nil
}
func (f *fakeLexical) Search(_ context.Context, _ port.LexicalSearchRequest) ([]port.RankedHit, error) {
	return append([]port.RankedHit(nil), f.searchHits...), nil
}

type retrievalMemoryRepo struct {
	next       int
	schemas    map[string]*model.DatasetSchemaDefinition
	datasets   map[string]*model.Dataset
	profiles   map[string]*model.RetrievalProfile
	snapshots  map[string]*model.RetrievalSnapshot
	items      map[string][]model.DatasetItem
	chunks     map[string]model.RetrievalChunk
	vectorHits []port.RankedHit
	audits     []port.KnowledgeToolAudit
}

func newRetrievalMemoryRepo() *retrievalMemoryRepo {
	return &retrievalMemoryRepo{schemas: map[string]*model.DatasetSchemaDefinition{},
		datasets: map[string]*model.Dataset{}, profiles: map[string]*model.RetrievalProfile{},
		snapshots: map[string]*model.RetrievalSnapshot{}, items: map[string][]model.DatasetItem{},
		chunks: map[string]model.RetrievalChunk{}}
}
func (r *retrievalMemoryRepo) id(prefix string) string {
	r.next++
	return fmt.Sprintf("%s-%d", prefix, r.next)
}
func (r *retrievalMemoryRepo) GetAppendDataset(_ context.Context, id string) (*model.Dataset, error) {
	value, ok := r.datasets[id]
	if !ok {
		return nil, errors.New("not found")
	}
	clone := *value
	return &clone, nil
}
func (r *retrievalMemoryRepo) GetDatasetSchema(_ context.Context, id string) (*model.DatasetSchemaDefinition, error) {
	value, ok := r.schemas[id]
	if !ok {
		return nil, errors.New("not found")
	}
	clone := *value
	return &clone, nil
}
func (r *retrievalMemoryRepo) ListDatasetItemsAfter(_ context.Context, datasetID string, afterSeq, throughSeq int64, limit int) ([]model.DatasetItem, error) {
	var out []model.DatasetItem
	for _, item := range r.items[datasetID] {
		if item.CommitSeq > afterSeq && item.CommitSeq <= throughSeq {
			out = append(out, item)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}
func (r *retrievalMemoryRepo) CreateRetrievalProfile(_ context.Context, profile *model.RetrievalProfile) error {
	profile.ID = r.id("profile")
	clone := *profile
	r.profiles[profile.ID] = &clone
	return nil
}
func (r *retrievalMemoryRepo) GetRetrievalProfile(_ context.Context, id string) (*model.RetrievalProfile, error) {
	value, ok := r.profiles[id]
	if !ok {
		return nil, errors.New("not found")
	}
	clone := *value
	return &clone, nil
}
func (r *retrievalMemoryRepo) ListRetrievalProfiles(_ context.Context, workspaceID, schemaID string, limit int) ([]model.RetrievalProfile, error) {
	var out []model.RetrievalProfile
	for _, value := range r.profiles {
		if value.WorkspaceID == workspaceID && (schemaID == "" || value.DatasetSchemaID == schemaID) {
			out = append(out, *value)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}
func (r *retrievalMemoryRepo) GetOrCreateRetrievalSnapshotForStep(_ context.Context, snapshot *model.RetrievalSnapshot, _ int) (*model.RetrievalSnapshot, error) {
	if existing := r.snapshotByStep(snapshot.SourceStepRunID); existing != nil {
		clone := *existing
		if clone.Status != model.RetrievalSnapshotActive {
			clone.Status = model.RetrievalSnapshotBuilding
			r.snapshots[clone.ID] = &clone
		}
		return &clone, nil
	}
	snapshot.ID = r.id("snapshot")
	clone := *snapshot
	r.snapshots[clone.ID] = &clone
	return &clone, nil
}
func (r *retrievalMemoryRepo) GetRetrievalSnapshot(_ context.Context, id string) (*model.RetrievalSnapshot, error) {
	value, ok := r.snapshots[id]
	if !ok {
		return nil, errors.New("not found")
	}
	clone := *value
	return &clone, nil
}
func (r *retrievalMemoryRepo) ListRetrievalSnapshots(_ context.Context, datasetID, profileID, status string, limit int) ([]model.RetrievalSnapshot, error) {
	var out []model.RetrievalSnapshot
	for _, value := range r.snapshots {
		if value.DatasetID == datasetID && (profileID == "" || value.RetrievalProfileID == profileID) && (status == "" || value.Status == status) {
			out = append(out, *value)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}
func (r *retrievalMemoryRepo) GetLatestActiveRetrievalSnapshot(_ context.Context, datasetID, profileID string, throughSeq int64) (*model.RetrievalSnapshot, error) {
	var found *model.RetrievalSnapshot
	for _, value := range r.snapshots {
		if value.DatasetID == datasetID && value.RetrievalProfileID == profileID && value.Status == model.RetrievalSnapshotActive && value.SourceSeq <= throughSeq && (found == nil || value.SourceSeq > found.SourceSeq) {
			clone := *value
			found = &clone
		}
	}
	if found == nil {
		return nil, port.ErrRetrievalSnapshotNotFound
	}
	return found, nil
}
func (r *retrievalMemoryRepo) SetRetrievalSnapshotStatusForStep(_ context.Context, snapshotID, _ string, _ int, status, reason string) error {
	r.snapshots[snapshotID].Status, r.snapshots[snapshotID].FailureReason = status, reason
	return nil
}
func (r *retrievalMemoryRepo) ActivateRetrievalSnapshotForStep(_ context.Context, snapshotID, _ string, _ int, lexicalRef, vectorRef string, lexicalCount, vectorCount int) (*model.RetrievalSnapshot, error) {
	value := r.snapshots[snapshotID]
	value.Status, value.LexicalRef, value.VectorRef = model.RetrievalSnapshotActive, lexicalRef, vectorRef
	value.LexicalCount, value.VectorCount = lexicalCount, vectorCount
	clone := *value
	return &clone, nil
}
func (r *retrievalMemoryRepo) UpsertRetrievalChunks(_ context.Context, chunks []model.RetrievalChunk) error {
	for _, chunk := range chunks {
		key := fmt.Sprintf("%s:%s:%d", chunk.DatasetItemID, chunk.RetrievalProfileID, chunk.ChunkNo)
		r.chunks[key] = chunk
	}
	return nil
}
func (r *retrievalMemoryRepo) CountRetrievalChunks(_ context.Context, datasetID, profileID string, sourceSeq int64) (int, int, error) {
	items := map[string]bool{}
	count := 0
	for _, chunk := range r.chunks {
		if chunk.DatasetID == datasetID && chunk.RetrievalProfileID == profileID && chunk.SourceSeq <= sourceSeq {
			count++
			items[chunk.DatasetItemID] = true
		}
	}
	return count, len(items), nil
}
func (r *retrievalMemoryRepo) SearchRetrievalChunks(_ context.Context, _ port.VectorSearchRequest) ([]port.RankedHit, error) {
	return append([]port.RankedHit(nil), r.vectorHits...), nil
}
func (r *retrievalMemoryRepo) GetDatasetItemsByIDs(_ context.Context, datasetID string, sourceSeq int64, ids []string) ([]model.DatasetItem, error) {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []model.DatasetItem
	for _, item := range r.items[datasetID] {
		if want[item.ID] && item.CommitSeq <= sourceSeq {
			out = append(out, item)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CommitSeq < out[j].CommitSeq })
	return out, nil
}
func (r *retrievalMemoryRepo) AppendKnowledgeToolAudit(_ context.Context, audit port.KnowledgeToolAudit) error {
	r.audits = append(r.audits, audit)
	return nil
}
func (r *retrievalMemoryRepo) snapshotByStep(stepID string) *model.RetrievalSnapshot {
	for _, snapshot := range r.snapshots {
		if snapshot.SourceStepRunID == stepID {
			return snapshot
		}
	}
	return nil
}
