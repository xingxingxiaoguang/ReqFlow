package workflow_test

import (
	"encoding/json"
	"testing"

	domain "reqflow/internal/domain/workflow"
)

func TestCompileDataContractIsStableAndClosed(t *testing.T) {
	contract := domain.DataContract{Fields: []domain.FieldContract{
		{Key: "sku", Label: "SKU", Type: domain.FieldString, Required: true},
		{Key: "released_at", Type: domain.FieldDateTime},
		{Key: "tags", Type: domain.FieldArray, Items: &domain.FieldContract{Key: "item", Type: domain.FieldString}},
	}}
	first, firstHash, err := domain.CompileDataContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	second, secondHash, err := domain.CompileDataContract(contract)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) || firstHash == "" || firstHash != secondHash {
		t.Fatalf("contract compilation is not stable: %s %s", firstHash, secondHash)
	}
	var root map[string]any
	if err := json.Unmarshal(first, &root); err != nil {
		t.Fatal(err)
	}
	if root["additionalProperties"] != false {
		t.Fatalf("compiled schema must be closed: %s", first)
	}
}
