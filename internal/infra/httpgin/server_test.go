package httpgin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestLegacyRoutesAreRemoved 守卫：Legacy /api 一代路由（任务运行时、数据集管理、
// 元数据、查重、归档、概览）不得复活；产品能力一律走 /api/v2。
func TestLegacyRoutesAreRemoved(t *testing.T) {
	engine := New(Services{})
	for _, target := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/tasks"},
		{http.MethodPost, "/api/tasks/task-1/analyze"},
		{http.MethodPost, "/api/tasks/task-1/resume"},
		{http.MethodGet, "/api/overview"},
		{http.MethodPost, "/api/datasets"},
		{http.MethodPost, "/api/datasets/ds-1/search"},
		{http.MethodPost, "/api/datasets/ds-1/schema/check"},
		{http.MethodPut, "/api/datasets/ds-1/schema"},
		{http.MethodDelete, "/api/datasets/ds-1"},
		{http.MethodGet, "/api/archives"},
		{http.MethodPost, "/api/archives/task/ds-1/restore"},
		{http.MethodGet, "/api/metadata"},
		{http.MethodGet, "/api/metadata/task-types/requirement_import"},
		{http.MethodPut, "/api/metadata/schemas/requirement"},
		{http.MethodPut, "/api/metadata/workflows/requirement_import"},
		{http.MethodPost, "/api/metadata/import"},
		{http.MethodPost, "/api/match/duplicates"},
	} {
		request := httptest.NewRequest(target.method, target.path, nil)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s %s status=%d, want 404", target.method, target.path, response.Code)
		}
	}
}

// TestHealthStaysOnRootAPI 健康检查保留在 /api/health（部署探活约定，不随业务版本走）。
func TestHealthStaysOnRootAPI(t *testing.T) {
	engine := New(Services{})
	request := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("GET /api/health status=%d, want 200", response.Code)
	}
}
