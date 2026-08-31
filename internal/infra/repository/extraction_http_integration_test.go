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

	appextraction "reqflow/internal/app/extraction"
	apporchestrator "reqflow/internal/app/orchestrator"
	apppipeline "reqflow/internal/app/pipeline"
	"reqflow/internal/domain/model"
	"reqflow/internal/infra/blobstore"
	"reqflow/internal/infra/database"
	"reqflow/internal/infra/httpgin"
	docparser "reqflow/internal/infra/parser"
	"reqflow/internal/port"
)

func TestIntegrationV2SourceParseAndDocumentExtract(t *testing.T) {
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
	extractions, err := appextraction.NewService(repo, llm,
		"integration-model", appextraction.Options{MaxUnitRunes: 10})
	if err != nil {
		t.Fatal(err)
	}
	parseExecutor, _ := apppipeline.NewSourceParseExecutor(assets)
	extractExecutor, _ := appextraction.NewExecutor(extractions)
	cleaning, err := apppipeline.NewCleaningService(repo)
	if err != nil {
		t.Fatal(err)
	}
	transformExecutor, _ := apppipeline.NewDataTransformExecutor(cleaning)
	validateExecutor, _ := apppipeline.NewDataValidateExecutor(cleaning)
	datasets := apppipeline.NewDatasetService(repo)
	publish, err := apppipeline.NewPublishService(repo, datasets)
	if err != nil {
		t.Fatal(err)
	}
	publishExecutor, _ := apppipeline.NewDataPublishExecutor(publish)
	registry, err := apporchestrator.NewRegistry(parseExecutor, extractExecutor, transformExecutor,
		validateExecutor, publishExecutor)
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
	review, err := apppipeline.NewReviewService(repo, runtime)
	if err != nil {
		t.Fatal(err)
	}
	engine := httpgin.New(httpgin.Services{V2Datasets: datasets,
		V2Assets: assets, V2Extractions: extractions, V2Cleaning: cleaning, V2Definitions: definitions,
		V2Runtime: runtime, V2Review: review, MaxFileMB: 2})
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
		"name": "产品规格清洗闭环", "status": "active",
		"input_ports": map[string]any{
			"source": map[string]any{"resource_type": "asset_set", "required": true},
			"target": map[string]any{"resource_type": "dataset", "required": true},
		},
		"output_ports":    map[string]any{"batch": map[string]any{"resource_type": "dataset_batch"}},
		"output_bindings": map[string]any{"batch": "$step.publish.batch"},
		"steps": []any{
			map[string]any{"id": "parse", "name": "解析", "kind": "source.parse",
				"inputs":  map[string]any{"assets": "$task.source"},
				"outputs": map[string]any{"documents": "parsed_documents"}},
			map[string]any{"id": "extract", "name": "抽取", "kind": "document.extract",
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
			map[string]any{"id": "review", "name": "人工审核", "kind": "human.review",
				"depends_on": []string{"validate"}, "inputs": map[string]any{"validation": "$step.validate.validation"},
				"outputs": map[string]any{"approved": "approved_records"},
				"config":  map[string]any{"allow_edit": true}},
			map[string]any{"id": "publish", "name": "原子发布", "kind": "data.publish",
				"depends_on": []string{"review"}, "inputs": map[string]any{"approved": "$step.review.approved"},
				"outputs": map[string]any{"batch": "dataset_batch"}},
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
	if llm.calls != firstAttemptCalls+4 {
		t.Fatalf("retry must resume the failed unit after its completed list turn: first=%d total=%d", firstAttemptCalls, llm.calls)
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot := requestJSON(t, engine, http.MethodGet, "/api/v2/tasks/"+taskID, nil, http.StatusOK)
	data := snapshot["data"].(map[string]any)
	if data["task"].(map[string]any)["status"] != string(model.TaskStatusAwaiting) {
		t.Fatalf("task must await review after validation: %+v", data)
	}
	stepOutputs := data["step_outputs"].(map[string]any)
	validationSetID := stepOutputs["validate"].([]any)[0].(map[string]any)["resource_id"].(string)
	validationResponse := requestJSON(t, engine, http.MethodGet,
		"/api/v2/validation-result-sets/"+validationSetID, nil, http.StatusOK)
	validationSet := validationResponse["data"].(map[string]any)["validation_result_set"].(map[string]any)
	if validationSet["status"] != model.ValidationResultSetSucceeded ||
		validationSet["record_count"].(float64) == 0 || validationSet["target_dataset_id"] != datasetID ||
		int64(validationSet["validated_through_seq"].(float64)) != 1 {
		t.Fatalf("validation manifest=%+v", validationSet)
	}
	firstValidation := validationSet["results"].([]any)[0].(map[string]any)
	if firstValidation["draft_fields"].(map[string]any)["sku"] != "A-100" ||
		firstValidation["field_confidence"].(map[string]any)["sku"].(float64) != 0.99 {
		t.Fatalf("validation review evidence missing draft/confidence: %+v", firstValidation)
	}
	validationProvenance := firstValidation["provenance"].(map[string]any)
	if len(validationProvenance["source_refs"].([]any)) == 0 {
		t.Fatalf("validation review evidence missing provenance: %+v", firstValidation)
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
	reviewDecisions := make([]any, 0, len(validationSet["results"].([]any)))
	for i, rawResult := range validationSet["results"].([]any) {
		result := rawResult.(map[string]any)
		decision := map[string]any{"validation_result_id": result["id"], "action": "exclude",
			"note": "批内重复候选不发布"}
		if i == 0 {
			decision["action"] = "edit"
			decision["fields"] = map[string]any{"sku": "A-101", "name": "产品 A（审核版）"}
			decision["note"] = "修正与基础库冲突的 SKU"
		}
		reviewDecisions = append(reviewDecisions, decision)
	}
	reviewRequest := map[string]any{"reviewer": "integration-reviewer",
		"rationale": "已核对原文并修正冲突主键", "decisions": reviewDecisions}
	reviewed := postJSON(t, engine, http.MethodPost,
		"/api/v2/tasks/"+taskID+"/steps/review/approve", reviewRequest, http.StatusOK)
	if statusOf(reviewed) != string(model.TaskStatusRunning) {
		t.Fatalf("review completion must queue publish: %+v", reviewed)
	}
	reviewData := reviewed["data"].(map[string]any)
	approvedSetID := reviewData["step_outputs"].(map[string]any)["review"].([]any)[0].(map[string]any)["resource_id"].(string)
	approvedResponse := requestJSON(t, engine, http.MethodGet,
		"/api/v2/approved-record-sets/"+approvedSetID, nil, http.StatusOK)
	approvedSet := approvedResponse["data"].(map[string]any)["approved_record_set"].(map[string]any)
	if approvedSet["edited_count"].(float64) != 1 ||
		int(approvedSet["excluded_count"].(float64)) != len(reviewDecisions)-1 {
		t.Fatalf("approved manifest=%+v", approvedSet)
	}
	// 同一审核请求可安全重试；客户端不能替换服务端生成的 ApprovedRecordSet。
	postJSON(t, engine, http.MethodPost, "/api/v2/tasks/"+taskID+"/steps/review/approve",
		reviewRequest, http.StatusOK)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	completed := requestJSON(t, engine, http.MethodGet, "/api/v2/tasks/"+taskID, nil, http.StatusOK)
	completedData := completed["data"].(map[string]any)
	if completedData["task"].(map[string]any)["status"] != string(model.TaskStatusSucceeded) {
		t.Fatalf("full cleaning task failed: %+v", completedData)
	}
	batchID := completedData["outputs"].([]any)[0].(map[string]any)["resource_id"].(string)
	publishedItems := requestJSON(t, engine, http.MethodGet,
		"/api/v2/datasets/"+datasetID+"/items?after_seq=1", nil, http.StatusOK)
	newItems := publishedItems["data"].(map[string]any)["items"].([]any)
	if len(newItems) != 1 || newItems[0].(map[string]any)["fields"].(map[string]any)["sku"] != "A-101" {
		t.Fatalf("published items=%+v", newItems)
	}
	publishedProvenance := newItems[0].(map[string]any)["provenance"].(map[string]any)
	if publishedProvenance["approved_record_set_id"] != approvedSetID ||
		publishedProvenance["validation_result_id"] == "" {
		t.Fatalf("published provenance=%+v", publishedProvenance)
	}
	publishExecution, err := repo.GetTaskExecution(context.Background(), taskID)
	if err != nil {
		t.Fatal(err)
	}
	publishRun := publishExecution.Steps[len(publishExecution.Steps)-1]
	if _, err := publish.PublishApprovedRecords(context.Background(), apppipeline.PublishApprovedRecordsInput{
		ApprovedRecordSetID: approvedSetID, SourceTaskID: taskID, SourceStepRunID: publishRun.ID,
		ProducerAttempt: publishRun.Attempt}); !errors.Is(err, port.ErrStaleResourceExecution) {
		t.Fatalf("终态发布步骤必须拒绝旧执行写入: %v", err)
	}
	if batchID == "" {
		t.Fatal("publish did not bind DatasetBatch")
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
		_ = db.Exec(`DELETE FROM dataset_batches WHERE source_task_id = ?`, taskID).Error
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
	client.calls++
	if client.calls == client.failCall {
		return &port.Message{Role: port.RoleAssistant, StopReason: port.StopReasonError,
				Usage: port.Usage{Input: 10, Output: 2}, ErrorMessage: "transient provider failure"},
			fmt.Errorf("transient provider failure")
	}
	toolName, arguments := "list_source_blocks", json.RawMessage(`{}`)
	if extractionContextHasToolResult(c, "list_source_blocks") {
		toolName, arguments = "read_source_blocks", json.RawMessage(`{"offset":0,"limit":20}`)
	}
	if extractionContextHasToolResult(c, "read_source_blocks") {
		blockID, quote, err := extractionReadSource(c)
		if err != nil {
			return nil, err
		}
		arguments, _ = json.Marshal(map[string]any{"records": []any{map[string]any{
			"draft_key":        "sku:A-100",
			"fields":           map[string]any{"sku": "A-100", "name": "产品 A"},
			"field_confidence": map[string]any{"sku": 0.99, "name": 0.95},
			"source_refs":      []any{map[string]any{"block_id": blockID, "quote": quote}},
		}}})
		toolName = "upsert_record_drafts"
	}
	if extractionContextHasSuccessfulToolResult(c, "upsert_record_drafts") {
		toolName, arguments = "validate_record_drafts", json.RawMessage(`{}`)
	}
	if extractionContextHasSuccessfulToolResult(c, "validate_record_drafts") {
		toolName, arguments = "finish_extraction_unit", json.RawMessage(`{"outcome":"records"}`)
	}
	call := port.ToolCall{ID: fmt.Sprintf("call-%d", client.calls), Name: toolName, Arguments: arguments}
	return &port.Message{Role: port.RoleAssistant, StopReason: port.StopReasonToolUse,
		Usage: port.Usage{Input: 10, Output: 2},
		Content: []port.Block{{Type: port.BlockThinking, Thinking: "follow extraction tool protocol"},
			{Type: port.BlockToolCall, ToolCall: &call}}}, nil
}

func (client *extractionIntegrationLLM) Stream(ctx context.Context, c *port.Context, _ func(port.AssistantEvent)) (*port.Message, error) {
	return client.Complete(ctx, c)
}

func (*extractionIntegrationLLM) Ping(context.Context) error { return nil }

func extractionContextHasToolResult(c *port.Context, name string) bool {
	for _, message := range c.Messages {
		if message.Role == port.RoleToolResult && message.ToolName == name {
			return true
		}
	}
	return false
}

func extractionContextHasSuccessfulToolResult(c *port.Context, name string) bool {
	for _, message := range c.Messages {
		if message.Role == port.RoleToolResult && message.ToolName == name && !message.IsError {
			return true
		}
	}
	return false
}

func extractionReadSource(c *port.Context) (string, string, error) {
	for index := len(c.Messages) - 1; index >= 0; index-- {
		message := c.Messages[index]
		if message.Role != port.RoleToolResult || message.ToolName != "read_source_blocks" || message.IsError {
			continue
		}
		var result struct {
			Blocks []struct {
				BlockID string `json:"block_id"`
				Text    string `json:"text"`
			} `json:"blocks"`
		}
		if err := json.Unmarshal([]byte(message.Result), &result); err == nil && len(result.Blocks) > 0 && result.Blocks[0].Text != "" {
			return result.Blocks[0].BlockID, result.Blocks[0].Text, nil
		}
	}
	return "", "", fmt.Errorf("read_source_blocks result missing")
}
