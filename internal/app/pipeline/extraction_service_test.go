package pipeline

import (
	"encoding/json"
	"strings"
	"testing"

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
	for _, segment := range segments {
		rebuilt.WriteString(segment.Text)
		if segment.ID != "block-1" || segment.Ordinal != 3 || segment.fullText != text {
			t.Fatalf("segment lost provenance: %+v", segment)
		}
	}
	if rebuilt.String() != text {
		t.Fatal("large block was truncated")
	}
}

func TestStrictExtractionPayloadAcceptsOneToolCallAndRejectsRepairCases(t *testing.T) {
	valid := &port.Message{StopReason: port.StopReasonToolUse, Content: []port.Block{{
		Type: port.BlockToolCall, ToolCall: &port.ToolCall{Name: emitRecordsToolName,
			Arguments: json.RawMessage(`{"records":[]}`)},
	}}}
	raw, err := strictExtractionPayload(valid)
	if err != nil || string(raw) != `{"records":[]}` {
		t.Fatalf("valid tool payload: raw=%s err=%v", raw, err)
	}

	unknown := []byte(`{"records":[],"explanation":"ignore"}`)
	if _, err := decodeExtractionPayload(unknown); err == nil {
		t.Fatal("unknown response keys must be rejected")
	}
	fenced := &port.Message{StopReason: port.StopReasonStop,
		Content: []port.Block{{Type: port.BlockText, Text: "```json\n{\"records\":[]}\n```"}}}
	if raw, err := strictExtractionPayload(fenced); err != nil {
		t.Fatal(err)
	} else if _, err := decodeExtractionPayload(raw); err == nil {
		t.Fatal("markdown-fenced JSON must not be repaired")
	}
	multiple := &port.Message{Content: []port.Block{
		{Type: port.BlockToolCall, ToolCall: &port.ToolCall{Name: emitRecordsToolName, Arguments: json.RawMessage(`{"records":[]}`)}},
		{Type: port.BlockToolCall, ToolCall: &port.ToolCall{Name: emitRecordsToolName, Arguments: json.RawMessage(`{"records":[]}`)}},
	}}
	if _, err := strictExtractionPayload(multiple); err == nil {
		t.Fatal("multiple tool calls must be rejected")
	}
}
