package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"reqflow/internal/app/agent"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

type analysisMemoryRepo struct {
	profiles map[string]model.AnalysisProfile
}

func (r *analysisMemoryRepo) CreateAnalysisProfile(_ context.Context, profile *model.AnalysisProfile) error {
	if r.profiles == nil {
		r.profiles = map[string]model.AnalysisProfile{}
	}
	profile.ID = "profile-1"
	r.profiles[profile.ID] = *profile
	return nil
}
func (r *analysisMemoryRepo) GetAnalysisProfile(_ context.Context, id string) (*model.AnalysisProfile, error) {
	profile, ok := r.profiles[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return &profile, nil
}
func (r *analysisMemoryRepo) ListAnalysisProfiles(context.Context, string, int) ([]model.AnalysisProfile, error) {
	return nil, nil
}
func (*analysisMemoryRepo) BeginAnalysisResult(context.Context, *model.AnalysisResult, int) (*model.AnalysisResult, error) {
	return nil, errors.New("not implemented")
}
func (*analysisMemoryRepo) GetAnalysisResult(context.Context, string) (*model.AnalysisResult, error) {
	return nil, errors.New("not implemented")
}
func (*analysisMemoryRepo) CompleteAnalysisResult(context.Context, *model.AnalysisResult, int) error {
	return errors.New("not implemented")
}
func (*analysisMemoryRepo) FailAnalysisResult(context.Context, string, string, int, string) error {
	return errors.New("not implemented")
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

func TestCreateProfileNormalizesAndHashesContract(t *testing.T) {
	repo := &analysisMemoryRepo{}
	service := &Service{repo: repo}
	schema := json.RawMessage(`{
		"type":"object","required":["report"],"properties":{"report":{"type":"string"}}
	}`)
	first, err := service.CreateProfile(context.Background(), CreateProfileInput{
		Name: "  产品方案  ", Instruction: "  生成方案  ", OutputSchema: schema,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.WorkspaceID != "default" || first.Name != "产品方案" || first.Instruction != "生成方案" {
		t.Fatalf("Profile 未归一化: %+v", first)
	}
	if first.ProfileHash == "" || !json.Valid(first.OutputSchema) {
		t.Fatalf("Profile 合同未固化: %+v", first)
	}
	second, err := service.CreateProfile(context.Background(), CreateProfileInput{
		Name: "另一个展示名", Instruction: "生成方案", OutputSchema: schema,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ProfileHash != second.ProfileHash {
		t.Fatalf("展示名不应影响执行合同哈希: %s != %s", first.ProfileHash, second.ProfileHash)
	}
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
