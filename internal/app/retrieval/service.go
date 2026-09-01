package retrieval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

const (
	defaultPageSize    = 200
	maxSearchQuerySize = 8000
	maxRecallLimit     = 1000
	maxTopK            = 200
)

type Service struct {
	repo           port.RetrievalRepo
	lexical        port.LexicalBackend
	embedder       port.Embedder
	reranker       port.Reranker
	embeddingModel string
	resolver       port.PlatformConfigResolver
	pageSize       int
}

type Options struct {
	EmbeddingModel string
	PageSize       int
	ConfigResolver port.PlatformConfigResolver
}

func NewService(repo port.RetrievalRepo, lexical port.LexicalBackend, embedder port.Embedder,
	reranker port.Reranker, options Options) (*Service, error) {
	if repo == nil || lexical == nil || embedder == nil || reranker == nil {
		return nil, fmt.Errorf("retrieval service 依赖不完整")
	}
	options.EmbeddingModel = strings.TrimSpace(options.EmbeddingModel)
	if options.PageSize <= 0 || options.PageSize > 2000 {
		options.PageSize = defaultPageSize
	}
	return &Service{repo: repo, lexical: lexical, embedder: embedder, reranker: reranker,
		embeddingModel: options.EmbeddingModel, resolver: options.ConfigResolver,
		pageSize: options.PageSize}, nil
}

type CreateProfileInput struct {
	WorkspaceID     string
	Name            string
	DatasetSchemaID string
	Lexical         model.LexicalConfig
	Vector          model.VectorConfig
	FilterFields    []string
	Fusion          model.FusionConfig
}

func (s *Service) CreateProfile(ctx context.Context, input CreateProfileInput) (*model.RetrievalProfile, error) {
	if strings.TrimSpace(input.WorkspaceID) == "" {
		input.WorkspaceID = "default"
	}
	schema, err := s.repo.GetDatasetSchema(ctx, strings.TrimSpace(input.DatasetSchemaID))
	if err != nil {
		return nil, fmt.Errorf("目标 DatasetSchema 不存在: %w", err)
	}
	if schema.WorkspaceID != input.WorkspaceID {
		return nil, fmt.Errorf("RetrievalProfile 与 DatasetSchema 必须属于同一 workspace")
	}
	profile := model.RetrievalProfile{WorkspaceID: input.WorkspaceID, Name: input.Name,
		DatasetSchemaID: input.DatasetSchemaID, Lexical: input.Lexical, Vector: input.Vector,
		FilterFields: input.FilterFields, Fusion: input.Fusion}
	profile, hash, err := logic.NormalizeRetrievalProfile(profile, *schema)
	if err != nil {
		return nil, err
	}
	profile.ProfileHash = hash
	if err := s.repo.CreateRetrievalProfile(ctx, &profile); err != nil {
		return nil, err
	}
	return &profile, nil
}

func (s *Service) GetProfile(ctx context.Context, id string) (*model.RetrievalProfile, error) {
	return s.repo.GetRetrievalProfile(ctx, strings.TrimSpace(id))
}

func (s *Service) CloneProfile(ctx context.Context, id, name string) (*model.RetrievalProfile, error) {
	source, err := s.GetProfile(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.CreateProfile(ctx, CreateProfileInput{WorkspaceID: source.WorkspaceID, Name: name,
		DatasetSchemaID: source.DatasetSchemaID, Lexical: source.Lexical, Vector: source.Vector,
		FilterFields: append([]string(nil), source.FilterFields...), Fusion: source.Fusion})
}

type BuildInput struct {
	DatasetID          string
	RetrievalProfileID string
	SourceSeq          int64
	TaskID             string
	StepRunID          string
	ProducerAttempt    int
}

type BuildProgress struct {
	SnapshotID   string
	ProcessedSeq int64
	SourceSeq    int64
	LexicalCount int
	VectorCount  int
	Status       string
}

func (s *Service) BuildSnapshot(ctx context.Context, input BuildInput,
	report func(BuildProgress) error) (snapshot *model.RetrievalSnapshot, err error) {
	dataset, profile, err := s.validateBuildInput(ctx, input)
	if err != nil {
		return nil, err
	}
	if !s.lexical.Available() {
		return nil, fmt.Errorf("OpenSearch BM25 后端未配置")
	}
	if !s.embedder.Available() {
		return nil, fmt.Errorf("embedding 后端未配置")
	}
	resolvedModel, err := s.resolveEmbeddingModel(ctx, profile.Vector.EmbeddingModel)
	if err != nil {
		return nil, err
	}
	indexRef := retrievalIndexRef(dataset.ID, profile.ID)
	snapshot, err = s.repo.GetOrCreateRetrievalSnapshotForStep(ctx, &model.RetrievalSnapshot{
		DatasetID: dataset.ID, RetrievalProfileID: profile.ID, SourceStepRunID: input.StepRunID,
		SourceSeq: input.SourceSeq, Status: model.RetrievalSnapshotBuilding,
	}, input.ProducerAttempt)
	if err != nil {
		return nil, err
	}
	if snapshot.Status == model.RetrievalSnapshotActive {
		return snapshot, nil
	}
	snapshotID := snapshot.ID
	defer func() {
		if err != nil {
			_ = s.repo.SetRetrievalSnapshotStatusForStep(context.WithoutCancel(ctx), snapshotID,
				input.StepRunID, input.ProducerAttempt, model.RetrievalSnapshotFailed, truncateError(err.Error(), 4000))
		}
	}()

	afterSeq := int64(0)
	if previous, previousErr := s.repo.GetLatestActiveRetrievalSnapshot(ctx, dataset.ID, profile.ID, input.SourceSeq); previousErr == nil {
		lexicalCount, lexicalErr := s.lexical.Count(ctx, indexRef, previous.SourceSeq)
		_, vectorItems, vectorErr := s.repo.CountRetrievalChunks(ctx, dataset.ID, profile.ID, previous.SourceSeq)
		if lexicalErr == nil && vectorErr == nil && lexicalCount == int(previous.SourceSeq) && vectorItems == int(previous.SourceSeq) {
			afterSeq = previous.SourceSeq
		}
	} else if !errors.Is(previousErr, port.ErrRetrievalSnapshotNotFound) {
		return nil, previousErr
	}
	// 即使 Dataset 为空也要先创建物理 BM25 索引，Active Snapshot 的 lexical_ref 必须可用。
	if err = s.lexical.Build(ctx, port.LexicalBuildRequest{IndexRef: indexRef,
		Analyzer: profile.Lexical.Analyzer, Fields: profile.Lexical.Fields, Filters: profile.FilterFields}); err != nil {
		return nil, err
	}
	processed := afterSeq
	for processed < input.SourceSeq {
		items, listErr := s.repo.ListDatasetItemsAfter(ctx, dataset.ID, processed, input.SourceSeq, s.pageSize)
		if listErr != nil {
			return nil, listErr
		}
		if len(items) == 0 || items[0].CommitSeq != processed+1 {
			return nil, fmt.Errorf("Dataset commit_seq 在 %d 之后不连续", processed)
		}
		documents := make([]port.LexicalDocument, 0, len(items))
		chunks := make([]model.RetrievalChunk, 0, len(items))
		for _, item := range items {
			fields, parseErr := decodeItemFields(item.Fields)
			if parseErr != nil {
				return nil, fmt.Errorf("DatasetItem %s fields 非法: %w", item.ID, parseErr)
			}
			documents = append(documents, lexicalDocument(item, fields, *profile))
			chunks = append(chunks, buildItemChunks(item, fields, *profile, resolvedModel)...)
		}
		texts := make([]string, len(chunks))
		for i := range chunks {
			texts[i] = chunks[i].ChunkText
		}
		embeddings, embedErr := s.embedder.Generate(ctx, texts)
		if embedErr != nil {
			return nil, embedErr
		}
		if len(embeddings) != len(chunks) {
			return nil, fmt.Errorf("embedding 数量不匹配: want %d got %d", len(chunks), len(embeddings))
		}
		for i := range chunks {
			chunks[i].Embedding = embeddings[i]
		}
		if err = s.lexical.Build(ctx, port.LexicalBuildRequest{IndexRef: indexRef,
			Analyzer: profile.Lexical.Analyzer, Fields: profile.Lexical.Fields,
			Filters: profile.FilterFields, Documents: documents}); err != nil {
			return nil, err
		}
		if err = s.repo.UpsertRetrievalChunks(ctx, chunks); err != nil {
			return nil, err
		}
		processed = items[len(items)-1].CommitSeq
		if report != nil {
			if err = report(BuildProgress{SnapshotID: snapshot.ID, ProcessedSeq: processed,
				SourceSeq: input.SourceSeq, LexicalCount: len(documents), VectorCount: len(chunks),
				Status: model.RetrievalSnapshotBuilding}); err != nil {
				return nil, err
			}
		}
	}
	if err = s.repo.SetRetrievalSnapshotStatusForStep(ctx, snapshot.ID, input.StepRunID,
		input.ProducerAttempt, model.RetrievalSnapshotValidating, ""); err != nil {
		return nil, err
	}
	lexicalCount, err := s.lexical.Count(ctx, indexRef, input.SourceSeq)
	if err != nil {
		return nil, err
	}
	vectorCount, vectorItems, err := s.repo.CountRetrievalChunks(ctx, dataset.ID, profile.ID, input.SourceSeq)
	if err != nil {
		return nil, err
	}
	if lexicalCount != int(input.SourceSeq) || vectorItems != int(input.SourceSeq) {
		return nil, fmt.Errorf("Snapshot 覆盖不完整: source_seq=%d lexical_items=%d vector_items=%d",
			input.SourceSeq, lexicalCount, vectorItems)
	}
	vectorRef := "pgvector:" + dataset.ID + ":" + profile.ID
	snapshot, err = s.repo.ActivateRetrievalSnapshotForStep(ctx, snapshot.ID, input.StepRunID,
		input.ProducerAttempt, indexRef, vectorRef, lexicalCount, vectorCount)
	if err != nil {
		return nil, err
	}
	if report != nil {
		err = report(BuildProgress{SnapshotID: snapshot.ID, ProcessedSeq: input.SourceSeq,
			SourceSeq: input.SourceSeq, LexicalCount: lexicalCount, VectorCount: vectorCount,
			Status: model.RetrievalSnapshotActive})
		if err != nil {
			return nil, err
		}
	}
	return snapshot, nil
}

func (s *Service) validateBuildInput(ctx context.Context, input BuildInput) (*model.Dataset, *model.RetrievalProfile, error) {
	if strings.TrimSpace(input.StepRunID) == "" || input.ProducerAttempt <= 0 {
		return nil, nil, fmt.Errorf("retrieval.build 必须由有效 StepRun attempt 执行")
	}
	dataset, err := s.repo.GetAppendDataset(ctx, strings.TrimSpace(input.DatasetID))
	if err != nil {
		return nil, nil, fmt.Errorf("Dataset 不存在: %w", err)
	}
	if dataset.Status != model.DatasetStatusActive {
		return nil, nil, fmt.Errorf("Dataset 未处于 active")
	}
	if input.SourceSeq < 0 || input.SourceSeq > dataset.CurrentSeq {
		return nil, nil, fmt.Errorf("source_seq %d 超出 Dataset 当前位点 %d", input.SourceSeq, dataset.CurrentSeq)
	}
	profile, err := s.repo.GetRetrievalProfile(ctx, strings.TrimSpace(input.RetrievalProfileID))
	if err != nil {
		return nil, nil, fmt.Errorf("RetrievalProfile 不存在: %w", err)
	}
	if profile.WorkspaceID != dataset.WorkspaceID || profile.DatasetSchemaID != dataset.SchemaID {
		return nil, nil, fmt.Errorf("RetrievalProfile 与 Dataset 的 workspace/schema 不匹配")
	}
	return dataset, profile, nil
}

func (s *Service) resolveEmbeddingModel(ctx context.Context, configured string) (string, error) {
	platformModel := strings.TrimSpace(s.embeddingModel)
	if s.resolver != nil {
		config, err := s.resolver.ResolveEmbedding(ctx)
		if err != nil {
			return "", fmt.Errorf("读取当前 Embedding 配置: %w", err)
		}
		platformModel = strings.TrimSpace(config.Model)
	}
	configured = strings.TrimSpace(configured)
	if configured == "" || configured == "platform_default" {
		if platformModel == "" {
			return "", fmt.Errorf("平台默认 embedding model 未配置")
		}
		return platformModel, nil
	}
	if platformModel == "" || configured != platformModel {
		return "", fmt.Errorf("RetrievalProfile 要求 embedding model %q，当前平台配置为 %q", configured, platformModel)
	}
	return configured, nil
}

type SearchRequest struct {
	RetrievalSnapshotID string
	Query               string
	Filters             map[string][]string
	Strategy            model.RetrievalSearchStrategy
}

type SearchResponse struct {
	RetrievalSnapshotID string                        `json:"retrieval_snapshot_id"`
	Strategy            model.RetrievalSearchStrategy `json:"strategy"`
	Hits                []model.RetrievalHit          `json:"hits"`
	TookMS              int64                         `json:"took_ms"`
}

func (s *Service) Search(ctx context.Context, request SearchRequest) (*SearchResponse, error) {
	started := time.Now()
	request.Query = strings.TrimSpace(request.Query)
	if request.Query == "" || len(request.Query) > maxSearchQuerySize {
		return nil, fmt.Errorf("query 必须为 1..%d 字节", maxSearchQuerySize)
	}
	snapshot, err := s.repo.GetRetrievalSnapshot(ctx, strings.TrimSpace(request.RetrievalSnapshotID))
	if err != nil {
		return nil, fmt.Errorf("RetrievalSnapshot 不存在: %w", err)
	}
	if snapshot.Status != model.RetrievalSnapshotActive {
		return nil, fmt.Errorf("RetrievalSnapshot %s 未激活", snapshot.ID)
	}
	profile, err := s.repo.GetRetrievalProfile(ctx, snapshot.RetrievalProfileID)
	if err != nil {
		return nil, err
	}
	strategy, err := normalizeSearchStrategy(request.Strategy, *profile)
	if err != nil {
		return nil, err
	}
	// reranker 未配置时降级为纯融合结果而不是报错——rerank 是默认产品路径，
	// 缺配置不应让搜索直接不可用；降级后策略回显 RerankEnabled=false 可被调用方感知。
	if strategy.RerankEnabled && !s.reranker.Available() {
		strategy.RerankEnabled = false
	}
	if !strategy.RerankEnabled {
		// 原始 RRF 融合分未校准（量级 ≈0.01x），0..1 阈值对它只会全量误杀；
		// 阈值只对 rerank 后的校准分数生效。
		strategy.ScoreThreshold = 0
	}
	if err := validateFilters(request.Filters, profile.FilterFields); err != nil {
		return nil, err
	}

	var lexicalHits, semanticHits []port.RankedHit
	group, groupCtx := errgroup.WithContext(ctx)
	if strategy.LexicalWeight > 0 {
		group.Go(func() error {
			if !s.lexical.Available() {
				return fmt.Errorf("OpenSearch BM25 后端未配置")
			}
			var searchErr error
			lexicalHits, searchErr = s.lexical.Search(groupCtx, port.LexicalSearchRequest{
				IndexRef: snapshot.LexicalRef, Query: request.Query, Fields: profile.Lexical.Fields,
				Filters: request.Filters, SourceSeq: snapshot.SourceSeq, Limit: strategy.RecallLimit,
			})
			return searchErr
		})
	}
	if strategy.SemanticWeight > 0 {
		group.Go(func() error {
			if !s.embedder.Available() {
				return fmt.Errorf("embedding 后端未配置")
			}
			vectors, embedErr := s.embedder.Generate(groupCtx, []string{request.Query})
			if embedErr != nil {
				return embedErr
			}
			if len(vectors) != 1 {
				return fmt.Errorf("query embedding 响应数量异常")
			}
			semanticHits, embedErr = s.repo.SearchRetrievalChunks(groupCtx, port.VectorSearchRequest{
				DatasetID: snapshot.DatasetID, RetrievalProfileID: profile.ID, QueryEmbedding: vectors[0],
				Filters: request.Filters, SourceSeq: snapshot.SourceSeq, Limit: strategy.RecallLimit,
			})
			return embedErr
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	fused := fuseRRF(lexicalHits, semanticHits, strategy, profile.Fusion.RankConstant)
	if len(fused) > strategy.RecallLimit {
		fused = fused[:strategy.RecallLimit]
	}
	ids := make([]string, len(fused))
	for i := range fused {
		ids[i] = fused[i].DatasetItemID
	}
	items, err := s.repo.GetDatasetItemsByIDs(ctx, snapshot.DatasetID, snapshot.SourceSeq, ids)
	if err != nil {
		return nil, err
	}
	itemsByID := make(map[string]model.DatasetItem, len(items))
	for _, item := range items {
		itemsByID[item.ID] = item
	}
	hits := make([]model.RetrievalHit, 0, len(fused))
	for _, candidate := range fused {
		item, ok := itemsByID[candidate.DatasetItemID]
		if !ok {
			continue
		}
		hits = append(hits, model.RetrievalHit{DatasetItemID: item.ID, ChunkID: candidate.ChunkID,
			ChunkText: candidate.ChunkText, Fields: json.RawMessage(item.Fields),
			Provenance: json.RawMessage(item.Provenance), CommitSeq: item.CommitSeq,
			Score: candidate.FusionScore, FusionScore: candidate.FusionScore, Ranks: candidate.Ranks})
	}
	if strategy.RerankEnabled && len(hits) > 0 {
		documents := make([]string, len(hits))
		for i := range hits {
			documents[i] = rerankDocument(hits[i].Fields, profile.Vector.Fields)
		}
		results, rerankErr := s.reranker.Rerank(ctx, request.Query, documents, strategy.RerankTopN)
		if rerankErr != nil {
			return nil, rerankErr
		}
		reranked := make([]model.RetrievalHit, 0, len(results))
		for _, result := range results {
			if result.Index < 0 || result.Index >= len(hits) {
				return nil, fmt.Errorf("reranker 返回非法 index %d", result.Index)
			}
			hit := hits[result.Index]
			score := result.Score
			hit.RerankScore, hit.Score = &score, score
			reranked = append(reranked, hit)
		}
		sort.SliceStable(reranked, func(i, j int) bool { return reranked[i].Score > reranked[j].Score })
		// score_threshold 语义是"过滤最终展示分数"：rerank 后分数已被覆盖为 rerank 分，
		// 必须再过滤一次，否则界面分值低于阈值的结果仍会返回，阈值看起来不生效。
		if strategy.ScoreThreshold > 0 {
			kept := reranked[:0]
			for _, hit := range reranked {
				if hit.Score+1e-12 >= strategy.ScoreThreshold {
					kept = append(kept, hit)
				}
			}
			reranked = kept
		}
		if len(reranked) > strategy.RerankTopN {
			reranked = reranked[:strategy.RerankTopN]
		}
		hits = reranked
	} else if len(hits) > strategy.TopK {
		hits = hits[:strategy.TopK]
	}
	return &SearchResponse{RetrievalSnapshotID: snapshot.ID, Strategy: strategy, Hits: hits,
		TookMS: time.Since(started).Milliseconds()}, nil
}

type fusedCandidate struct {
	DatasetItemID string
	ChunkID       string
	ChunkText     string
	FusionScore   float64
	Ranks         map[string]model.RetrievalRankSource
}

func fuseRRF(lexical, semantic []port.RankedHit, strategy model.RetrievalSearchStrategy, rankConstant int) []fusedCandidate {
	byID := map[string]*fusedCandidate{}
	add := func(source string, weight float64, hits []port.RankedHit) {
		for i, hit := range hits {
			rank := hit.Rank
			if rank <= 0 {
				rank = i + 1
			}
			candidate := byID[hit.DatasetItemID]
			if candidate == nil {
				candidate = &fusedCandidate{DatasetItemID: hit.DatasetItemID,
					Ranks: map[string]model.RetrievalRankSource{}}
				byID[hit.DatasetItemID] = candidate
			}
			candidate.FusionScore += weight / float64(rankConstant+rank)
			candidate.Ranks[source] = model.RetrievalRankSource{Rank: rank, Score: hit.Score}
			if source == "semantic" || candidate.ChunkID == "" {
				candidate.ChunkID, candidate.ChunkText = hit.ChunkID, hit.Text
			}
		}
	}
	add("lexical", strategy.LexicalWeight, lexical)
	add("semantic", strategy.SemanticWeight, semantic)
	// RRF 只看名次不看分差，融合分保留原始量纲（量级 ≈ 1/(k+rank)，约 0.006..0.017），
	// 刻意不做归一化：它衡量的是双路共识，不是校准的相关性分。除以"理论满分"式的
	// 归一化会把分数压进 0.5..1.0 的窄带（两路中等靠前 ≈0.67，单路第一 ≈0.5），
	// 阈值随之失效。用户可见的最终分数由 rerank 决定；阈值过滤也只作用于最终分数。
	out := make([]fusedCandidate, 0, len(byID))
	for _, candidate := range byID {
		out = append(out, *candidate)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].FusionScore == out[j].FusionScore {
			return out[i].DatasetItemID < out[j].DatasetItemID
		}
		return out[i].FusionScore > out[j].FusionScore
	})
	return out
}

func normalizeSearchStrategy(strategy model.RetrievalSearchStrategy, profile model.RetrievalProfile) (model.RetrievalSearchStrategy, error) {
	if strategy.Mode == "" {
		strategy.Mode = model.RetrievalModeHybrid
	}
	switch strategy.Mode {
	case model.RetrievalModeLexical:
		strategy.LexicalWeight, strategy.SemanticWeight = 1, 0
	case model.RetrievalModeSemantic:
		strategy.LexicalWeight, strategy.SemanticWeight = 0, 1
	case model.RetrievalModeHybrid:
		if strategy.LexicalWeight == 0 && strategy.SemanticWeight == 0 {
			strategy.LexicalWeight, strategy.SemanticWeight = 0.5, 0.5
		}
	default:
		return strategy, fmt.Errorf("mode 必须是 lexical、semantic 或 hybrid")
	}
	if strategy.LexicalWeight < 0 || strategy.SemanticWeight < 0 ||
		strategy.LexicalWeight > 100 || strategy.SemanticWeight > 100 ||
		strategy.LexicalWeight+strategy.SemanticWeight <= 0 {
		return strategy, fmt.Errorf("lexical_weight/semantic_weight 必须在 0..100 且至少一个大于 0")
	}
	totalWeight := strategy.LexicalWeight + strategy.SemanticWeight
	strategy.LexicalWeight /= totalWeight
	strategy.SemanticWeight /= totalWeight
	if strategy.ScoreThreshold < 0 || strategy.ScoreThreshold > 1 {
		return strategy, fmt.Errorf("score_threshold 必须在 0..1 之间")
	}
	if strategy.RecallLimit == 0 {
		strategy.RecallLimit = profile.Fusion.LexicalCandidates
		if profile.Fusion.VectorCandidates > strategy.RecallLimit {
			strategy.RecallLimit = profile.Fusion.VectorCandidates
		}
	}
	if strategy.RecallLimit < 1 || strategy.RecallLimit > maxRecallLimit {
		return strategy, fmt.Errorf("recall_limit 必须在 1..%d 之间", maxRecallLimit)
	}
	if strategy.TopK == 0 {
		strategy.TopK = 20
	}
	if strategy.TopK < 1 || strategy.TopK > maxTopK || strategy.TopK > strategy.RecallLimit {
		return strategy, fmt.Errorf("top_k 必须在 1..%d 且不能大于 recall_limit", maxTopK)
	}
	if strategy.RerankEnabled {
		if strategy.RerankTopN == 0 {
			strategy.RerankTopN = strategy.TopK
		}
		if strategy.RerankTopN < 1 || strategy.RerankTopN > maxTopK || strategy.RerankTopN > strategy.RecallLimit {
			return strategy, fmt.Errorf("rerank_top_n 必须在 1..%d 且不能大于 recall_limit", maxTopK)
		}
	} else {
		strategy.RerankTopN = 0
	}
	return strategy, nil
}

func validateFilters(filters map[string][]string, allowed []string) error {
	whitelist := make(map[string]bool, len(allowed))
	for _, field := range allowed {
		whitelist[field] = true
	}
	for field, values := range filters {
		if !whitelist[field] {
			return fmt.Errorf("filter 字段 %s 不在 RetrievalProfile 白名单", field)
		}
		if len(values) == 0 || len(values) > 100 {
			return fmt.Errorf("filter 字段 %s 必须包含 1..100 个值", field)
		}
		for _, value := range values {
			if strings.TrimSpace(value) == "" || len(value) > 2048 {
				return fmt.Errorf("filter 字段 %s 包含空值或超长值", field)
			}
		}
	}
	return nil
}

func retrievalIndexRef(datasetID, profileID string) string {
	digest := sha256.Sum256([]byte(datasetID + ":" + profileID))
	return "retrieval-" + hex.EncodeToString(digest[:12])
}

func lexicalDocument(item model.DatasetItem, fields map[string]any, profile model.RetrievalProfile) port.LexicalDocument {
	indexed := map[string]any{}
	for name := range profile.Lexical.Fields {
		indexed[name] = normalizedSearchValue(fields[name])
	}
	filters := map[string]any{}
	for _, name := range profile.FilterFields {
		filters[name] = fields[name]
	}
	return port.LexicalDocument{DatasetItemID: item.ID, SourceSeq: item.CommitSeq,
		Fields: indexed, Filters: filters}
}

func buildItemChunks(item model.DatasetItem, fields map[string]any, profile model.RetrievalProfile,
	embeddingModel string) []model.RetrievalChunk {
	text := rerankDocumentFromValues(fields, profile.Vector.Fields)
	parts := chunkRunes(text, profile.Vector.ChunkSize, profile.Vector.ChunkOverlap)
	chunks := make([]model.RetrievalChunk, len(parts))
	for i, part := range parts {
		digest := sha256.Sum256([]byte(part))
		metadata, _ := json.Marshal(map[string]any{"chunker_version": profile.Vector.ChunkerVersion})
		chunks[i] = model.RetrievalChunk{DatasetID: item.DatasetID, DatasetItemID: item.ID,
			RetrievalProfileID: profile.ID, ChunkNo: i, ChunkText: part,
			ChunkHash: hex.EncodeToString(digest[:]), SourceSeq: item.CommitSeq,
			EmbeddingModel: embeddingModel, Metadata: metadata}
	}
	return chunks
}

func chunkRunes(text string, size, overlap int) []string {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) == 0 {
		return []string{"(empty)"}
	}
	step := size - overlap
	parts := make([]string, 0, (len(runes)+step-1)/step)
	for start := 0; start < len(runes); start += step {
		end := start + size
		if end > len(runes) {
			end = len(runes)
		}
		parts = append(parts, string(runes[start:end]))
		if end == len(runes) {
			break
		}
	}
	return parts
}

func rerankDocument(raw json.RawMessage, fieldNames []string) string {
	fields, _ := decodeItemFields(string(raw))
	return rerankDocumentFromValues(fields, fieldNames)
}

func rerankDocumentFromValues(fields map[string]any, fieldNames []string) string {
	var lines []string
	for _, name := range fieldNames {
		value := strings.TrimSpace(normalizedSearchValue(fields[name]))
		if value != "" {
			lines = append(lines, name+": "+value)
		}
	}
	if len(lines) == 0 {
		return "(empty)"
	}
	return strings.Join(lines, "\n")
}

func normalizedSearchValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, " ")
	case nil:
		return ""
	default:
		return fmt.Sprint(typed)
	}
}

func decodeItemFields(raw string) (map[string]any, error) {
	fields := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		return nil, err
	}
	return fields, nil
}

func truncateError(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
