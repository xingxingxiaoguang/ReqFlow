package extraction

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"reqflow/internal/app/agent"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

func TestSplitExtractionBlocksNeverTruncatesLargeBlock(t *testing.T) {
	text := strings.Repeat("产", 25)
	segments := splitExtractionBlocks([]model.DocumentBlock{{ID: "block-1", Ordinal: 3,
		BlockType: model.BlockParagraph, Text: text}}, 10)
	if len(segments) != 3 {
		t.Fatalf("segments=%d", len(segments))
	}
	var rebuilt strings.Builder
	seen := map[string]bool{}
	for _, segment := range segments {
		rebuilt.WriteString(segment.Text)
		if segment.ID != "block-1" || segment.Ordinal != 3 || segment.fullText != text {
			t.Fatalf("segment lost provenance: %+v", segment)
		}
		if segment.SegmentID == "" || seen[segment.SegmentID] {
			t.Fatalf("segment id must be stable and unique: %q", segment.SegmentID)
		}
		seen[segment.SegmentID] = true
	}
	if rebuilt.String() != text {
		t.Fatal("large block was truncated")
	}
}

func TestExtractionAgentRepairsRejectedDraftAndFinishes(t *testing.T) {
	schemaJSON := json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"sku":{"type":"string"},"name":{"type":"string"}},"required":["sku","name"]}`)
	blockText := "SKU: A-100\n名称：产品 A"
	plan := plannedExtractionUnit{assetID: "asset-1", unit: model.ExtractionUnit{
		UnitKey: "unit-1", Ordinal: 0,
	}, blocks: []extractionBlock{{SegmentID: "block-1:0:22", ID: "block-1", Ordinal: 0,
		BlockType: model.BlockParagraph, Text: blockText, fullText: blockText}}}
	client := &extractionAgentScript{responses: []*port.Message{
		extractionToolMessage("c1", "list_source_blocks", `{}`, "先查看来源范围"),
		extractionToolMessage("c2", "read_source_blocks", `{"offset":0,"limit":6}`, "读取全部原文"),
		extractionToolMessage("c3", "upsert_record_drafts", `{"records":[{"draft_key":"sku:A-100","fields":{"sku":"A-100","name":"产品 A"},"field_confidence":{"sku":0.99},"source_refs":[{"block_id":"block-1","quote":"并不存在的原文"}]}]}`, "先写入候选"),
		extractionToolMessage("c4", "upsert_record_drafts", `{"records":[{"draft_key":"sku:A-100","fields":{"sku":"A-100","name":"产品 A"},"field_confidence":{"sku":0.99,"name":0.95},"source_refs":[{"block_id":"block-1","quote":"SKU: A-100"},{"block_id":"block-1","quote":"名称：产品 A"}]}]}`, "根据工具错误修正逐字引用"),
		extractionToolMessage("c5", "validate_record_drafts", `{}`, "提交前校验"),
		extractionToolMessage("c6", "finish_extraction_unit", `{"outcome":"records","summary":"提取一条产品记录"}`, "校验通过后显式完成"),
	}}
	service := &Service{llm: client, model: "test-model", maxTurns: 10}
	checkpoint := extractionCheckpoint{UnitStates: map[string]unitCheckpoint{}}
	reports := 0
	drafts, responseHash, usage, err := service.extractUnitAgent(context.Background(), model.ExtractionProfile{
		ID: "profile-1", ProfileHash: "profile-hash", RecordGranularity: "每个产品一条",
	}, model.DatasetSchemaDefinition{ID: "schema-1", JSONSchema: schemaJSON}, plan, &checkpoint,
		func(string, int) error { reports++; return nil }, "test-model")
	if err != nil {
		t.Fatalf("extractUnitAgent: %v", err)
	}
	if client.calls != 6 || usage.RequestCount != 6 || usage.InputTokens != 60 || usage.OutputTokens != 12 {
		t.Fatalf("agent usage/calls mismatch: calls=%d usage=%+v", client.calls, usage)
	}
	if reports == 0 || responseHash == "" || len(drafts) != 1 || string(drafts[0].Fields) != `{"name":"产品 A","sku":"A-100"}` {
		t.Fatalf("unexpected result: reports=%d hash=%q drafts=%+v", reports, responseHash, drafts)
	}
	state := checkpoint.UnitStates[plan.unit.UnitKey]
	if !state.State.Finished || len(state.State.Drafts) != 1 {
		t.Fatalf("agent state not finished: %+v", state.State)
	}
	var sawRecoverableError, readResultLeaked bool
	for _, message := range state.Run.Context.Messages {
		if message.Role == port.RoleToolResult && message.ToolName == "upsert_record_drafts" &&
			message.IsError && strings.Contains(message.Result, "SOURCE_QUOTE_NOT_FOUND") {
			sawRecoverableError = true
		}
	}
	if len(checkpoint.AgentRuns) != 1 || checkpoint.AgentRuns[0].Status != "succeeded" {
		t.Fatalf("live agent run missing: %+v", checkpoint.AgentRuns)
	}
	for _, tool := range checkpoint.AgentRuns[0].Tools {
		if tool.Name == "read_source_blocks" && strings.Contains(tool.Result, blockText) {
			readResultLeaked = true
		}
	}
	if !sawRecoverableError || readResultLeaked {
		t.Fatalf("repair/sanitization mismatch: repaired=%t leaked=%t", sawRecoverableError, readResultLeaked)
	}

	// Agent 已显式完成但后续资源提交失败时，重试直接复用 checkpoint，不再重复请求模型。
	resumeClient := &extractionAgentScript{}
	resumeService := &Service{llm: resumeClient, model: "test-model", maxTurns: 10}
	resumedDrafts, resumedHash, _, err := resumeService.extractUnitAgent(context.Background(), model.ExtractionProfile{
		ID: "profile-1", ProfileHash: "profile-hash", RecordGranularity: "每个产品一条",
	}, model.DatasetSchemaDefinition{ID: "schema-1", JSONSchema: schemaJSON}, plan, &checkpoint,
		func(string, int) error { return nil }, "test-model")
	if err != nil || resumeClient.calls != 0 || len(resumedDrafts) != 1 || resumedHash != responseHash {
		t.Fatalf("completed checkpoint should skip llm: calls=%d drafts=%d hash=%q err=%v",
			resumeClient.calls, len(resumedDrafts), resumedHash, err)
	}
}

func TestExtractionAgentResumesCheckpointWithoutDoubleCountingUsage(t *testing.T) {
	schemaJSON := json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"sku":{"type":"string"}},"required":["sku"]}`)
	plan := plannedExtractionUnit{assetID: "asset-1", unit: model.ExtractionUnit{UnitKey: "resume-unit"},
		blocks: []extractionBlock{{SegmentID: "block-1:0:10", ID: "block-1", Text: "SKU: A-100"}}}
	checkpoint := extractionCheckpoint{UnitStates: map[string]unitCheckpoint{}}
	profile := model.ExtractionProfile{ID: "profile-1", ProfileHash: "profile-hash"}
	schema := model.DatasetSchemaDefinition{ID: "schema-1", JSONSchema: schemaJSON}

	firstClient := &extractionAgentScript{responses: []*port.Message{
		extractionToolMessage("c1", "list_source_blocks", `{}`, "先列出来源"),
	}}
	firstService := &Service{llm: firstClient, model: "test-model", maxTurns: 1}
	_, _, firstUsage, err := firstService.extractUnitAgent(context.Background(), profile, schema,
		plan, &checkpoint, func(string, int) error { return nil }, "test-model")
	if err == nil || firstUsage.RequestCount != 1 {
		t.Fatalf("first run must stop at turn limit with accounted usage: usage=%+v err=%v", firstUsage, err)
	}
	state := checkpoint.UnitStates[plan.unit.UnitKey]
	state.Run.AccountedUsage = agent.AddUsage(state.Run.AccountedUsage, agentUsage(firstUsage))
	checkpoint.UnitStates[plan.unit.UnitKey] = state

	secondClient := &extractionAgentScript{responses: []*port.Message{
		extractionToolMessage("c2", "read_source_blocks", `{"offset":0,"limit":6}`, "继续读取"),
		extractionToolMessage("c3", "upsert_record_drafts", `{"records":[{"draft_key":"sku:A-100","fields":{"sku":"A-100"},"field_confidence":{"sku":0.95},"source_refs":[{"block_id":"block-1","quote":"SKU: A-100"}]}]}`, "写入草稿"),
		extractionToolMessage("c4", "validate_record_drafts", `{}`, "校验"),
		extractionToolMessage("c5", "finish_extraction_unit", `{"outcome":"records"}`, "完成"),
	}}
	secondService := &Service{llm: secondClient, model: "test-model", maxTurns: 4}
	drafts, _, secondUsage, err := secondService.extractUnitAgent(context.Background(), profile, schema,
		plan, &checkpoint, func(string, int) error { return nil }, "test-model")
	if err != nil || len(drafts) != 1 {
		t.Fatalf("resumed agent failed: drafts=%+v err=%v", drafts, err)
	}
	if secondUsage.RequestCount != 4 || secondUsage.InputTokens != 40 || secondUsage.OutputTokens != 8 {
		t.Fatalf("resumed usage must include only unaccounted turns: %+v", secondUsage)
	}
	if checkpoint.AgentRuns[0].RequestCount != 5 {
		t.Fatalf("live trace must retain all turns across attempts: %+v", checkpoint.AgentRuns[0])
	}
}

func TestValidateExtractionCandidateRequiresFieldConfidence(t *testing.T) {
	schemaJSON := json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"sku":{"type":"string"}},"required":["sku"]}`)
	blocks := []extractionBlock{{SegmentID: "block-1:0:10", ID: "block-1", Text: "SKU: A-100"}}
	fields := map[string]struct{}{"sku": {}}
	base := extractionCandidate{DraftKey: "sku:A-100", Fields: map[string]any{"sku": "A-100"},
		SourceRefs: []extractionSourceRef{{BlockID: "block-1", Quote: "SKU: A-100"}}}

	if code, _, _ := validateExtractionCandidate(base, fields, schemaJSON, blocks); code != "MISSING_FIELD_CONFIDENCE" {
		t.Fatalf("missing field_confidence must be rejected as recoverable feedback, got %q", code)
	}
	base.FieldConfidence = map[string]float64{"sku": 0.95}
	if code, _, _ := validateExtractionCandidate(base, fields, schemaJSON, blocks); code != "" {
		t.Fatalf("candidate with confidence must pass, got %q", code)
	}
}

func TestExtractionCandidateDraftNormalizesMissingConfidenceToObject(t *testing.T) {
	blockText := "SKU: A-100"
	plan := plannedExtractionUnit{assetID: "asset-1", blocks: []extractionBlock{{SegmentID: "block-1:0:9",
		ID: "block-1", Text: blockText, fullText: blockText}}}
	candidate := extractionCandidate{DraftKey: "sku:A-100", Fields: map[string]any{"sku": "A-100"},
		SourceRefs: []extractionSourceRef{{BlockID: "block-1", Quote: "SKU: A-100"}}}
	draft, err := extractionCandidateDraft(candidate, plan, model.ExtractionProfile{ID: "p1", ProfileHash: "h1"},
		"test-model", "prompt-hash")
	if err != nil {
		t.Fatalf("extractionCandidateDraft: %v", err)
	}
	if string(draft.FieldConfidence) != `{}` {
		t.Fatalf("missing confidence must serialize as {} (record_drafts requires jsonb object), got %s", draft.FieldConfidence)
	}
}

func TestExtractionWriteSchemaRequiresFieldConfidence(t *testing.T) {
	parameters, err := extractionAgentWriteSchema(json.RawMessage(
		`{"type":"object","properties":{"sku":{"type":"string"}},"required":["sku"]}`))
	if err != nil {
		t.Fatalf("extractionAgentWriteSchema: %v", err)
	}
	var root struct {
		Properties struct {
			Records struct {
				Items struct {
					Required []string `json:"required"`
				} `json:"items"`
			} `json:"records"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(parameters, &root); err != nil {
		t.Fatalf("unmarshal write schema: %v", err)
	}
	required := map[string]bool{}
	for _, name := range root.Properties.Records.Items.Required {
		required[name] = true
	}
	for _, name := range []string{"draft_key", "fields", "field_confidence", "source_refs"} {
		if !required[name] {
			t.Fatalf("write schema must require %s, got %v", name, root.Properties.Records.Items.Required)
		}
	}
}

type extractionAgentScript struct {
	responses []*port.Message
	calls     int
}

func (client *extractionAgentScript) Stream(_ context.Context, _ *port.Context,
	onEvent func(port.AssistantEvent)) (*port.Message, error) {
	if client.calls >= len(client.responses) {
		return nil, fmt.Errorf("extraction agent script exhausted at turn %d", client.calls+1)
	}
	message := client.responses[client.calls]
	client.calls++
	if onEvent != nil {
		for _, block := range message.Content {
			switch block.Type {
			case port.BlockThinking:
				onEvent(port.AssistantEvent{Type: port.EventThinkingDelta, Delta: block.Thinking, Message: message})
			case port.BlockText:
				onEvent(port.AssistantEvent{Type: port.EventTextDelta, Delta: block.Text, Message: message})
			}
		}
	}
	return message, nil
}

func (client *extractionAgentScript) Complete(ctx context.Context, c *port.Context) (*port.Message, error) {
	return client.Stream(ctx, c, nil)
}

func (*extractionAgentScript) Ping(context.Context) error { return nil }

func extractionToolMessage(id, name, arguments, thinking string) *port.Message {
	call := port.ToolCall{ID: id, Name: name, Arguments: json.RawMessage(arguments)}
	return &port.Message{Role: port.RoleAssistant, StopReason: port.StopReasonToolUse,
		Usage: port.Usage{Input: 10, Output: 2}, Content: []port.Block{
			{Type: port.BlockThinking, Thinking: thinking},
			{Type: port.BlockToolCall, ToolCall: &call},
		}}
}
