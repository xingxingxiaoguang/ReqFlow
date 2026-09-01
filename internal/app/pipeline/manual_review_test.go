package pipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reqflow/internal/domain/model"
	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

// manualReviewRepo 覆盖人工审核完成处理器实际调用的仓储方法；其余
// ReviewPipelineRepo 方法通过接口嵌入保持编译，测试不会触达。
type testReviewError string

func (e testReviewError) Error() string { return string(e) }

type manualReviewRepo struct {
	port.ReviewPipelineRepo
	set       *model.ValidationResultSet
	results   []model.ValidationResult
	dataset   *model.Dataset
	schema    *model.DatasetSchemaDefinition
	approved  *model.ApprovedRecordSet
	decisions []model.RecordReviewDecision
}

func (r *manualReviewRepo) GetValidationResultSet(context.Context, string) (*model.ValidationResultSet, error) {
	return r.set, nil
}
func (r *manualReviewRepo) ListValidationResults(context.Context, string) ([]model.ValidationResult, error) {
	return r.results, nil
}
func (r *manualReviewRepo) GetAppendDataset(context.Context, string) (*model.Dataset, error) {
	return r.dataset, nil
}
func (r *manualReviewRepo) GetDatasetSchema(context.Context, string) (*model.DatasetSchemaDefinition, error) {
	return r.schema, nil
}
func (r *manualReviewRepo) CreateApprovedRecordSet(_ context.Context, set *model.ApprovedRecordSet,
	decisions []model.RecordReviewDecision) (*model.ApprovedRecordSet, error) {
	if set.RecordCount <= 0 || len(decisions) != set.RecordCount ||
		set.ApprovedCount+set.EditedCount+set.ExcludedCount != set.RecordCount {
		return nil, testReviewError("ApprovedRecordSet 决策数量非法")
	}
	if r.approved != nil {
		if r.approved.ReviewHash != set.ReviewHash {
			return nil, testReviewError("已存在不同内容的不可变审核结论")
		}
		return r.approved, nil
	}
	stored := *set
	stored.ID = "approved-1"
	r.approved, r.decisions = &stored, decisions
	return &stored, nil
}

func reviewFixture(t *testing.T) (*manualReviewRepo, *domain.StaticCatalog) {
	t.Helper()
	contract := domain.DataContract{RecordGranularity: "一行一个 SKU",
		KeyFields: []string{"sku"},
		Fields: []domain.FieldContract{
			{Key: "sku", Label: "编码", Type: domain.FieldString, Required: true},
			{Key: "count", Label: "数量", Type: domain.FieldInteger, Required: true},
		}}
	schema, schemaHash, err := domain.CompileDataContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	repo := &manualReviewRepo{
		set: &model.ValidationResultSet{ID: "validation-1", TargetDatasetID: "dataset-1",
			TargetSchemaID: "schema-1", Status: model.ValidationResultSetSucceeded,
			ValidatedThroughSeq: 10, RecordCount: 3},
		results: []model.ValidationResult{
			{ID: "result-1", TransformedRecordID: "record-1", Ordinal: 0,
				Fields: json.RawMessage(`{"sku":"A","count":1}`), ItemKey: "key-1", Status: model.ValidationRecordValid},
			{ID: "result-2", TransformedRecordID: "record-2", Ordinal: 1,
				Fields: json.RawMessage(`{"sku":"B","count":2}`), ItemKey: "key-2", Status: model.ValidationRecordInvalid},
			{ID: "result-3", TransformedRecordID: "record-3", Ordinal: 2,
				Fields: json.RawMessage(`{"sku":"C","count":3}`), ItemKey: "key-3", Status: model.ValidationRecordWarning},
		},
		dataset: &model.Dataset{ID: "dataset-1", SchemaID: "schema-1", KeyFields: []string{"sku"},
			Status: model.DatasetStatusActive},
		schema: &model.DatasetSchemaDefinition{ID: "schema-1", JSONSchema: schema, SchemaHash: schemaHash},
	}
	return repo, nil
}

func reviewExecution(payload string) port.WorkflowManualExecution {
	return port.WorkflowManualExecution{WorkspaceID: "default", RunID: "run-1",
		NodeRunID: "node-run-1", Attempt: 1, Actor: "reviewer-1",
		Payload: json.RawMessage(payload),
		Inputs: []domain.NodeResourceBinding{{Port: "validation", Direction: domain.BindingInput,
			ResourceType: domain.ResourceValidationResults, ResourceID: "validation-1"}}}
}

func TestReviewManualCompleterCreatesImmutableApprovedRecordSet(t *testing.T) {
	repo, _ := reviewFixture(t)
	completer, err := NewWorkflowReviewManualCompleter(repo)
	if err != nil {
		t.Fatal(err)
	}
	outputs, err := completer.Complete(context.Background(), reviewExecution(`{
		"rationale": "逐条核对无误",
		"decisions": [
			{"validation_result_id": "result-1", "action": "approve"},
			{"validation_result_id": "result-2", "action": "exclude", "note": "证据不足"},
			{"validation_result_id": "result-3", "action": "edit", "fields": {"sku": "C", "count": 30}}
		]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(outputs) != 1 || outputs[0].Port != "approved" ||
		outputs[0].ResourceType != domain.ResourceApprovedRecords || outputs[0].ResourceID != "approved-1" {
		t.Fatalf("输出绑定必须由服务端生成: %+v", outputs)
	}
	stored := repo.approved
	if stored.RecordCount != 3 || stored.ApprovedCount != 1 || stored.EditedCount != 1 || stored.ExcludedCount != 1 {
		t.Fatalf("审核计数非法: %+v", stored)
	}
	if stored.Reviewer != "reviewer-1" || stored.ReviewHash == "" || stored.ReviewedThroughSeq != 10 {
		t.Fatalf("审核身份不完整: %+v", stored)
	}
	edited := repo.decisions[2]
	if edited.Action != model.ReviewActionEdit || edited.ItemKey == "" || edited.Fingerprint == "" {
		t.Fatalf("编辑决定必须服务端重算身份: %+v", edited)
	}
	if !strings.Contains(string(edited.Fields), `"count":30`) {
		t.Fatalf("编辑字段应来自客户端载荷: %s", edited.Fields)
	}
	approved := repo.decisions[0]
	if string(approved.Fields) != `{"sku":"A","count":1}` || approved.ItemKey != "key-1" {
		t.Fatalf("approve 必须沿用校验结果字段: %+v", approved)
	}
}

func TestReviewManualCompleterRejectsIncompleteCoverage(t *testing.T) {
	repo, _ := reviewFixture(t)
	completer, _ := NewWorkflowReviewManualCompleter(repo)
	if _, err := completer.Complete(context.Background(), reviewExecution(`{
		"rationale": "漏掉一条",
		"decisions": [{"validation_result_id": "result-1", "action": "approve"}]}`)); err == nil {
		t.Fatal("审核决定必须覆盖全部校验记录")
	}
	if _, err := completer.Complete(context.Background(), reviewExecution(`{
		"rationale": "非法动作",
		"decisions": [
			{"validation_result_id": "result-1", "action": "approve"},
			{"validation_result_id": "result-2", "action": "delete"},
			{"validation_result_id": "result-3", "action": "approve"}]}`)); err == nil {
		t.Fatal("非法审核动作必须被拒绝")
	}
	if _, err := completer.Complete(context.Background(), reviewExecution(`{
		"rationale": "编辑缺字段",
		"decisions": [
			{"validation_result_id": "result-1", "action": "approve"},
			{"validation_result_id": "result-2", "action": "edit"},
			{"validation_result_id": "result-3", "action": "approve"}]}`)); err == nil {
		t.Fatal("edit 必须提供 fields")
	}
	if repo.approved != nil {
		t.Fatal("失败路径不得产生 ApprovedRecordSet")
	}
}

func TestReviewManualCompleterIsIdempotentPerNodeRun(t *testing.T) {
	repo, _ := reviewFixture(t)
	completer, _ := NewWorkflowReviewManualCompleter(repo)
	first, err := completer.Complete(context.Background(), reviewExecution(`{
		"rationale": "逐条核对无误",
		"decisions": [
			{"validation_result_id": "result-1", "action": "approve"},
			{"validation_result_id": "result-2", "action": "exclude"},
			{"validation_result_id": "result-3", "action": "approve"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := completer.Complete(context.Background(), reviewExecution(`{
		"rationale": "逐条核对无误（网络重试）",
		"decisions": [
			{"validation_result_id": "result-1", "action": "approve"},
			{"validation_result_id": "result-2", "action": "exclude"},
			{"validation_result_id": "result-3", "action": "approve"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if first[0].ResourceID != second[0].ResourceID {
		t.Fatalf("相同决定的重试必须复用结论: %s vs %s", first[0].ResourceID, second[0].ResourceID)
	}
	diverged, err := completer.Complete(context.Background(), reviewExecution(`{
		"rationale": "改主意了",
		"decisions": [
			{"validation_result_id": "result-1", "action": "exclude"},
			{"validation_result_id": "result-2", "action": "exclude"},
			{"validation_result_id": "result-3", "action": "approve"}]}`))
	if err == nil {
		t.Fatalf("不同内容的二次提交必须被拒绝: %+v", diverged)
	}
}
