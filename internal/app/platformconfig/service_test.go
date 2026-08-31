package platformconfig

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

type memoryRepo struct {
	values map[string]model.PlatformConfig
}

func newMemoryRepo() *memoryRepo { return &memoryRepo{values: map[string]model.PlatformConfig{}} }

func configKey(workspaceID, kind, id string) string { return workspaceID + "/" + kind + "/" + id }

func (r *memoryRepo) ListPlatformConfigs(_ context.Context, workspaceID, kind string) ([]model.PlatformConfig, error) {
	var out []model.PlatformConfig
	for _, value := range r.values {
		if value.WorkspaceID == workspaceID && value.Kind == kind {
			out = append(out, value)
		}
	}
	return out, nil
}
func (r *memoryRepo) GetPlatformConfig(_ context.Context, workspaceID, kind, id string) (*model.PlatformConfig, error) {
	value, ok := r.values[configKey(workspaceID, kind, id)]
	if !ok {
		return nil, port.ErrPlatformConfigNotFound
	}
	return &value, nil
}
func (r *memoryRepo) GetActivePlatformConfig(_ context.Context, workspaceID, kind string) (*model.PlatformConfig, error) {
	for _, value := range r.values {
		if value.WorkspaceID == workspaceID && value.Kind == kind && value.Active {
			return &value, nil
		}
	}
	return nil, port.ErrPlatformConfigNotFound
}
func (r *memoryRepo) CreatePlatformConfig(_ context.Context, value *model.PlatformConfig) error {
	if value.ID == "" {
		value.ID = "config-" + strings.ReplaceAll(value.Name, " ", "-")
	}
	if value.Active {
		_ = r.DeactivatePlatformConfigs(context.Background(), value.WorkspaceID, value.Kind)
	}
	r.values[configKey(value.WorkspaceID, value.Kind, value.ID)] = *value
	return nil
}
func (r *memoryRepo) UpdatePlatformConfig(_ context.Context, value *model.PlatformConfig) error {
	key := configKey(value.WorkspaceID, value.Kind, value.ID)
	if _, ok := r.values[key]; !ok {
		return port.ErrPlatformConfigNotFound
	}
	r.values[key] = *value
	return nil
}
func (r *memoryRepo) DeletePlatformConfig(_ context.Context, workspaceID, kind, id string) error {
	key := configKey(workspaceID, kind, id)
	if _, ok := r.values[key]; !ok {
		return port.ErrPlatformConfigNotFound
	}
	delete(r.values, key)
	return nil
}
func (r *memoryRepo) ActivatePlatformConfig(_ context.Context, workspaceID, kind, id string) error {
	key := configKey(workspaceID, kind, id)
	value, ok := r.values[key]
	if !ok {
		return port.ErrPlatformConfigNotFound
	}
	_ = r.DeactivatePlatformConfigs(context.Background(), workspaceID, kind)
	value.Active = true
	r.values[key] = value
	return nil
}
func (r *memoryRepo) DeactivatePlatformConfigs(_ context.Context, workspaceID, kind string) error {
	for key, value := range r.values {
		if value.WorkspaceID == workspaceID && value.Kind == kind {
			value.Active = false
			r.values[key] = value
		}
	}
	return nil
}

type testCipher struct{}

func (testCipher) Encrypt(value string) (string, error) { return "encrypted:" + value, nil }
func (testCipher) Decrypt(value string) (string, error) {
	if !strings.HasPrefix(value, "encrypted:") {
		return "", errors.New("bad ciphertext")
	}
	return strings.TrimPrefix(value, "encrypted:"), nil
}

func newTestService(t *testing.T) (*Service, *memoryRepo) {
	t.Helper()
	repo := newMemoryRepo()
	const fallbackCredential = "placeholder"
	service, err := NewService(repo, testCipher{}, Fallbacks{
		WorkspaceName: "ReqFlow",
		LLM: port.LLMRuntimeConfig{Provider: "openai", BaseURL: "https://file.example/v1",
			APIKey: fallbackCredential, Model: "file-model", TimeoutMs: 300000},
		Embedding: port.EmbeddingRuntimeConfig{BaseURL: "https://file.example/v1", APIKey: fallbackCredential,
			Model: "embedding-model", Dimensions: 1024, BatchSize: 32, TimeoutMs: 30000},
		Rerank: port.RerankRuntimeConfig{BaseURL: "https://file.example/v1", APIKey: fallbackCredential,
			Model: "rerank-model", TimeoutMs: 60000},
		MinerU: port.MinerURuntimeConfig{Enabled: true, APIURL: "https://mineru.net", APIToken: fallbackCredential,
			ModelVersion: "vlm", TimeoutMs: 600000, PollIntervalMs: 5000},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, repo
}

func TestCatalogShowsReadOnlyFileFallbackWithoutSecret(t *testing.T) {
	service, _ := newTestService(t)
	view, err := service.Catalog(context.Background(), "default")
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Groups) != 4 || view.Groups[0].ActiveID != fileConfigID || !view.Summary.LLM {
		t.Fatalf("unexpected catalog: %+v", view)
	}
	file := view.Groups[0].Items[0]
	if !file.ReadOnly || !file.Active || !file.SecretConfigured {
		t.Fatalf("file fallback flags: %+v", file)
	}
	raw, _ := json.Marshal(view)
	if strings.Contains(string(raw), "placeholder") {
		t.Fatalf("catalog leaked secret: %s", raw)
	}
}

func TestDatabaseConfigActivationAndFileFallback(t *testing.T) {
	service, repo := newTestService(t)
	input := func(name, modelName, secret string) UpsertInput {
		config, _ := json.Marshal(port.LLMRuntimeConfig{Provider: "openai", BaseURL: "https://db.example/v1",
			Model: modelName, Temperature: 0.3, MaxTokens: 4096, TimeoutMs: 120000})
		return UpsertInput{Name: name, Config: config, Secret: secret, Activate: true}
	}
	first, err := service.Create(context.Background(), "default", model.PlatformConfigLLM, input("first", "model-1", "secret-1"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Create(context.Background(), "default", model.PlatformConfigLLM, input("second", "model-2", "secret-2"))
	if err != nil {
		t.Fatal(err)
	}
	active := 0
	for _, value := range repo.values {
		if value.Kind == model.PlatformConfigLLM && value.Active {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("active database configs=%d want=1", active)
	}
	resolved, err := service.ResolveLLM(context.Background())
	if err != nil || resolved.Model != "model-2" || resolved.APIKey != "secret-2" {
		t.Fatalf("unexpected resolved config: %+v err=%v", resolved, err)
	}
	if err := service.Activate(context.Background(), "default", model.PlatformConfigLLM, first.ID); err != nil {
		t.Fatal(err)
	}
	resolved, _ = service.ResolveLLM(context.Background())
	if resolved.Model != "model-1" {
		t.Fatalf("first config not activated: %+v", resolved)
	}
	if err := service.Activate(context.Background(), "default", model.PlatformConfigLLM, fileConfigID); err != nil {
		t.Fatal(err)
	}
	resolved, _ = service.ResolveLLM(context.Background())
	if resolved.Model != "file-model" || resolved.APIKey != "placeholder" {
		t.Fatalf("file fallback not restored: %+v", resolved)
	}
	_ = second
}

func TestFileFallbackCannotBeChangedAndEmbeddingDimensionIsGuarded(t *testing.T) {
	service, _ := newTestService(t)
	if err := service.Delete(context.Background(), "default", model.PlatformConfigLLM, fileConfigID); err == nil {
		t.Fatal("file fallback deletion must fail")
	}
	config, _ := json.Marshal(port.EmbeddingRuntimeConfig{BaseURL: "https://db.example/v1",
		Model: "wrong", Dimensions: 768, BatchSize: 32, TimeoutMs: 30000})
	_, err := service.Create(context.Background(), "default", model.PlatformConfigEmbedding,
		UpsertInput{Name: "wrong dimensions", Config: config, Secret: "secret"})
	if err == nil || !strings.Contains(err.Error(), "1024") {
		t.Fatalf("expected dimension validation error, got %v", err)
	}
}
