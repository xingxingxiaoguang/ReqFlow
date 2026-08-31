package httpgin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLegacyTaskRuntimeRoutesAreRemoved(t *testing.T) {
	engine := New(Services{})
	for _, target := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/tasks"},
		{http.MethodPost, "/api/tasks/task-1/analyze"},
		{http.MethodPost, "/api/tasks/task-1/resume"},
	} {
		request := httptest.NewRequest(target.method, target.path, nil)
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s %s status=%d, want 404", target.method, target.path, response.Code)
		}
	}
}
