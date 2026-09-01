package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

var (
	ErrNoQueryDatasetIncrement   = errors.New("query dataset has no new source increment")
	ErrStaleQueryDatasetBoundary = errors.New("query dataset source boundary is stale")
)

const queryDatasetReadPageSize = 1000

// QueryDerivationConfig 是固化在 WorkflowRevision 快照中的确定性 Base → Query 映射合同。
// 字段都引用 Base Dataset 的顶层字段；目标 Query Dataset 使用平台统一字段合同。
type QueryDerivationConfig struct {
	PipelineKey        string            `json:"pipeline_key"`
	SemanticUnitsField string            `json:"semantic_units_field,omitempty"`
	UnitKeyField       string            `json:"unit_key_field,omitempty"`
	TitleField         string            `json:"title_field"`
	DefinitionFields   []string          `json:"definition_fields"`
	AliasFields        []string          `json:"alias_fields,omitempty"`
	KeywordFields      []string          `json:"keyword_fields,omitempty"`
	FacetFields        map[string]string `json:"facet_fields,omitempty"`
}

func DecodeQueryDerivationConfig(raw json.RawMessage) (QueryDerivationConfig, error) {
	var config QueryDerivationConfig
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return config, fmt.Errorf("data.query_derive config 非法: %w", err)
	}
	if err := validateQueryDerivationConfig(config); err != nil {
		return config, err
	}
	return config, nil
}

func validateQueryDerivationConfig(config QueryDerivationConfig) error {
	if config.PipelineKey != strings.TrimSpace(config.PipelineKey) || !logic.IsValidIdentifier(config.PipelineKey) {
		return fmt.Errorf("pipeline_key 必须是 snake_case 标识符")
	}
	if err := validateQuerySourceField("title_field", config.TitleField, true); err != nil {
		return err
	}
	if len(config.DefinitionFields) == 0 {
		return fmt.Errorf("definition_fields 至少声明一个字段")
	}
	for _, entry := range []struct {
		name   string
		fields []string
	}{
		{name: "definition_fields", fields: config.DefinitionFields},
		{name: "alias_fields", fields: config.AliasFields},
		{name: "keyword_fields", fields: config.KeywordFields},
	} {
		seen := map[string]bool{}
		for _, field := range entry.fields {
			if err := validateQuerySourceField(entry.name, field, true); err != nil {
				return err
			}
			if seen[field] {
				return fmt.Errorf("%s 重复声明字段 %s", entry.name, field)
			}
			seen[field] = true
		}
	}
	if err := validateQuerySourceField("semantic_units_field", config.SemanticUnitsField, false); err != nil {
		return err
	}
	if err := validateQuerySourceField("unit_key_field", config.UnitKeyField, false); err != nil {
		return err
	}
	if config.UnitKeyField != "" && config.SemanticUnitsField == "" {
		return fmt.Errorf("unit_key_field 只能与 semantic_units_field 一起使用")
	}
	for facet, sourceField := range config.FacetFields {
		if !logic.IsValidIdentifier(facet) {
			return fmt.Errorf("facet_fields 的目标键 %s 非法", facet)
		}
		if err := validateQuerySourceField("facet_fields", sourceField, true); err != nil {
			return err
		}
	}
	return nil
}

func validateQuerySourceField(name, field string, required bool) error {
	if field != strings.TrimSpace(field) {
		return fmt.Errorf("%s 字段名不能包含首尾空白", name)
	}
	if field == "" && !required {
		return nil
	}
	if !logic.IsValidIdentifier(field) {
		return fmt.Errorf("%s 包含非法字段名 %q", name, field)
	}
	return nil
}

type QueryDatasetService struct {
	repo     port.QueryDatasetPipelineRepo
	datasets *DatasetService
}

func NewQueryDatasetService(repo port.QueryDatasetPipelineRepo, datasets *DatasetService) (*QueryDatasetService, error) {
	if repo == nil {
		return nil, fmt.Errorf("query dataset: repo is required")
	}
	if datasets == nil {
		return nil, fmt.Errorf("query dataset: dataset service is required")
	}
	return &QueryDatasetService{repo: repo, datasets: datasets}, nil
}

func (s *QueryDatasetService) GetCursor(ctx context.Context, pipelineKey, sourceDatasetID,
	targetDatasetID string) (*model.PipelineCursor, error) {
	if !logic.IsValidIdentifier(strings.TrimSpace(pipelineKey)) || strings.TrimSpace(sourceDatasetID) == "" ||
		strings.TrimSpace(targetDatasetID) == "" {
		return nil, fmt.Errorf("pipeline_key、source_dataset_id、target_dataset_id 必须完整且合法")
	}
	return s.repo.GetPipelineCursor(ctx, strings.TrimSpace(pipelineKey),
		strings.TrimSpace(sourceDatasetID), strings.TrimSpace(targetDatasetID))
}

type DeriveQueryDatasetInput struct {
	SourceDatasetID       string
	SourceThroughSeq      int64
	TargetDatasetID       string
	Config                QueryDerivationConfig
	ProducerWorkflowRunID string
	ProducerNodeRunID     string
	ProducerAttempt       int
}

type QueryDatasetDerivation struct {
	Batch           *model.DatasetBatch
	Cursor          *model.PipelineCursor
	SourceItemCount int
	QueryItemCount  int
}

func (s *QueryDatasetService) Derive(ctx context.Context, in DeriveQueryDatasetInput) (*QueryDatasetDerivation, error) {
	if err := validateQueryDerivationConfig(in.Config); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.SourceDatasetID) == "" || strings.TrimSpace(in.TargetDatasetID) == "" {
		return nil, fmt.Errorf("source_dataset_id 和 target_dataset_id 不能为空")
	}
	if in.SourceDatasetID == in.TargetDatasetID {
		return nil, fmt.Errorf("Base Dataset 与 Query Dataset 不能相同")
	}
	if in.SourceThroughSeq <= 0 {
		return nil, fmt.Errorf("source through_seq 必须大于 0")
	}
	if strings.TrimSpace(in.ProducerWorkflowRunID) == "" || strings.TrimSpace(in.ProducerNodeRunID) == "" ||
		in.ProducerAttempt <= 0 {
		return nil, fmt.Errorf("派生写入必须绑定有效 NodeRun 和 attempt")
	}

	source, err := s.repo.GetAppendDataset(ctx, in.SourceDatasetID)
	if err != nil {
		return nil, fmt.Errorf("读取 Base Dataset: %w", err)
	}
	target, err := s.repo.GetAppendDataset(ctx, in.TargetDatasetID)
	if err != nil {
		return nil, fmt.Errorf("读取 Query Dataset: %w", err)
	}
	if source.Purpose != model.DatasetPurposeBase {
		return nil, fmt.Errorf("源 Dataset %s purpose 必须是 base", source.ID)
	}
	if target.Purpose != model.DatasetPurposeQuery {
		return nil, fmt.Errorf("目标 Dataset %s purpose 必须是 query", target.ID)
	}
	if source.WorkspaceID != target.WorkspaceID {
		return nil, fmt.Errorf("Base 与 Query Dataset 必须属于同一 workspace")
	}
	if source.Status != model.DatasetStatusActive || target.Status != model.DatasetStatusActive {
		return nil, fmt.Errorf("Base 与 Query Dataset 都必须处于 active 状态")
	}
	if in.SourceThroughSeq > source.CurrentSeq {
		return nil, fmt.Errorf("source through_seq %d 超过 Dataset 当前位点 %d", in.SourceThroughSeq, source.CurrentSeq)
	}
	targetSchema, err := s.repo.GetDatasetSchema(ctx, target.SchemaID)
	if err != nil {
		return nil, fmt.Errorf("读取 Query Dataset Schema: %w", err)
	}
	if err := validateQueryDatasetContract(targetSchema.JSONSchema, target.KeyFields, in.Config); err != nil {
		return nil, err
	}

	cursor, err := s.repo.GetOrCreatePipelineCursor(ctx, in.Config.PipelineKey, source.ID, target.ID)
	if err != nil {
		return nil, fmt.Errorf("读取 PipelineCursor: %w", err)
	}
	if cursor.ProcessedThroughSeq >= in.SourceThroughSeq {
		// Batch 已提交但 Worker 在保存 progress/完成 Step 前失去 lease 时，同一 Task
		// 的新 attempt 必须复用原 Batch；其他无增量或旧边界任务不创建 staging Batch。
		if cursor.LastSuccessRunID == strings.TrimSpace(in.ProducerWorkflowRunID) {
			batch, batchErr := s.datasets.GetOrCreateBatch(ctx, CreateBatchInput{DatasetID: target.ID,
				ProducerWorkflowRunID: strings.TrimSpace(in.ProducerWorkflowRunID), ProducerNodeRunID: in.ProducerNodeRunID}, in.ProducerAttempt)
			if batchErr != nil {
				return nil, batchErr
			}
			if batch.Status != model.DatasetBatchCommitted {
				return nil, fmt.Errorf("Cursor 已推进但同 Task Query Batch 未提交")
			}
			return &QueryDatasetDerivation{Batch: batch, Cursor: cursor, QueryItemCount: batch.ItemCount}, nil
		}
		if cursor.ProcessedThroughSeq > in.SourceThroughSeq {
			return nil, fmt.Errorf("%w: Cursor=%d TaskBoundary=%d", ErrStaleQueryDatasetBoundary,
				cursor.ProcessedThroughSeq, in.SourceThroughSeq)
		}
		return nil, fmt.Errorf("%w: through_seq=%d", ErrNoQueryDatasetIncrement, in.SourceThroughSeq)
	}
	batch, err := s.datasets.GetOrCreateBatch(ctx, CreateBatchInput{DatasetID: target.ID,
		ProducerWorkflowRunID: strings.TrimSpace(in.ProducerWorkflowRunID), ProducerNodeRunID: in.ProducerNodeRunID}, in.ProducerAttempt)
	if err != nil {
		return nil, err
	}
	if batch.Status == model.DatasetBatchCommitted {
		return nil, fmt.Errorf("已提交 Query Batch 与 Cursor 位点不一致")
	}

	sourceItems, err := s.readSourceIncrement(ctx, source.ID, cursor.ProcessedThroughSeq, in.SourceThroughSeq)
	if err != nil {
		return nil, err
	}
	queryInputs := make([]BatchItemInput, 0, len(sourceItems))
	for _, item := range sourceItems {
		derived, err := deriveQueryItems(item, source.ID, in.Config)
		if err != nil {
			return nil, fmt.Errorf("派生 Base Item %s(seq=%d): %w", item.ID, item.CommitSeq, err)
		}
		queryInputs = append(queryInputs, derived...)
	}
	if len(queryInputs) == 0 {
		return nil, fmt.Errorf("增量范围没有产生 Query Item")
	}
	storedBatch, prepared, err := s.datasets.prepareBatchItems(ctx, batch.ID, queryInputs)
	if err != nil {
		return nil, err
	}
	payloadHash := datasetBatchPayloadHash(prepared)
	committed, advanced, err := s.repo.CommitQueryDatasetBatchForNode(ctx, storedBatch.ID,
		in.ProducerNodeRunID, in.ProducerAttempt, payloadHash, prepared, cursor.ID,
		cursor.ProcessedThroughSeq, in.SourceThroughSeq, strings.TrimSpace(in.ProducerWorkflowRunID))
	if err != nil {
		return nil, err
	}
	return &QueryDatasetDerivation{Batch: committed, Cursor: advanced,
		SourceItemCount: len(sourceItems), QueryItemCount: len(prepared)}, nil
}

func (s *QueryDatasetService) readSourceIncrement(ctx context.Context, datasetID string,
	afterSeq, throughSeq int64) ([]model.DatasetItem, error) {
	items := make([]model.DatasetItem, 0)
	next := afterSeq
	for next < throughSeq {
		page, err := s.repo.ListDatasetItemsAfter(ctx, datasetID, next, throughSeq, queryDatasetReadPageSize)
		if err != nil {
			return nil, fmt.Errorf("读取 Base Dataset 增量: %w", err)
		}
		if len(page) == 0 {
			return nil, fmt.Errorf("Base Dataset commit_seq 在 (%d,%d] 存在断点", next, throughSeq)
		}
		for _, item := range page {
			if item.CommitSeq != next+1 {
				return nil, fmt.Errorf("Base Dataset commit_seq 不连续: 期望 %d，实际 %d", next+1, item.CommitSeq)
			}
			next = item.CommitSeq
			items = append(items, item)
		}
	}
	return items, nil
}

func deriveQueryItems(source model.DatasetItem, sourceDatasetID string,
	config QueryDerivationConfig) ([]BatchItemInput, error) {
	if strings.TrimSpace(source.ID) == "" || strings.TrimSpace(source.Fingerprint) == "" {
		return nil, fmt.Errorf("Base Item 缺少 id 或 fingerprint")
	}
	fields, err := decodeDatasetFields(source.Fields)
	if err != nil {
		return nil, err
	}
	units := []map[string]any{fields}
	if config.SemanticUnitsField != "" {
		raw, ok := fields[config.SemanticUnitsField]
		if !ok {
			return nil, fmt.Errorf("缺少 semantic_units_field %s", config.SemanticUnitsField)
		}
		array, ok := raw.([]any)
		if !ok || len(array) == 0 {
			return nil, fmt.Errorf("字段 %s 必须是非空 object 数组", config.SemanticUnitsField)
		}
		units = make([]map[string]any, len(array))
		for i, entry := range array {
			unit, ok := entry.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("字段 %s 第 %d 项必须是 object", config.SemanticUnitsField, i+1)
			}
			units[i] = unit
		}
	}

	refs, err := sourceReferences(source)
	if err != nil {
		return nil, err
	}
	results := make([]BatchItemInput, 0, len(units))
	seenUnitKeys := map[string]bool{}
	for ordinal, unit := range units {
		unitKey := strconv.Itoa(ordinal + 1)
		if config.SemanticUnitsField == "" {
			unitKey = "item"
		} else if config.UnitKeyField != "" {
			unitKey = strings.TrimSpace(valueText(querySourceValue(unit, fields, config.UnitKeyField)))
			if unitKey == "" {
				return nil, fmt.Errorf("第 %d 个语义单元的 %s 不能为空", ordinal+1, config.UnitKeyField)
			}
		}
		if seenUnitKeys[unitKey] {
			return nil, fmt.Errorf("语义单元 key 重复: %s", unitKey)
		}
		seenUnitKeys[unitKey] = true

		title := strings.TrimSpace(valueText(querySourceValue(unit, fields, config.TitleField)))
		if title == "" {
			return nil, fmt.Errorf("第 %d 个语义单元标题为空", ordinal+1)
		}
		definitions := make([]string, 0, len(config.DefinitionFields))
		for _, field := range config.DefinitionFields {
			if text := strings.TrimSpace(valueText(querySourceValue(unit, fields, field))); text != "" {
				definitions = append(definitions, text)
			}
		}
		if len(definitions) == 0 {
			return nil, fmt.Errorf("第 %d 个语义单元 definition 为空", ordinal+1)
		}
		facets := make(map[string]any, len(config.FacetFields))
		for facet, sourceField := range config.FacetFields {
			if value := querySourceValue(unit, fields, sourceField); value != nil {
				facets[facet] = value
			}
		}
		semanticKey := source.ID + ":" + unitKey
		resultFields := map[string]any{
			"semantic_unit_key":  semanticKey,
			"source_item_id":     source.ID,
			"source_fingerprint": source.Fingerprint,
			"title":              title,
			"aliases":            collectQueryTerms(unit, fields, config.AliasFields),
			"definition":         strings.Join(definitions, "\n"),
			"keywords":           collectQueryTerms(unit, fields, config.KeywordFields),
			"facets":             facets,
			"source_refs":        sourceReferenceFields(refs),
		}
		provenance := model.ItemProvenance{SourceRefs: refs, PipelineKey: config.PipelineKey,
			SourceDatasetID: sourceDatasetID, SourceDatasetItemID: source.ID,
			SourceFingerprint: source.Fingerprint, QualityStatus: "derived"}
		results = append(results, BatchItemInput{Fields: resultFields, Provenance: provenance})
	}
	return results, nil
}

func decodeDatasetFields(raw string) (map[string]any, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	var fields map[string]any
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		if err == nil {
			err = fmt.Errorf("字段根节点必须是 object")
		}
		return nil, fmt.Errorf("解析 Base Item fields: %w", err)
	}
	return fields, nil
}

func querySourceValue(unit, source map[string]any, field string) any {
	if value, ok := unit[field]; ok {
		return value
	}
	return source[field]
}

func valueText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []any:
		parts := make([]string, 0, len(typed))
		for _, entry := range typed {
			if text := strings.TrimSpace(valueText(entry)); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		raw, _ := json.Marshal(typed)
		return string(raw)
	default:
		return fmt.Sprint(typed)
	}
}

func collectQueryTerms(unit, source map[string]any, fields []string) []any {
	terms := make([]string, 0)
	for _, field := range fields {
		collectQueryTermValue(querySourceValue(unit, source, field), &terms)
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(terms))
	for _, term := range terms {
		term = strings.TrimSpace(term)
		key := strings.ToLower(term)
		if term == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, term)
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	values := make([]any, len(out))
	for i := range out {
		values[i] = out[i]
	}
	return values
}

func collectQueryTermValue(value any, target *[]string) {
	switch typed := value.(type) {
	case []any:
		for _, entry := range typed {
			collectQueryTermValue(entry, target)
		}
	case string:
		parts := strings.FieldsFunc(typed, func(r rune) bool {
			switch r {
			case ',', '，', ';', '；', '|', '\n', '\r', '\t':
				return true
			default:
				return false
			}
		})
		*target = append(*target, parts...)
	case nil:
	default:
		*target = append(*target, fmt.Sprint(typed))
	}
}

func sourceReferences(source model.DatasetItem) ([]model.SourceReference, error) {
	var provenance model.ItemProvenance
	if strings.TrimSpace(source.Provenance) != "" {
		if err := json.Unmarshal([]byte(source.Provenance), &provenance); err != nil {
			return nil, fmt.Errorf("解析 Base Item provenance: %w", err)
		}
	}
	refs := make([]model.SourceReference, 0, len(provenance.SourceRefs)+1)
	refs = append(refs, model.SourceReference{DatasetItemID: source.ID})
	for _, ref := range provenance.SourceRefs {
		if ref.DatasetItemID == "" {
			ref.DatasetItemID = source.ID
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

func sourceReferenceFields(refs []model.SourceReference) []any {
	out := make([]any, 0, len(refs))
	for _, ref := range refs {
		value := map[string]any{"dataset_item_id": ref.DatasetItemID}
		if ref.AssetID != "" {
			value["asset_id"] = ref.AssetID
		}
		if ref.BlockID != "" {
			value["block_id"] = ref.BlockID
		}
		if ref.PageNo > 0 {
			value["page_no"] = ref.PageNo
		}
		if ref.Quote != "" {
			value["quote"] = ref.Quote
		}
		out = append(out, value)
	}
	return out
}

func validateQueryDatasetContract(schema json.RawMessage, keyFields []string, config QueryDerivationConfig) error {
	if len(keyFields) != 1 || keyFields[0] != "semantic_unit_key" {
		return fmt.Errorf("Query Dataset key_fields 必须固定为 [semantic_unit_key]")
	}
	decoder := json.NewDecoder(bytes.NewReader(schema))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil {
		return fmt.Errorf("解析 Query Dataset Schema: %w", err)
	}
	properties, _ := root["properties"].(map[string]any)
	requiredTypes := map[string]string{
		"semantic_unit_key": "string", "source_item_id": "string", "source_fingerprint": "string",
		"title": "string", "aliases": "array", "definition": "string", "keywords": "array",
		"facets": "object", "source_refs": "array",
	}
	for field, expectedType := range requiredTypes {
		node, ok := properties[field].(map[string]any)
		if !ok || node["type"] != expectedType {
			return fmt.Errorf("Query Dataset Schema 必须声明 %s: %s", field, expectedType)
		}
	}
	requiredEntries, ok := root["required"].([]any)
	if !ok {
		return fmt.Errorf("Query Dataset Schema 必须声明 required")
	}
	required := map[string]bool{}
	for _, value := range requiredEntries {
		if field, ok := value.(string); ok {
			required[field] = true
		}
	}
	for field := range requiredTypes {
		if !required[field] {
			return fmt.Errorf("Query Dataset Schema 必须将 %s 声明为 required", field)
		}
	}
	for _, field := range []string{"aliases", "keywords"} {
		node, _ := properties[field].(map[string]any)
		items, _ := node["items"].(map[string]any)
		if items["type"] != "string" {
			return fmt.Errorf("Query Dataset Schema %s.items 必须是 string", field)
		}
	}
	sourceRefs, _ := properties["source_refs"].(map[string]any)
	sourceRefItems, _ := sourceRefs["items"].(map[string]any)
	if sourceRefItems["type"] != "object" {
		return fmt.Errorf("Query Dataset Schema source_refs.items 必须是 object")
	}
	facets, _ := properties["facets"].(map[string]any)
	facetProperties, _ := facets["properties"].(map[string]any)
	for facet := range config.FacetFields {
		if _, ok := facetProperties[facet]; !ok {
			return fmt.Errorf("Query Dataset Schema facets 未声明 %s", facet)
		}
	}
	return nil
}
