//go:build integration

package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	apporchestrator "reqflow/internal/app/orchestrator"
	apppipeline "reqflow/internal/app/pipeline"
	"reqflow/internal/domain/model"
	"reqflow/internal/infra/database"
	"reqflow/internal/infra/httpgin"
)

func TestIntegrationV2HTTPDatasetAndAliasBoundary(t *testing.T) {
	db, err := database.Connect(testDSN(), 3, 500)
	if err != nil {
		t.Skipf("本地 PG 不可用，跳过集成测试: %v", err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	repo := NewPipelineRepo(db)
	datasets := apppipeline.NewDatasetService(repo)
	queryDatasets, err := apppipeline.NewQueryDatasetService(repo, datasets)
	if err != nil {
		t.Fatal(err)
	}
	queries, err := apporchestrator.NewTaskQueryService(repo)
	if err != nil {
		t.Fatal(err)
	}
	engine := httpgin.New(httpgin.Services{V2Datasets: datasets, V2QueryDatasets: queryDatasets,
		V2TaskQueries: queries})

	schemaID := postID(t, engine, "/api/v2/schemas", "schema", map[string]any{
		"name": "HTTP Schema",
		"json_schema": map[string]any{"type": "object", "properties": map[string]any{
			"sku": map[string]any{"type": "string"}, "name": map[string]any{"type": "string"},
		}, "required": []string{"sku", "name"}},
	})
	datasetID := postID(t, engine, "/api/v2/datasets", "dataset", map[string]any{
		"name": "HTTP Dataset", "purpose": "base", "schema_id": schemaID, "key_fields": []string{"sku"},
	})
	queryDatasetID := postID(t, engine, "/api/v2/datasets", "dataset", map[string]any{
		"name": "HTTP Query Dataset", "purpose": "query", "schema_id": schemaID, "key_fields": []string{"sku"},
	})
	if _, err := repo.GetOrCreatePipelineCursor(context.Background(), "http_query_v1", datasetID, queryDatasetID); err != nil {
		t.Fatal(err)
	}
	cursorResponse := requestJSON(t, engine, http.MethodGet,
		"/api/v2/pipeline-cursors?pipeline_key=http_query_v1&source_dataset_id="+datasetID+"&target_dataset_id="+queryDatasetID,
		nil, http.StatusOK)
	cursorView := cursorResponse["data"].(map[string]any)["pipeline_cursor"].(map[string]any)
	if cursorView["processed_through_seq"].(float64) != 0 || cursorView["target_dataset_id"] != queryDatasetID {
		t.Fatalf("PipelineCursor 读端点错误: %+v", cursorView)
	}
	schemaResponse := requestJSON(t, engine, http.MethodGet, "/api/v2/schemas/"+schemaID, nil, http.StatusOK)
	if schemaResponse["data"].(map[string]any)["schema"].(map[string]any)["name"] != "HTTP Schema" {
		t.Fatal("V2 Schema 读端点未返回创建结果")
	}
	datasetResponse := requestJSON(t, engine, http.MethodGet, "/api/v2/datasets/"+datasetID, nil, http.StatusOK)
	if datasetResponse["data"].(map[string]any)["dataset"].(map[string]any)["schema_id"] != schemaID {
		t.Fatal("V2 Dataset 读端点未返回不可变 Schema 绑定")
	}
	tasksResponse := requestJSON(t, engine, http.MethodGet, "/api/v2/tasks?status=awaiting", nil, http.StatusOK)
	if len(tasksResponse["data"].(map[string]any)["tasks"].([]any)) != 0 {
		t.Fatal("空库不应返回 V2 Task")
	}
	batchID := postID(t, engine, "/api/v2/datasets/"+datasetID+"/batches", "batch", map[string]any{})
	postJSON(t, engine, http.MethodPost, "/api/v2/batches/"+batchID+"/commit", map[string]any{
		"items": []any{map[string]any{"fields": map[string]any{"sku": "A-1", "name": "第一条"}}},
	}, http.StatusOK)
	items := requestJSON(t, engine, http.MethodGet, "/api/v2/datasets/"+datasetID+"/items?after_seq=0", nil, http.StatusOK)
	data := items["data"].(map[string]any)
	if got := len(data["items"].([]any)); got != 1 {
		t.Fatalf("V2 增量读取 items=%d", got)
	}
	aliasName := fmt.Sprintf("http_current_%d", time.Now().UnixNano())
	if err := db.Exec(`INSERT INTO dataset_aliases (workspace_id, name, active_dataset_id) VALUES ('default', ?, ?)`,
		aliasName, datasetID).Error; err != nil {
		t.Fatal(err)
	}

	resolved, err := repo.ResolveTaskResource(context.Background(), "default", model.TaskResourceBinding{
		ResourceType: model.ResourceDatasetBoundary}, aliasName)
	if err != nil {
		t.Fatal(err)
	}
	var boundary model.DatasetBoundary
	if err := json.Unmarshal(resolved.Boundary, &boundary); err != nil {
		t.Fatal(err)
	}
	if resolved.ResourceID != datasetID || boundary.ThroughSeq != 1 {
		t.Fatalf("Alias 未解析并固化 Dataset 边界: resource=%+v boundary=%+v", resolved, boundary)
	}

	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM outbox_events WHERE aggregate_id = ?`, datasetID).Error
		_ = db.Exec(`DELETE FROM dataset_aliases WHERE name = ?`, aliasName).Error
		_ = db.Exec(`DELETE FROM datasets WHERE id IN (?, ?)`, datasetID, queryDatasetID).Error
		_ = db.Exec(`DELETE FROM dataset_schemas WHERE id = ?`, schemaID).Error
	})
}

func postID(t *testing.T, engine http.Handler, path, key string, body any) string {
	t.Helper()
	response := postJSON(t, engine, http.MethodPost, path, body, http.StatusCreated)
	data := response["data"].(map[string]any)
	return data[key].(map[string]any)["id"].(string)
}

func postJSON(t *testing.T, engine http.Handler, method, path string, body any, wantStatus int) map[string]any {
	t.Helper()
	return requestJSON(t, engine, method, path, body, wantStatus)
}

func requestJSON(t *testing.T, engine http.Handler, method, path string, body any, wantStatus int) map[string]any {
	t.Helper()
	var payload []byte
	if body != nil {
		payload, _ = json.Marshal(body)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(payload)).WithContext(context.Background())
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, req)
	if response.Code != wantStatus {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, response.Code, wantStatus, response.Body.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("解析响应: %v body=%s", err, response.Body.String())
	}
	return decoded
}

func statusOf(response map[string]any) string {
	data := response["data"].(map[string]any)
	// transition 响应 data 直接是 TaskSnapshot。
	return data["task"].(map[string]any)["status"].(string)
}
