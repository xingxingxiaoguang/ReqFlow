package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

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
func (*analysisMemoryRepo) CreateArtifactForStep(context.Context, *model.Artifact, int) (*model.Artifact, error) {
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

func TestFinalStructuredOutputRejectsSchemaDrift(t *testing.T) {
	schema := json.RawMessage(`{"type":"object","additionalProperties":false,"required":["report"],"properties":{"report":{"type":"string"}}}`)
	valid := &structContextBuilder{}
	ctx := valid.withAssistant(`前置说明 {"report":"# 方案"} 结束`)
	output, err := finalStructuredOutput(ctx, schema)
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != `{"report":"# 方案"}` {
		t.Fatalf("unexpected output: %s", output)
	}
	if _, err := finalStructuredOutput(valid.withAssistant(`{"report":"ok","extra":true}`), schema); err == nil {
		t.Fatal("额外字段必须被 Schema 拒绝")
	}
}

type structContextBuilder struct{}

func (*structContextBuilder) withAssistant(text string) *port.Context {
	return &port.Context{Messages: []port.Message{{Role: port.RoleAssistant,
		Content: []port.Block{{Type: port.BlockText, Text: text}}}}}
}
