package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"reqflow/internal/app/agent"
	"reqflow/internal/domain/model"
	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

type analysisMemoryRepo struct {
	result        *model.AnalysisResult
	failureReason string
}

func (r *analysisMemoryRepo) BeginAnalysisResult(_ context.Context, result *model.AnalysisResult, attempt int) (*model.AnalysisResult, error) {
	stored := *result
	stored.ID, stored.Status, stored.ProducerAttempt = "result-1", model.AnalysisResultRunning, attempt
	r.result = &stored
	return &stored, nil
}
func (r *analysisMemoryRepo) GetAnalysisResult(context.Context, string) (*model.AnalysisResult, error) {
	if r.result == nil {
		return nil, errors.New("not found")
	}
	return r.result, nil
}
func (*analysisMemoryRepo) CompleteAnalysisResult(context.Context, *model.AnalysisResult, int) error {
	return errors.New("not implemented")
}
func (r *analysisMemoryRepo) FailAnalysisResult(_ context.Context, _, _ string, _ int, message string) error {
	r.failureReason = message
	return nil
}

func TestAnalyzePersistsFailureWhenModelResolutionFails(t *testing.T) {
	repo := &analysisMemoryRepo{}
	service := &Service{repo: repo}
	_, err := service.Analyze(context.Background(), RunInput{WorkflowRunID: "run-1", NodeRunID: "node-1",
		ProducerAttempt: 1, Instruction: "生成报告", OutputContract: domain.OutputContract{Fields: []domain.FieldContract{{
			Key: "report", Label: "报告", Type: domain.FieldString, Required: true,
		}}}})
	if err == nil || repo.failureReason == "" {
		t.Fatalf("模型配置失败必须终结 AnalysisResult: err=%v failure=%q", err, repo.failureReason)
	}
}
func (*analysisMemoryRepo) CreateArtifactForNode(context.Context, *model.Artifact, int) (*model.Artifact, error) {
	return nil, errors.New("not implemented")
}
func (*analysisMemoryRepo) GetArtifact(context.Context, string) (*model.Artifact, error) {
	return nil, errors.New("not implemented")
}
func (*analysisMemoryRepo) ListArtifacts(context.Context, string, string, int) ([]model.Artifact, error) {
	return nil, nil
}

func TestSubmitResultToolReturnsSchemaErrorsToAgent(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","additionalProperties":false,"required":["report"],"properties":{"report":{"type":"string"}}}`)
	state := &resultState{}
	tool := &submitResultTool{schema: schema, state: state}
	invalid := tool.Execute(context.Background(), port.ToolCall{Arguments: json.RawMessage(`{"report":"ok","extra":true}`)}, nil)
	if !invalid.IsError || invalid.Terminate || state.Submitted {
		t.Fatalf("Schema 错误必须可纠正且不能终止: %+v state=%+v", invalid, state)
	}
	valid := tool.Execute(context.Background(), port.ToolCall{Arguments: json.RawMessage(`{"report":"# 方案"}`)}, nil)
	if valid.IsError || !valid.Terminate || !state.Submitted {
		t.Fatalf("合法结果应显式完成: %+v state=%+v", valid, state)
	}
	if string(state.Output) != `{"report":"# 方案"}` {
		t.Fatalf("unexpected output: %s", state.Output)
	}
}

func TestAnalysisAgentRepairsInvalidSubmission(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","additionalProperties":false,"required":["report"],"properties":{"report":{"type":"string"}}}`)
	state := &resultState{}
	client := &analysisScriptClient{responses: []*port.Message{
		analysisToolMessage("c1", `{"report":1}`),
		analysisToolMessage("c2", `{"report":"已修正"}`),
	}}
	cc := &port.Context{Messages: []port.Message{port.NewUserMessage("开始分析")}}
	_, err := agent.New(client, []agent.Tool{&submitResultTool{schema: schema, state: state}}, agent.Config{
		MaxIterations: 3, RequireToolTermination: true,
	}).Run(context.Background(), cc, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if client.calls != 2 || !state.Submitted || string(state.Output) != `{"report":"已修正"}` {
		t.Fatalf("calls=%d state=%+v", client.calls, state)
	}
	if len(cc.Messages) < 3 || cc.Messages[2].Role != port.RoleToolResult || !cc.Messages[2].IsError {
		t.Fatalf("首个 Schema 错误未作为工具回执进入上下文: %+v", cc.Messages)
	}
}

type analysisScriptClient struct {
	responses []*port.Message
	calls     int
}

func (c *analysisScriptClient) Stream(context.Context, *port.Context, func(port.AssistantEvent)) (*port.Message, error) {
	if c.calls >= len(c.responses) {
		return nil, fmt.Errorf("analysis script exhausted")
	}
	message := c.responses[c.calls]
	c.calls++
	return message, nil
}

func (c *analysisScriptClient) Complete(ctx context.Context, cc *port.Context) (*port.Message, error) {
	return c.Stream(ctx, cc, nil)
}

func (*analysisScriptClient) Ping(context.Context) error { return nil }

func analysisToolMessage(id, arguments string) *port.Message {
	call := port.ToolCall{ID: id, Name: "submit_analysis_result", Arguments: json.RawMessage(arguments)}
	return &port.Message{Role: port.RoleAssistant, StopReason: port.StopReasonToolUse,
		Content: []port.Block{{Type: port.BlockToolCall, ToolCall: &call}}}
}
