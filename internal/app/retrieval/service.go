package retrieval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"reqflow/internal/domain/model"
	domain "reqflow/internal/domain/workflow"
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

type searchContract struct {
	Spec         domain.SearchSpec
	Raw          json.RawMessage
	Hash         string
	Lexical      model.LexicalConfig
	Vector       model.VectorConfig
	FilterFields []string
	Fusion       model.FusionConfig
}

func compileSearchContract(spec domain.SearchSpec) (searchContract, error) {
	raw, err := json.Marshal(spec)
	if err != nil {
		return searchContract{}, err
	}
	hash, err := domain.HashContract(spec)
	if err != nil {
		return searchContract{}, err
	}
	lexical := make(map[string]float64, len(spec.LexicalFields))
	for _, field := range spec.LexicalFields {
		if strings.TrimSpace(field.Field) == "" || field.Weight <= 0 {
			return searchContract{}, fmt.Errorf("SearchSpec lexical_fields 非法")
		}
		lexical[field.Field] = field.Weight
	}
	if len(lexical) == 0 && len(spec.VectorFields) == 0 {
		return searchContract{}, fmt.Errorf("SearchSpec 至少需要一个搜索字段")
	}
	if spec.ChunkSize <= 0 || spec.ChunkOverlap < 0 || spec.ChunkOverlap >= spec.ChunkSize {
		return searchContract{}, fmt.Errorf("SearchSpec 分块参数非法")
	}
	return searchContract{Spec: spec, Raw: raw, Hash: hash,
		Lexical: model.LexicalConfig{Fields: lexical, Analyzer: "standard"},
		Vector: model.VectorConfig{Fields: append([]string(nil), spec.VectorFields...),
			ChunkSize: spec.ChunkSize, ChunkOverlap: spec.ChunkOverlap,
			ChunkerVersion: "rune_v1", EmbeddingModel: "platform_default"},
		FilterFields: append([]string(nil), spec.FilterFields...),
		Fusion: model.FusionConfig{Method: "rrf", RankConstant: 60,
			LexicalCandidates: spec.LexicalCandidates, VectorCandidates: spec.VectorCandidates}}, nil
}

type BuildInput struct {
	DatasetID         string
	DataContract      domain.DataContract
	SearchSpec        domain.SearchSpec
	SourceSeq         int64
	ProducerNodeRunID string
	ProducerAttempt   int
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
	dataset, dataContractHash, contract, err := s.validateBuildInput(ctx, input)
	if err != nil {
		return nil, err
	}
	if len(contract.Lexical.Fields) > 0 && !s.lexical.Available() {
		return nil, fmt.Errorf("OpenSearch BM25 后端未配置")
	}
	if len(contract.Vector.Fields) > 0 && !s.embedder.Available() {
		return nil, fmt.Errorf("embedding 后端未配置")
	}
	resolvedModel := "none"
	if len(contract.Vector.Fields) > 0 {
		resolvedModel, err = s.resolveEmbeddingModel(ctx, contract.Vector.EmbeddingModel)
		if err != nil {
			return nil, err
		}
	}
	indexRef := retrievalIndexRef(dataset.ID, contract.Hash)
	snapshot, err = s.repo.GetOrCreateRetrievalSnapshotForNode(ctx, &model.RetrievalSnapshot{
		DatasetID: dataset.ID, DataContractHash: dataContractHash, SearchSpec: contract.Raw,
		SearchSpecHash: contract.Hash, EmbeddingModel: resolvedModel, ProducerNodeRunID: input.ProducerNodeRunID,
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
			_ = s.repo.SetRetrievalSnapshotStatusForNode(context.WithoutCancel(ctx), snapshotID,
				input.ProducerNodeRunID, input.ProducerAttempt, model.RetrievalSnapshotFailed, truncateError(err.Error(), 4000))
		}
	}()

	afterSeq := int64(0)
	if previous, previousErr := s.repo.GetLatestActiveRetrievalSnapshot(ctx, dataset.ID, contract.Hash, input.SourceSeq); previousErr == nil {
		lexicalCount, vectorItems := int(previous.SourceSeq), int(previous.SourceSeq)
		var lexicalErr, vectorErr error
		if len(contract.Lexical.Fields) > 0 {
			lexicalCount, lexicalErr = s.lexical.Count(ctx, indexRef, previous.SourceSeq)
		}
		if len(contract.Vector.Fields) > 0 {
			_, vectorItems, vectorErr = s.repo.CountRetrievalChunks(ctx, dataset.ID, contract.Hash, previous.SourceSeq)
		}
		if lexicalErr == nil && vectorErr == nil && lexicalCount == int(previous.SourceSeq) && vectorItems == int(previous.SourceSeq) {
			afterSeq = previous.SourceSeq
		}
	} else if !errors.Is(previousErr, port.ErrRetrievalSnapshotNotFound) {
		return nil, previousErr
	}
	// 即使 Dataset 为空也要先创建物理 BM25 索引，Active Snapshot 的 lexical_ref 必须可用。
	if len(contract.Lexical.Fields) > 0 {
		if err = s.lexical.Build(ctx, port.LexicalBuildRequest{IndexRef: indexRef,
			Analyzer: contract.Lexical.Analyzer, Fields: contract.Lexical.Fields, Filters: contract.FilterFields}); err != nil {
			return nil, err
		}
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
			if len(contract.Lexical.Fields) > 0 {
				documents = append(documents, lexicalDocument(item, fields, contract))
			}
			if len(contract.Vector.Fields) > 0 {
				chunks = append(chunks, buildItemChunks(item, fields, contract, resolvedModel)...)
			}
		}
		texts := make([]string, len(chunks))
		for i := range chunks {
			texts[i] = chunks[i].ChunkText
		}
		if len(chunks) > 0 {
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
		}
		if len(documents) > 0 {
			if err = s.lexical.Build(ctx, port.LexicalBuildRequest{IndexRef: indexRef,
				Analyzer: contract.Lexical.Analyzer, Fields: contract.Lexical.Fields,
				Filters: contract.FilterFields, Documents: documents}); err != nil {
				return nil, err
			}
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
	if err = s.repo.SetRetrievalSnapshotStatusForNode(ctx, snapshot.ID, input.ProducerNodeRunID,
		input.ProducerAttempt, model.RetrievalSnapshotValidating, ""); err != nil {
		return nil, err
	}
	lexicalCount := 0
	if len(contract.Lexical.Fields) > 0 {
		lexicalCount, err = s.lexical.Count(ctx, indexRef, input.SourceSeq)
		if err != nil {
			return nil, err
		}
	}
	vectorCount, vectorItems := 0, 0
	if len(contract.Vector.Fields) > 0 {
		vectorCount, vectorItems, err = s.repo.CountRetrievalChunks(ctx, dataset.ID, contract.Hash, input.SourceSeq)
		if err != nil {
			return nil, err
		}
	}
	if (len(contract.Lexical.Fields) > 0 && lexicalCount != int(input.SourceSeq)) ||
		(len(contract.Vector.Fields) > 0 && vectorItems != int(input.SourceSeq)) {
		return nil, fmt.Errorf("Snapshot 覆盖不完整: source_seq=%d lexical_items=%d vector_items=%d",
			input.SourceSeq, lexicalCount, vectorItems)
	}
	lexicalRef, vectorRef := "", ""
	if len(contract.Lexical.Fields) > 0 {
		lexicalRef = indexRef
	}
	if len(contract.Vector.Fields) > 0 {
		vectorRef = "pgvector:" + dataset.ID + ":" + contract.Hash
	}
	snapshot, err = s.repo.ActivateRetrievalSnapshotForNode(ctx, snapshot.ID, input.ProducerNodeRunID,
		input.ProducerAttempt, lexicalRef, vectorRef, lexicalCount, vectorCount)
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

func (s *Service) validateBuildInput(ctx context.Context, input BuildInput) (*model.Dataset, string, searchContract, error) {
	if strings.TrimSpace(input.ProducerNodeRunID) == "" || input.ProducerAttempt <= 0 {
		return nil, "", searchContract{}, fmt.Errorf("retrieval.build 必须由有效 NodeRun attempt 执行")
	}
	dataset, err := s.repo.GetAppendDataset(ctx, strings.TrimSpace(input.DatasetID))
	if err != nil {
		return nil, "", searchContract{}, fmt.Errorf("Dataset 不存在: %w", err)
	}
	if dataset.Status != model.DatasetStatusActive {
		return nil, "", searchContract{}, fmt.Errorf("Dataset 未处于 active")
	}
	if input.SourceSeq < 0 || input.SourceSeq > dataset.CurrentSeq {
		return nil, "", searchContract{}, fmt.Errorf("source_seq %d 超出 Dataset 当前位点 %d", input.SourceSeq, dataset.CurrentSeq)
	}
	schema, err := s.repo.GetDatasetSchema(ctx, dataset.SchemaID)
	if err != nil {
		return nil, "", searchContract{}, fmt.Errorf("DatasetSchema 不存在: %w", err)
	}
	_, schemaHash, err := domain.CompileDataContract(input.DataContract)
	if err != nil {
		return nil, "", searchContract{}, err
	}
	if schema.SchemaHash != schemaHash || !slices.Equal(dataset.KeyFields, input.DataContract.KeyFields) {
		return nil, "", searchContract{}, fmt.Errorf("Dataset 与内联 DataContract 不一致")
	}
	dataHash, err := domain.HashContract(input.DataContract)
	if err != nil {
		return nil, "", searchContract{}, err
	}
	contract, err := compileSearchContract(input.SearchSpec)
	if err != nil {
		return nil, "", searchContract{}, err
	}
	return dataset, dataHash, contract, nil
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
		return "", fmt.Errorf("SearchSpec 要求 embedding model %q，当前平台配置为 %q", configured, platformModel)
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
	var spec domain.SearchSpec
	if err := json.Unmarshal(snapshot.SearchSpec, &spec); err != nil {
		return nil, fmt.Errorf("RetrievalSnapshot SearchSpec 非法: %w", err)
	}
	contract, err := compileSearchContract(spec)
	if err != nil {
		return nil, err
	}
	if contract.Hash != snapshot.SearchSpecHash {
		return nil, fmt.Errorf("RetrievalSnapshot SearchSpec 哈希不一致")
	}
	strategy, err := normalizeSearchStrategy(request.Strategy, contract)
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
	if err := validateFilters(request.Filters, contract.FilterFields); err != nil {
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
				IndexRef: snapshot.LexicalRef, Query: request.Query, Fields: contract.Lexical.Fields,
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
				DatasetID: snapshot.DatasetID, SearchSpecHash: contract.Hash, QueryEmbedding: vectors[0],
				Filters: request.Filters, SourceSeq: snapshot.SourceSeq, Limit: strategy.RecallLimit,
			})
			return embedErr
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	fused := fuseRRF(lexicalHits, semanticHits, strategy, contract.Fusion.RankConstant)
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
			documents[i] = rerankDocument(hits[i].Fields, contract.Vector.Fields)
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

func normalizeSearchStrategy(strategy model.RetrievalSearchStrategy, contract searchContract) (model.RetrievalSearchStrategy, error) {
	if strategy.Mode == "" {
		strategy.Mode = model.RetrievalModeHybrid
	}
	switch strategy.Mode {
	case model.RetrievalModeLexical:
		if len(contract.Lexical.Fields) == 0 {
			return strategy, fmt.Errorf("当前 Snapshot 未配置 lexical 搜索")
		}
		strategy.LexicalWeight, strategy.SemanticWeight = 1, 0
	case model.RetrievalModeSemantic:
		if len(contract.Vector.Fields) == 0 {
			return strategy, fmt.Errorf("当前 Snapshot 未配置 semantic 搜索")
		}
		strategy.LexicalWeight, strategy.SemanticWeight = 0, 1
	case model.RetrievalModeHybrid:
		if len(contract.Lexical.Fields) == 0 || len(contract.Vector.Fields) == 0 {
			return strategy, fmt.Errorf("当前 Snapshot 不支持 hybrid 搜索")
		}
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
		strategy.RecallLimit = contract.Fusion.LexicalCandidates
		if contract.Fusion.VectorCandidates > strategy.RecallLimit {
			strategy.RecallLimit = contract.Fusion.VectorCandidates
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
			return fmt.Errorf("filter 字段 %s 不在 SearchSpec 白名单", field)
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

func retrievalIndexRef(datasetID, searchSpecHash string) string {
	digest := sha256.Sum256([]byte(datasetID + ":" + searchSpecHash))
	return "retrieval-" + hex.EncodeToString(digest[:12])
}

func lexicalDocument(item model.DatasetItem, fields map[string]any, contract searchContract) port.LexicalDocument {
	indexed := map[string]any{}
	for name := range contract.Lexical.Fields {
		indexed[name] = normalizedSearchValue(fields[name])
	}
	filters := map[string]any{}
	for _, name := range contract.FilterFields {
		filters[name] = fields[name]
	}
	return port.LexicalDocument{DatasetItemID: item.ID, SourceSeq: item.CommitSeq,
		Fields: indexed, Filters: filters}
}

func buildItemChunks(item model.DatasetItem, fields map[string]any, contract searchContract,
	embeddingModel string) []model.RetrievalChunk {
	text := rerankDocumentFromValues(fields, contract.Vector.Fields)
	parts := chunkRunes(text, contract.Vector.ChunkSize, contract.Vector.ChunkOverlap)
	chunks := make([]model.RetrievalChunk, len(parts))
	for i, part := range parts {
		digest := sha256.Sum256([]byte(part))
		metadata, _ := json.Marshal(map[string]any{"chunker_version": contract.Vector.ChunkerVersion})
		chunks[i] = model.RetrievalChunk{DatasetID: item.DatasetID, DatasetItemID: item.ID,
			SearchSpecHash: contract.Hash, ChunkNo: i, ChunkText: part,
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
