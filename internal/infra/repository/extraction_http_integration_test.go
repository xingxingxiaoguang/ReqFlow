//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	apporchestrator "reqflow/internal/app/orchestrator"
	apppipeline "reqflow/internal/app/pipeline"
	"reqflow/internal/domain/model"
	"reqflow/internal/infra/blobstore"
	"reqflow/internal/infra/database"
	"reqflow/internal/infra/httpgin"
	docparser "reqflow/internal/infra/parser"
	"reqflow/internal/port"
)

func TestIntegrationV2SourceParseAndLLMExtract(t *testing.T) {
	db, err := database.Connect(testDSN(), 3, 500)
	if err != nil {
		t.Skipf("本地 PG 不可用，跳过集成测试: %v", err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	repo := NewPipelineRepo(db)
	store, err := blobstore.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	assets, err := apppipeline.NewAssetService(repo, store,
		docparser.New(docparser.Options{MaxFileMB: 2}), 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	llm := &extractionIntegrationLLM{failCall: 2}
	extractions, err := apppipeline.NewExtractionService(repo, llm,
		"integration-model", apppipeline.ExtractionOptions{MaxUnitRunes: 10})
	if err != nil {
		t.Fatal(err)
	}
	parseExecutor, _ := apppipeline.NewSourceParseExecutor(assets)
	extractExecutor, _ := apppipeline.NewLLMExtractExecutor(extractions)
	cleaning, err := apppipeline.NewCleaningService(repo)
	if err != nil {
		t.Fatal(err)
	}
	transformExecutor, _ := apppipeline.NewDataTransformExecutor(cleaning)
	validateExecutor, _ := apppipeline.NewDataValidateExecutor(cleaning)
	registry, err := apporchestrator.NewRegistry(parseExecutor, extractExecutor, transformExecutor, validateExecutor)
	if err != nil {
		t.Fatal(err)
	}
	definitions := apporchestrator.NewDefinitionService(repo, registry, repo)
	scheduler := apporchestrator.NewScheduler(repo)
	worker, err := apporchestrator.NewWorker(repo, registry, scheduler, apporchestrator.WorkerOptions{
		Owner: "extract-http-integration", LeaseDuration: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := apporchestrator.NewRuntimeService(repo, scheduler, worker)
	if err != nil {
		t.Fatal(err)
	}
	engine := httpgin.New(httpgin.Services{V2Datasets: apppipeline.NewDatasetService(repo),
		V2Assets: assets, V2Extractions: extractions, V2Cleaning: cleaning, V2Definitions: definitions,
		V2Runtime: runtime, MaxFileMB: 2})
	workspaceID := fmt.Sprintf("extract-http-%d", time.Now().UnixNano())

	schemaID := postID(t, engine, "/api/v2/schemas", "schema", map[string]any{
		"workspace_id": workspaceID, "name": "产品候选",
		"json_schema": map[string]any{"type": "object", "additionalProperties": false,
			"properties": map[string]any{"sku": map[string]any{"type": "string"},
				"name": map[string]any{"type": "string"}}, "required": []string{"sku", "name"}},
	})
	profileID := postID(t, engine, "/api/v2/extraction-profiles", "extraction_profile", map[string]any{
		"workspace_id": workspaceID, "name": "产品规格抽取", "target_schema_id": schemaID,
		"record_granularity": "每个产品一条记录", "system_instruction": "抽取 SKU 和产品名称",
		"field_guides": map[string]any{"sku": "原文中的 SKU", "name": "产品名称"},
		"validation_rules": []any{map[string]any{"field": "sku", "operation": "regex",
			"pattern": "^[A-Z]-[0-9]+$", "severity": "warning"}},
	})
	datasetID := postID(t, engine, "/api/v2/datasets", "dataset", map[string]any{
		"workspace_id": workspaceID, "name": "产品基础数据", "purpose": "base",
		"schema_id": schemaID, "key_fields": []string{"sku"},
	})
	existingBatchID := postID(t, engine, "/api/v2/datasets/"+datasetID+"/batches", "batch", map[string]any{})
	postJSON(t, engine, http.MethodPost, "/api/v2/batches/"+existingBatchID+"/commit", map[string]any{
		"items": []any{map[string]any{"fields": map[string]any{"sku": "A-100", "name": "已存在产品"}}},
	}, http.StatusOK)
	assetID, _ := uploadV2Asset(t, engine, workspaceID, "product.md", "text/markdown",
		[]byte("# 产品 A\n\nSKU: A-100\n\n名称：产品 A"), http.StatusCreated)
	assetSetID := postID(t, engine, "/api/v2/asset-sets", "asset_set", map[string]any{
		"workspace_id": workspaceID, "name": "产品文档", "asset_ids": []string{assetID},
	})
	definitionID := postID(t, engine, "/api/v2/task-definitions", "definition", map[string]any{
		"workspace_id": workspaceID, "key": fmt.Sprintf("extract_%d", time.Now().UnixNano()),
		"name": "解析、抽取、转换并校验", "status": "active",
		"input_ports": map[string]any{
			"source": map[string]any{"resource_type": "asset_set", "required": true},
			"target": map[string]any{"resource_type": "dataset", "required": true},
		},
		"output_ports":    map[string]any{"validation": map[string]any{"resource_type": "validation_results"}},
		"output_bindings": map[string]any{"validation": "$step.validate.validation"},
		"steps": []any{
			map[string]any{"id": "parse", "name": "解析", "kind": "source.parse",
				"inputs":  map[string]any{"assets": "$task.source"},
				"outputs": map[string]any{"documents": "parsed_documents"}},
			map[string]any{"id": "extract", "name": "抽取", "kind": "llm.extract",
				"depends_on": []string{"parse"}, "inputs": map[string]any{"documents": "$step.parse.documents"},
				"outputs": map[string]any{"drafts": "record_drafts"},
				"config":  map[string]any{"extraction_profile_id": profileID}},
			map[string]any{"id": "transform", "name": "确定性转换", "kind": "data.transform",
				"depends_on": []string{"extract"}, "inputs": map[string]any{"drafts": "$step.extract.drafts"},
				"outputs": map[string]any{"records": "transformed_records"}},
			map[string]any{"id": "validate", "name": "校验", "kind": "data.validate",
				"depends_on": []string{"transform"}, "inputs": map[string]any{
					"records": "$step.transform.records", "dataset": "$task.target"},
				"outputs": map[string]any{"validation": "validation_results"}},
		},
	})
	taskID := postID(t, engine, "/api/v2/tasks", "task", map[string]any{
		"definition_id": definitionID, "title": "抽取并校验产品", "bindings": []any{
			map[string]any{"port_name": "source", "resource_type": "asset_set", "resource_id": assetSetID},
			map[string]any{"port_name": "target", "resource_type": "dataset", "resource_id": datasetID},
		},
	})
	postJSON(t, engine, http.MethodPost, "/api/v2/tasks/"+taskID+"/start", nil, http.StatusOK)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	failedExecution, err := repo.GetTaskExecution(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	if failedExecution.Task.Status != model.TaskStatusFailed || failedExecution.Steps[1].Status != model.StepRunFailed {
		t.Fatalf("partial extraction must fail the step before retry: %+v", failedExecution)
	}
	partialSet, _, err := repo.GetRecordDraftSetByStepRun(context.Background(), failedExecution.Steps[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if partialSet.Status != model.RecordDraftSetPartial || partialSet.SucceededUnitCount == 0 ||
		partialSet.FailedUnitCount != 1 {
		t.Fatalf("partial manifest did not preserve unit outcomes: %+v", partialSet)
	}
	firstAttemptCalls := llm.calls
	if err := runtime.Retry(context.Background(), taskID, "extract"); err != nil {
		t.Fatal(err)
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if llm.calls != firstAttemptCalls+1 {
		t.Fatalf("retry must call LLM only for the failed unit: first=%d total=%d", firstAttemptCalls, llm.calls)
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot := requestJSON(t, engine, http.MethodGet, "/api/v2/tasks/"+taskID, nil, http.StatusOK)
	data := snapshot["data"].(map[string]any)
	if data["task"].(map[string]any)["status"] != string(model.TaskStatusSucceeded) {
		t.Fatalf("parse+extract+transform+validate task failed: %+v", data)
	}
	outputs := data["outputs"].([]any)
	validationSetID := outputs[0].(map[string]any)["resource_id"].(string)
	validationResponse := requestJSON(t, engine, http.MethodGet,
		"/api/v2/validation-result-sets/"+validationSetID, nil, http.StatusOK)
	validationSet := validationResponse["data"].(map[string]any)["validation_result_set"].(map[string]any)
	if validationSet["status"] != model.ValidationResultSetSucceeded ||
		validationSet["record_count"].(float64) == 0 || validationSet["target_dataset_id"] != datasetID ||
		int64(validationSet["validated_through_seq"].(float64)) != 1 {
		t.Fatalf("validation manifest=%+v", validationSet)
	}
	foundExistingConflict := false
	for _, rawResult := range validationSet["results"].([]any) {
		for _, rawIssue := range rawResult.(map[string]any)["issues"].([]any) {
			if rawIssue.(map[string]any)["code"] == "conflict_existing_key" {
				foundExistingConflict = true
			}
		}
	}
	if !foundExistingConflict {
		t.Fatalf("validation did not detect key conflict at pinned seq: %+v", validationSet)
	}
	transformedSetID := validationSet["transformed_record_set_id"].(string)
	transformedResponse := requestJSON(t, engine, http.MethodGet,
		"/api/v2/transformed-record-sets/"+transformedSetID, nil, http.StatusOK)
	transformedSet := transformedResponse["data"].(map[string]any)["transformed_record_set"].(map[string]any)
	if transformedSet["status"] != model.TransformedRecordSetSucceeded || transformedSet["transformed_count"].(float64) == 0 {
		t.Fatalf("transformed manifest=%+v", transformedSet)
	}
	storedTransform, storedRecords, err := cleaning.GetTransformedRecordSet(context.Background(), transformedSetID)
	if err != nil || len(storedRecords) == 0 {
		t.Fatalf("读取转换结果: set=%+v records=%d err=%v", storedTransform, len(storedRecords), err)
	}
	if err := repo.SaveTransformedRecord(context.Background(), transformedSetID, storedTransform.ProducerAttempt,
		&storedRecords[0]); !errors.Is(err, port.ErrStaleResourceExecution) {
		t.Fatalf("终态 Step 必须拒绝过期转换写入: %v", err)
	}
	storedValidation, storedResults, err := cleaning.GetValidationResultSet(context.Background(), validationSetID)
	if err != nil || len(storedResults) == 0 {
		t.Fatalf("读取校验结果: set=%+v results=%d err=%v", storedValidation, len(storedResults), err)
	}
	if err := repo.SaveValidationResult(context.Background(), validationSetID, storedValidation.ProducerAttempt,
		&storedResults[0]); !errors.Is(err, port.ErrStaleResourceExecution) {
		t.Fatalf("终态 Step 必须拒绝过期校验写入: %v", err)
	}
	draftSetID := transformedSet["record_draft_set_id"].(string)
	response := requestJSON(t, engine, http.MethodGet,
		"/api/v2/record-draft-sets/"+draftSetID, nil, http.StatusOK)
	draftSet := response["data"].(map[string]any)["record_draft_set"].(map[string]any)
	if draftSet["status"] != model.RecordDraftSetSucceeded || draftSet["draft_count"].(float64) == 0 {
		t.Fatalf("record draft manifest=%+v", draftSet)
	}
	draft := draftSet["drafts"].([]any)[0].(map[string]any)
	fields := draft["fields"].(map[string]any)
	if fields["sku"] != "A-100" || fields["name"] != "产品 A" {
		t.Fatalf("draft fields=%+v", fields)
	}
	provenance := draft["provenance"].(map[string]any)
	refs := provenance["source_refs"].([]any)
	if len(refs) != 1 || refs[0].(map[string]any)["asset_id"] != assetID ||
		refs[0].(map[string]any)["block_id"] == "" {
		t.Fatalf("draft provenance=%+v", provenance)
	}
	if int(draftSet["llm_request_count"].(float64)) != llm.calls {
		t.Fatalf("persisted request count=%v actual=%d", draftSet["llm_request_count"], llm.calls)
	}
	if int(draftSet["input_tokens"].(float64)) != llm.calls*10 ||
		int(draftSet["output_tokens"].(float64)) != llm.calls*2 {
		t.Fatalf("persisted token usage does not include retries: %+v", draftSet)
	}
	if err := repo.FailExtractionUnit(context.Background(), draftSetID, 1,
		draftSet["units"].([]any)[0].(map[string]any)["unit_key"].(string), "stale", model.LLMUsage{}); !errors.Is(err, port.ErrStaleResourceExecution) {
		t.Fatalf("旧 attempt 必须被 draft fencing 拒绝: %v", err)
	}

	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM tasks WHERE id = ?`, taskID).Error
		_ = db.Exec(`DELETE FROM task_definitions WHERE id = ?`, definitionID).Error
		_ = db.Exec(`DELETE FROM asset_sets WHERE id = ?`, assetSetID).Error
		_ = db.Exec(`DELETE FROM assets WHERE id = ?`, assetID).Error
		_ = db.Exec(`DELETE FROM datasets WHERE id = ?`, datasetID).Error
		_ = db.Exec(`DELETE FROM extraction_profiles WHERE id = ?`, profileID).Error
		_ = db.Exec(`DELETE FROM dataset_schemas WHERE id = ?`, schemaID).Error
	})
}

type extractionIntegrationLLM struct {
	calls    int
	failCall int
}

func (client *extractionIntegrationLLM) Complete(_ context.Context, c *port.Context) (*port.Message, error) {
	var request struct {
		Blocks []struct {
			BlockID string `json:"block_id"`
			Text    string `json:"text"`
		} `json:"blocks"`
	}
	if len(c.Messages) == 0 || json.Unmarshal([]byte(c.Messages[0].Text()), &request) != nil || len(request.Blocks) == 0 {
		return nil, fmt.Errorf("invalid extraction prompt")
	}
	block := request.Blocks[0]
	client.calls++
	if client.calls == client.failCall {
		call := port.ToolCall{ID: "call-invalid", Name: "emit_records",
			Arguments: json.RawMessage(`{"records":[{"fields":{"sku":"bad"},"source_refs":[{"block_id":"outside","quote":"fabricated"}]}]}`)}
		return &port.Message{Role: port.RoleAssistant, StopReason: port.StopReasonToolUse,
			Usage:   port.Usage{Input: 10, Output: 2},
			Content: []port.Block{{Type: port.BlockToolCall, ToolCall: &call}}}, nil
	}
	arguments, _ := json.Marshal(map[string]any{"records": []any{map[string]any{
		"fields":           map[string]any{"sku": "A-100", "name": "产品 A"},
		"field_confidence": map[string]any{"sku": 0.99, "name": 0.95},
		"source_refs":      []any{map[string]any{"block_id": block.BlockID, "quote": block.Text}},
	}}})
	call := port.ToolCall{ID: "call-1", Name: "emit_records", Arguments: arguments}
	return &port.Message{Role: port.RoleAssistant, StopReason: port.StopReasonToolUse,
		Usage:   port.Usage{Input: 10, Output: 2},
		Content: []port.Block{{Type: port.BlockToolCall, ToolCall: &call}}}, nil
}

func (client *extractionIntegrationLLM) Stream(ctx context.Context, c *port.Context, _ func(port.AssistantEvent)) (*port.Message, error) {
	return client.Complete(ctx, c)
}

func (*extractionIntegrationLLM) Ping(context.Context) error { return nil }
