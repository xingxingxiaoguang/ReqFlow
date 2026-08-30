//go:build integration

package opensearch

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"

	"reqflow/internal/port"
)

func TestIntegrationOpenSearchBM25BuildCountFilterAndSearch(t *testing.T) {
	baseURL := os.Getenv("REQFLOW_TEST_OPENSEARCH_URL")
	if baseURL == "" {
		baseURL = "http://127.0.0.1:9200"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	probe, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response, err := http.DefaultClient.Do(probe); err != nil {
		t.Skipf("OpenSearch 不可用: %v", err)
	} else {
		_ = response.Body.Close()
	}

	client := New(Options{BaseURL: baseURL, IndexPrefix: "reqflow-it", Timeout: 10 * time.Second})
	ref := "retrieval-" + uuid.NewString()
	physical := client.PhysicalIndex(ref)
	t.Cleanup(func() {
		request, _ := http.NewRequest(http.MethodDelete, fmt.Sprintf("%s/%s", baseURL, physical), nil)
		if response, err := http.DefaultClient.Do(request); err == nil {
			_ = response.Body.Close()
		}
	})
	build := port.LexicalBuildRequest{IndexRef: ref, Analyzer: "standard",
		Fields: map[string]float64{"title": 3, "definition": 1}, Filters: []string{"module"},
		Documents: []port.LexicalDocument{
			{DatasetItemID: uuid.NewString(), SourceSeq: 1,
				Fields:  map[string]any{"title": "Apple thermal shutdown", "definition": "automatic protection"},
				Filters: map[string]any{"module": "power"}},
			{DatasetItemID: uuid.NewString(), SourceSeq: 2,
				Fields:  map[string]any{"title": "Banana report", "definition": "export data"},
				Filters: map[string]any{"module": "report"}},
		}}
	if err := client.Build(ctx, build); err != nil {
		t.Fatal(err)
	}
	count, err := client.Count(ctx, ref, 1)
	if err != nil || count != 1 {
		t.Fatalf("source_seq count=%d err=%v", count, err)
	}
	hits, err := client.Search(ctx, port.LexicalSearchRequest{IndexRef: ref, Query: "Apple shutdown",
		Fields: build.Fields, Filters: map[string][]string{"module": {"power"}}, SourceSeq: 2, Limit: 10})
	if err != nil || len(hits) != 1 || hits[0].DatasetItemID != build.Documents[0].DatasetItemID || hits[0].Score <= 0 {
		t.Fatalf("BM25 search hits=%+v err=%v", hits, err)
	}
}
