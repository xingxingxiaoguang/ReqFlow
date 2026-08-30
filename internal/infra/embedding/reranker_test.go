package embedding

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRerankerUsesSiliconFlowContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/rerank" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer shared-key" {
			t.Fatalf("Authorization = %q", got)
		}
		var body struct {
			Model           string   `json:"model"`
			Query           string   `json:"query"`
			Documents       []string `json:"documents"`
			ReturnDocuments bool     `json:"return_documents"`
			TopN            int      `json:"top_n"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Model != "BAAI/bge-reranker-v2-m3" || body.Query != "Apple" ||
			len(body.Documents) != 4 || body.ReturnDocuments || body.TopN != 2 {
			t.Fatalf("body = %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"index":2,"relevance_score":0.98},{"index":0,"relevance_score":0.75}]}`))
	}))
	defer server.Close()

	reranker := NewReranker(RerankerOptions{BaseURL: server.URL + "/v1", APIKey: "shared-key",
		Model: "BAAI/bge-reranker-v2-m3"})
	results, err := reranker.Rerank(context.Background(), "Apple",
		[]string{"apple", "banana", "fruit", "vegetable"}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Index != 2 || results[0].Score != .98 {
		t.Fatalf("results = %+v", results)
	}
}

func TestRerankerRejectsInvalidResponseIndex(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"index":9,"relevance_score":1}]}`))
	}))
	defer server.Close()
	reranker := NewReranker(RerankerOptions{BaseURL: server.URL, APIKey: "key", Model: "model"})
	if _, err := reranker.Rerank(context.Background(), "q", []string{"one"}, 1); err == nil {
		t.Fatal("非法 index 应失败")
	}
}
