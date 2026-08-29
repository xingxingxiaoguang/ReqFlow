//go:build integration

package repository

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
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

func TestIntegrationV2AssetUploadAndSourceParse(t *testing.T) {
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
	executor, err := apppipeline.NewSourceParseExecutor(assets)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := apporchestrator.NewRegistry(executor)
	if err != nil {
		t.Fatal(err)
	}
	definitions := apporchestrator.NewDefinitionService(repo, registry, repo)
	scheduler := apporchestrator.NewScheduler(repo)
	worker, err := apporchestrator.NewWorker(repo, registry, scheduler, apporchestrator.WorkerOptions{
		Owner: "asset-http-integration", LeaseDuration: 10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := apporchestrator.NewRuntimeService(repo, scheduler, worker)
	if err != nil {
		t.Fatal(err)
	}
	engine := httpgin.New(httpgin.Services{V2Assets: assets, V2Definitions: definitions,
		V2Runtime: runtime, MaxFileMB: 2})
	workspaceID := fmt.Sprintf("asset-http-%d", time.Now().UnixNano())

	markdown := []byte("<!-- page=3 -->\n# 产品规格\n\n一段说明。\n\n| 参数 | 值 |\n|---|---|\n| 电压 | 220V |")
	goodID, created := uploadV2Asset(t, engine, workspaceID, "spec.md", "text/markdown", markdown, http.StatusCreated)
	if !created {
		t.Fatal("首次上传应创建 Asset")
	}
	duplicateID, created := uploadV2Asset(t, engine, workspaceID, "renamed.md", "text/markdown", markdown, http.StatusOK)
	if created || duplicateID != goodID {
		t.Fatalf("相同内容应复用 Asset: first=%s second=%s created=%v", goodID, duplicateID, created)
	}
	badID, _ := uploadV2Asset(t, engine, workspaceID, "unsupported.xyz", "application/octet-stream", []byte("bad"), http.StatusCreated)

	assetSetID := postID(t, engine, "/api/v2/asset-sets", "asset_set", map[string]any{
		"workspace_id": workspaceID, "name": "混合产品文档", "asset_ids": []string{goodID, badID},
	})
	definitionID := postID(t, engine, "/api/v2/task-definitions", "definition", map[string]any{
		"workspace_id": workspaceID, "key": fmt.Sprintf("source_parse_%d", time.Now().UnixNano()), "name": "结构化解析", "status": "active",
		"input_ports": map[string]any{"documents": map[string]any{
			"resource_type": "asset_set", "required": true,
		}},
		"output_ports":    map[string]any{"parsed": map[string]any{"resource_type": "parsed_documents"}},
		"output_bindings": map[string]any{"parsed": "$step.parse.documents"},
		"steps": []any{map[string]any{"id": "parse", "name": "解析", "kind": "source.parse",
			"inputs":  map[string]any{"assets": "$task.documents"},
			"outputs": map[string]any{"documents": "parsed_documents"}, "config": map[string]any{}}},
	})
	taskID := postID(t, engine, "/api/v2/tasks", "task", map[string]any{
		"definition_id": definitionID, "title": "解析混合文档", "bindings": []any{map[string]any{
			"port_name": "documents", "resource_type": "asset_set", "resource_id": assetSetID,
		}},
	})
	postJSON(t, engine, http.MethodPost, "/api/v2/tasks/"+taskID+"/start", nil, http.StatusOK)
	if err := worker.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot := requestJSON(t, engine, http.MethodGet, "/api/v2/tasks/"+taskID, nil, http.StatusOK)
	data := snapshot["data"].(map[string]any)
	if data["task"].(map[string]any)["status"] != "succeeded" {
		t.Fatalf("source.parse task status=%v", data["task"])
	}
	outputs := data["outputs"].([]any)
	if len(outputs) != 1 {
		t.Fatalf("task outputs=%v", outputs)
	}
	manifestID := outputs[0].(map[string]any)["resource_id"].(string)
	manifestResponse := requestJSON(t, engine, http.MethodGet,
		"/api/v2/parsed-document-sets/"+manifestID, nil, http.StatusOK)
	manifest := manifestResponse["data"].(map[string]any)["parsed_document_set"].(map[string]any)
	if manifest["status"] != "partial" || manifest["succeeded_count"].(float64) != 1 || manifest["failed_count"].(float64) != 1 {
		t.Fatalf("partial manifest=%v", manifest)
	}
	items := manifest["items"].([]any)
	goodDocumentID := items[0].(map[string]any)["parsed_document_id"].(string)
	blocksResponse := requestJSON(t, engine, http.MethodGet,
		"/api/v2/parsed-documents/"+goodDocumentID+"/blocks", nil, http.StatusOK)
	blocks := blocksResponse["data"].(map[string]any)["parsed_document"].(map[string]any)["blocks"].([]any)
	if len(blocks) != 3 || blocks[0].(map[string]any)["block_type"] != "heading" ||
		blocks[2].(map[string]any)["block_type"] != "table" || blocks[0].(map[string]any)["page_no"].(float64) != 3 {
		t.Fatalf("structured blocks=%v", blocks)
	}
	if err := repo.RecordParsedDocumentSetItem(context.Background(), manifestID, 0,
		model.ParsedDocumentSetItem{AssetID: goodID, Status: model.ParsedDocumentFailed}); !errors.Is(err, port.ErrStaleResourceExecution) {
		t.Fatalf("旧 attempt 必须被 Manifest fencing 拒绝: %v", err)
	}

	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM tasks WHERE id = ?`, taskID).Error
		_ = db.Exec(`DELETE FROM task_definitions WHERE id = ?`, definitionID).Error
		_ = db.Exec(`DELETE FROM asset_sets WHERE id = ?`, assetSetID).Error
		_ = db.Exec(`DELETE FROM assets WHERE id IN (?, ?)`, goodID, badID).Error
	})
}

func uploadV2Asset(t *testing.T, engine http.Handler, workspaceID, filename, contentType string, content []byte, wantStatus int) (string, bool) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("workspace_id", workspaceID); err != nil {
		t.Fatal(err)
	}
	partHeader := make(textproto.MIMEHeader)
	partHeader.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename))
	partHeader.Set("Content-Type", contentType)
	part, err := writer.CreatePart(partHeader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v2/assets", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, req)
	if response.Code != wantStatus {
		t.Fatalf("upload status=%d want=%d body=%s", response.Code, wantStatus, response.Body.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	data := decoded["data"].(map[string]any)
	return data["asset"].(map[string]any)["id"].(string), data["created"].(bool)
}
