package pipeline

import (
	"context"
	"encoding/json"
	"testing"

	"reqflow/internal/domain/model"
)

func TestCleaningExecutorsValidateStablePortContracts(t *testing.T) {
	transform := &DataTransformExecutor{}
	transformDef := model.StepDefinition{ID: "transform", Kind: model.StepKindDataTransform,
		Inputs:  map[string]string{"drafts": "$step.extract.drafts"},
		Outputs: map[string]model.ResourceType{"records": model.ResourceTransformedRecords}, Config: json.RawMessage(`{}`)}
	if err := transform.ValidateDefinition(context.Background(), transformDef); err != nil {
		t.Fatal(err)
	}
	invalidTransform := transformDef
	invalidTransform.Outputs = map[string]model.ResourceType{"records": model.ResourceApprovedRecords}
	if err := transform.ValidateDefinition(context.Background(), invalidTransform); err == nil {
		t.Fatal("data.transform accepted wrong output resource")
	}

	validate := &DataValidateExecutor{}
	validateDef := model.StepDefinition{ID: "validate", Kind: model.StepKindDataValidate,
		Inputs:  map[string]string{"records": "$step.transform.records", "dataset": "$task.target"},
		Outputs: map[string]model.ResourceType{"validation": model.ResourceValidationResults}, Config: json.RawMessage(`{}`)}
	if err := validate.ValidateDefinition(context.Background(), validateDef); err != nil {
		t.Fatal(err)
	}
	validateDef.Config = json.RawMessage(`{"unsafe":true}`)
	if err := validate.ValidateDefinition(context.Background(), validateDef); err == nil {
		t.Fatal("data.validate accepted undeclared config")
	}
}
