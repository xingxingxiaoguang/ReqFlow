package pipeline

import (
	"context"
	"encoding/json"
	"testing"

	"reqflow/internal/domain/model"
)

func TestSourceParseExecutorValidatesStablePortContract(t *testing.T) {
	executor := &SourceParseExecutor{}
	valid := model.StepDefinition{ID: "parse", Kind: model.StepKindSourceParse,
		Inputs:  map[string]string{"assets": "$task.documents"},
		Outputs: map[string]model.ResourceType{"documents": model.ResourceParsedDocuments},
		Config:  json.RawMessage(`{}`)}
	if err := executor.ValidateDefinition(context.Background(), valid); err != nil {
		t.Fatalf("valid source.parse definition: %v", err)
	}
	invalid := valid
	invalid.Outputs = map[string]model.ResourceType{"text": model.ResourceArtifact}
	if err := executor.ValidateDefinition(context.Background(), invalid); err == nil {
		t.Fatal("source.parse should reject non-manifest output")
	}
	invalid = valid
	invalid.Config = json.RawMessage(`{"parser":"legacy"}`)
	if err := executor.ValidateDefinition(context.Background(), invalid); err == nil {
		t.Fatal("source.parse should reject per-definition parser identity")
	}
}

func TestParsedContentHashCanonicalizesMetadata(t *testing.T) {
	left := []model.DocumentBlock{{BlockType: model.BlockParagraph, Text: "same", Metadata: `{"b":2,"a":1}`}}
	right := []model.DocumentBlock{{BlockType: model.BlockParagraph, Text: "same", Metadata: `{"a":1,"b":2}`}}
	leftHash, err := parsedContentHash(left)
	if err != nil {
		t.Fatal(err)
	}
	rightHash, err := parsedContentHash(right)
	if err != nil {
		t.Fatal(err)
	}
	if leftHash != rightHash {
		t.Fatalf("metadata key order changed content hash: %s != %s", leftHash, rightHash)
	}
}
