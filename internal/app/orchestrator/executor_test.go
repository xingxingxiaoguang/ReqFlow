package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"reqflow/internal/domain/model"
)

func TestRegistryRejectsDuplicateAndHumanExecutors(t *testing.T) {
	_, err := NewRegistry(testExecutor{kind: model.StepKindLLMExtract}, testExecutor{kind: model.StepKindLLMExtract})
	if err == nil {
		t.Fatal("重复 Kind 必须拒绝")
	}
	_, err = NewRegistry(testExecutor{kind: model.StepKindHumanReview})
	if err == nil {
		t.Fatal("human.review 不能注册为普通 Executor")
	}
}

func TestRegistryValidatesEveryRepeatedKindStep(t *testing.T) {
	executor := &validatingExecutor{kind: model.StepKindLLMExtract, rejectStep: "refine"}
	registry, err := NewRegistry(executor)
	if err != nil {
		t.Fatal(err)
	}
	definition := model.TaskDefinition{Steps: []model.StepDefinition{
		{ID: "extract", Kind: model.StepKindLLMExtract},
		{ID: "refine", Kind: model.StepKindLLMExtract},
		{ID: "review", Kind: model.StepKindHumanReview},
	}}
	err = registry.ValidateDefinition(context.Background(), definition)
	if err == nil || !errors.Is(err, errRejectedDefinition) {
		t.Fatalf("每个重复 Kind 步骤都应单独校验，got %v", err)
	}
	if executor.validated != 2 {
		t.Fatalf("validated=%d, want 2", executor.validated)
	}
}

var errRejectedDefinition = errors.New("rejected definition")

type validatingExecutor struct {
	kind       model.StepKind
	rejectStep string
	validated  int
}

func (e *validatingExecutor) Kind() model.StepKind { return e.kind }
func (e *validatingExecutor) ValidateDefinition(_ context.Context, def model.StepDefinition) error {
	e.validated++
	if def.ID == e.rejectStep {
		return errRejectedDefinition
	}
	return nil
}
func (*validatingExecutor) Execute(context.Context, StepRunContext) (StepResult, error) {
	return StepResult{}, nil
}
func (*validatingExecutor) Resume(context.Context, StepRunContext, json.RawMessage) (StepResult, error) {
	return StepResult{}, nil
}
