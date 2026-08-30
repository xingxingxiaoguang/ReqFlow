package opensearch

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"reqflow/internal/port"
)

func TestClientBuildCountAndWeightedSearch(t *testing.T) {
	var mu sync.Mutex
	created := false
	handlerErrors := make(chan string, 8)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		fail := func(format string, args ...any) {
			handlerErrors <- fmt.Sprintf(format, args...)
			http.Error(w, "test handler assertion failed", http.StatusInternalServerError)
		}
		switch {
		case r.Method == http.MethodHead:
			if created {
				w.WriteHeader(http.StatusOK)
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		case r.Method == http.MethodPut:
			created = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"acknowledged":true}`))
		case r.URL.Path == "/_bulk":
			scanner := bufio.NewScanner(r.Body)
			lines := 0
			for scanner.Scan() {
				lines++
			}
			if lines != 2 {
				fail("bulk lines = %d", lines)
				return
			}
			_, _ = w.Write([]byte(`{"errors":false,"items":[{"index":{"status":201}}]}`))
		case strings.HasSuffix(r.URL.Path, "/_count"):
			_, _ = w.Write([]byte(`{"count":1}`))
		case strings.HasSuffix(r.URL.Path, "/_search"):
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				fail("decode search body: %v", err)
				return
			}
			raw, _ := json.Marshal(body)
			if !strings.Contains(string(raw), "fields.title^3") || !strings.Contains(string(raw), "filters.module") {
				fail("search body 未携带权重/过滤: %s", raw)
				return
			}
			_, _ = w.Write([]byte(`{"hits":{"hits":[{"_score":8.5,"_source":{"dataset_item_id":"item-1"}}]}}`))
		default:
			fail("unexpected request: %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()

	client := New(Options{BaseURL: server.URL, IndexPrefix: "reqflow-test"})
	err := client.Build(context.Background(), port.LexicalBuildRequest{IndexRef: "logical-index",
		Analyzer: "standard", Fields: map[string]float64{"title": 3}, Filters: []string{"module"},
		Documents: []port.LexicalDocument{{DatasetItemID: "item-1", SourceSeq: 1,
			Fields: map[string]any{"title": "Apple"}, Filters: map[string]any{"module": "fruit"}}}})
	if err != nil {
		t.Fatal(err)
	}
	count, err := client.Count(context.Background(), "logical-index", 1)
	if err != nil || count != 1 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	hits, err := client.Search(context.Background(), port.LexicalSearchRequest{IndexRef: "logical-index",
		Query: "Apple", Fields: map[string]float64{"title": 3},
		Filters: map[string][]string{"module": {"fruit"}}, SourceSeq: 1, Limit: 10})
	if err != nil || len(hits) != 1 || hits[0].DatasetItemID != "item-1" || hits[0].Rank != 1 {
		t.Fatalf("hits=%+v err=%v", hits, err)
	}
	select {
	case handlerErr := <-handlerErrors:
		t.Fatal(handlerErr)
	default:
	}
}
