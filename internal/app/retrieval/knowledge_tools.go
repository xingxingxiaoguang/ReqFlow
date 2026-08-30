package retrieval

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"

	"reqflow/internal/app/agent"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

var knowledgeSourceNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

type KnowledgeSource struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	SnapshotID  string `json:"retrieval_snapshot_id,omitempty"`
}

// KnowledgeScope 是一次 Agent 运行可见知识源的完整白名单。工具参数只接受 Name，
// SnapshotID 永远由服务端 Scope 解析，阻止模型越权猜测任意资源 ID。
type KnowledgeScope struct {
	ID          string                     `json:"id"`
	WorkspaceID string                     `json:"workspace_id"`
	Sources     map[string]KnowledgeSource `json:"sources"`
}

func (s *Service) BuildKnowledgeTools(ctx context.Context, scope KnowledgeScope) ([]agent.Tool, error) {
	scope.ID = strings.TrimSpace(scope.ID)
	scope.WorkspaceID = strings.TrimSpace(scope.WorkspaceID)
	if scope.WorkspaceID == "" {
		scope.WorkspaceID = "default"
	}
	if scope.ID == "" || len(scope.Sources) == 0 || len(scope.Sources) > 32 {
		return nil, fmt.Errorf("KnowledgeScope 必须包含 id 和 1..32 个知识源")
	}
	normalized := make(map[string]KnowledgeSource, len(scope.Sources))
	for key, source := range scope.Sources {
		name := strings.TrimSpace(source.Name)
		if name == "" {
			name = strings.TrimSpace(key)
		}
		if name != strings.TrimSpace(key) || !knowledgeSourceNamePattern.MatchString(name) {
			return nil, fmt.Errorf("知识源逻辑名 %q 非法或与 map key 不一致", name)
		}
		snapshot, err := s.repo.GetRetrievalSnapshot(ctx, strings.TrimSpace(source.SnapshotID))
		if err != nil || snapshot.Status != model.RetrievalSnapshotActive {
			return nil, fmt.Errorf("知识源 %s 的 RetrievalSnapshot 不存在或未激活", name)
		}
		dataset, err := s.repo.GetAppendDataset(ctx, snapshot.DatasetID)
		if err != nil || dataset.WorkspaceID != scope.WorkspaceID {
			return nil, fmt.Errorf("知识源 %s 不属于 KnowledgeScope workspace", name)
		}
		source.Name, source.Description, source.SnapshotID = name,
			strings.TrimSpace(source.Description), snapshot.ID
		normalized[name] = source
	}
	scope.Sources = normalized
	base := knowledgeToolBase{service: s, scope: scope}
	return []agent.Tool{&listKnowledgeSourcesTool{base}, &searchKnowledgeTool{base}, &getKnowledgeItemTool{base}}, nil
}

func (s *Service) GetKnowledgeItem(ctx context.Context, snapshotID, itemID string) (*model.DatasetItem, error) {
	snapshot, err := s.repo.GetRetrievalSnapshot(ctx, snapshotID)
	if err != nil {
		return nil, err
	}
	if snapshot.Status != model.RetrievalSnapshotActive {
		return nil, fmt.Errorf("RetrievalSnapshot 未激活")
	}
	items, err := s.repo.GetDatasetItemsByIDs(ctx, snapshot.DatasetID, snapshot.SourceSeq, []string{itemID})
	if err != nil {
		return nil, err
	}
	if len(items) != 1 {
		return nil, fmt.Errorf("知识条目不存在或超出 Snapshot 边界")
	}
	return &items[0], nil
}

type knowledgeToolBase struct {
	service *Service
	scope   KnowledgeScope
}

func (b knowledgeToolBase) source(name string) (KnowledgeSource, error) {
	source, ok := b.scope.Sources[strings.TrimSpace(name)]
	if !ok {
		return KnowledgeSource{}, fmt.Errorf("知识源 %q 不在当前 KnowledgeScope", name)
	}
	return source, nil
}

func (b knowledgeToolBase) audit(ctx context.Context, toolName, sourceName string, request json.RawMessage,
	started time.Time, count int, executionErr error) {
	errorMessage := ""
	if executionErr != nil {
		errorMessage = truncateError(executionErr.Error(), 2000)
	}
	_ = b.service.repo.AppendKnowledgeToolAudit(context.WithoutCancel(ctx), port.KnowledgeToolAudit{
		ScopeID: b.scope.ID, WorkspaceID: b.scope.WorkspaceID, ToolName: toolName,
		SourceName: sourceName, Request: request, ResultCount: count,
		LatencyMS: time.Since(started).Milliseconds(), ErrorMessage: errorMessage,
	})
}

type listKnowledgeSourcesTool struct{ knowledgeToolBase }

func (*listKnowledgeSourcesTool) Spec() port.ToolSpec {
	return port.ToolSpec{Name: "list_knowledge_sources", Description: "列出当前任务授权访问的逻辑知识源。",
		Parameters: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)}
}

func (t *listKnowledgeSourcesTool) Execute(ctx context.Context, call port.ToolCall, _ func(string)) agent.ToolOutput {
	started := time.Now()
	if err := decodeStrictToolArgs(call.Arguments, &struct{}{}); err != nil {
		t.audit(ctx, call.Name, "", call.Arguments, started, 0, err)
		return agent.ToolOutput{Output: err.Error(), IsError: true}
	}
	sources := make([]KnowledgeSource, 0, len(t.scope.Sources))
	for _, source := range t.scope.Sources {
		// Snapshot ID 是服务端内部授权细节，不暴露给 Agent。
		source.SnapshotID = ""
		sources = append(sources, source)
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Name < sources[j].Name })
	out, _ := json.Marshal(map[string]any{"sources": sources})
	t.audit(ctx, call.Name, "", call.Arguments, started, len(sources), nil)
	return agent.ToolOutput{Output: string(out), Details: fmt.Sprintf("列出 %d 个知识源", len(sources))}
}

func (*listKnowledgeSourcesTool) PromptSnippet() string {
	return "list_knowledge_sources：列出当前任务授权的知识源逻辑名"
}
func (*listKnowledgeSourcesTool) PromptGuidelines() []string {
	return []string{"首次查询知识前先确认可用 source 名称；不得猜测或传入 Dataset/Index ID"}
}

type searchKnowledgeTool struct{ knowledgeToolBase }

func (*searchKnowledgeTool) Spec() port.ToolSpec {
	return port.ToolSpec{Name: "search_knowledge",
		Description: "在授权知识源内进行可调权重的 BM25 + 语义混合检索，可选择 SiliconFlow rerank。",
		Parameters: json.RawMessage(`{"type":"object","properties":{
			"source":{"type":"string"},"query":{"type":"string"},
			"filters":{"type":"object","additionalProperties":{"type":"array","items":{"type":"string"}}},
			"mode":{"type":"string","enum":["lexical","semantic","hybrid"]},
			"lexical_weight":{"type":"number","minimum":0,"maximum":100},
			"semantic_weight":{"type":"number","minimum":0,"maximum":100},
			"score_threshold":{"type":"number","minimum":0,"maximum":1},
			"recall_limit":{"type":"integer","minimum":1,"maximum":1000},
			"top_k":{"type":"integer","minimum":1,"maximum":200},
			"rerank_enabled":{"type":"boolean"},
			"rerank_top_n":{"type":"integer","minimum":1,"maximum":200}
		},"required":["source","query"],"additionalProperties":false}`)}
}

type searchKnowledgeArgs struct {
	Source         string                    `json:"source"`
	Query          string                    `json:"query"`
	Filters        map[string][]string       `json:"filters"`
	Mode           model.RetrievalSearchMode `json:"mode"`
	LexicalWeight  float64                   `json:"lexical_weight"`
	SemanticWeight float64                   `json:"semantic_weight"`
	ScoreThreshold float64                   `json:"score_threshold"`
	RecallLimit    int                       `json:"recall_limit"`
	TopK           int                       `json:"top_k"`
	RerankEnabled  bool                      `json:"rerank_enabled"`
	RerankTopN     int                       `json:"rerank_top_n"`
}

func (t *searchKnowledgeTool) Execute(ctx context.Context, call port.ToolCall, _ func(string)) agent.ToolOutput {
	started := time.Now()
	var args searchKnowledgeArgs
	if err := decodeStrictToolArgs(call.Arguments, &args); err != nil {
		t.audit(ctx, call.Name, args.Source, call.Arguments, started, 0, err)
		return agent.ToolOutput{Output: "参数非法: " + err.Error(), IsError: true}
	}
	source, err := t.source(args.Source)
	if err != nil {
		t.audit(ctx, call.Name, args.Source, call.Arguments, started, 0, err)
		return agent.ToolOutput{Output: err.Error(), IsError: true}
	}
	response, err := t.service.Search(ctx, SearchRequest{RetrievalSnapshotID: source.SnapshotID,
		Query: args.Query, Filters: args.Filters, Strategy: model.RetrievalSearchStrategy{
			Mode: args.Mode, LexicalWeight: args.LexicalWeight, SemanticWeight: args.SemanticWeight,
			ScoreThreshold: args.ScoreThreshold, RecallLimit: args.RecallLimit, TopK: args.TopK,
			RerankEnabled: args.RerankEnabled, RerankTopN: args.RerankTopN,
		}})
	if err != nil {
		t.audit(ctx, call.Name, source.Name, call.Arguments, started, 0, err)
		return agent.ToolOutput{Output: err.Error(), IsError: true}
	}
	out, _ := json.Marshal(map[string]any{"source": source.Name, "hits": response.Hits,
		"strategy": response.Strategy, "took_ms": response.TookMS})
	t.audit(ctx, call.Name, source.Name, call.Arguments, started, len(response.Hits), nil)
	return agent.ToolOutput{Output: string(out), Details: fmt.Sprintf("%s 命中 %d 条", source.Name, len(response.Hits))}
}

func (*searchKnowledgeTool) PromptSnippet() string {
	return "search_knowledge：在授权知识源内执行 BM25/语义/混合检索，可调权重、阈值、召回数和 rerank"
}
func (*searchKnowledgeTool) PromptGuidelines() []string {
	return []string{
		"精确术语、编号优先提高 lexical_weight；自然语言意图优先提高 semantic_weight",
		"先用较大 recall_limit 召回，再用 rerank_enabled=true 与 rerank_top_n 收敛高质量证据",
		"引用结论时保留命中中的 dataset_item_id 和 provenance，不得编造来源",
	}
}

type getKnowledgeItemTool struct{ knowledgeToolBase }

func (*getKnowledgeItemTool) Spec() port.ToolSpec {
	return port.ToolSpec{Name: "get_knowledge_item", Description: "读取授权知识源中一个检索命中的完整条目及 provenance。",
		Parameters: json.RawMessage(`{"type":"object","properties":{"source":{"type":"string"},"item_id":{"type":"string"}},"required":["source","item_id"],"additionalProperties":false}`)}
}

func (t *getKnowledgeItemTool) Execute(ctx context.Context, call port.ToolCall, _ func(string)) agent.ToolOutput {
	started := time.Now()
	var args struct {
		Source string `json:"source"`
		ItemID string `json:"item_id"`
	}
	if err := decodeStrictToolArgs(call.Arguments, &args); err != nil {
		t.audit(ctx, call.Name, args.Source, call.Arguments, started, 0, err)
		return agent.ToolOutput{Output: "参数非法: " + err.Error(), IsError: true}
	}
	source, err := t.source(args.Source)
	if err != nil {
		t.audit(ctx, call.Name, args.Source, call.Arguments, started, 0, err)
		return agent.ToolOutput{Output: err.Error(), IsError: true}
	}
	item, err := t.service.GetKnowledgeItem(ctx, source.SnapshotID, strings.TrimSpace(args.ItemID))
	if err != nil {
		t.audit(ctx, call.Name, source.Name, call.Arguments, started, 0, err)
		return agent.ToolOutput{Output: err.Error(), IsError: true}
	}
	out, _ := json.Marshal(map[string]any{"source": source.Name, "dataset_item_id": item.ID,
		"commit_seq": item.CommitSeq, "fields": json.RawMessage(item.Fields),
		"provenance": json.RawMessage(item.Provenance)})
	t.audit(ctx, call.Name, source.Name, call.Arguments, started, 1, nil)
	return agent.ToolOutput{Output: string(out), Details: "读取知识条目 " + item.ID}
}

func (*getKnowledgeItemTool) PromptSnippet() string {
	return "get_knowledge_item：按 search_knowledge 命中的 item_id 读取完整字段和来源链"
}
func (*getKnowledgeItemTool) PromptGuidelines() []string {
	return []string{"只能读取 search_knowledge 已返回且属于同一 source 的 item_id"}
}

func decodeStrictToolArgs(raw json.RawMessage, dst any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("只能包含一个 JSON object")
		}
		return err
	}
	return nil
}
