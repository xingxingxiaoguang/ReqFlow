package platformagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	appcatalog "reqflow/internal/app/catalog"
	apporchestrator "reqflow/internal/app/orchestrator"
	appretrieval "reqflow/internal/app/retrieval"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

type memorySessionRepo struct {
	mu       sync.Mutex
	sessions map[string]model.AgentSession
}

func newMemorySessionRepo() *memorySessionRepo {
	return &memorySessionRepo{sessions: map[string]model.AgentSession{}}
}

func (r *memorySessionRepo) CreateAgentSession(_ context.Context, session *model.AgentSession) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if session.ID == "" {
		session.ID = "session-1"
	}
	r.sessions[session.ID] = *session
	return nil
}

func (r *memorySessionRepo) ListAgentSessions(_ context.Context, _ string, _ int) ([]model.AgentSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]model.AgentSession, 0, len(r.sessions))
	for _, session := range r.sessions {
		out = append(out, session)
	}
	return out, nil
}

func (r *memorySessionRepo) GetAgentSession(_ context.Context, id string) (*model.AgentSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	session, ok := r.sessions[id]
	if !ok {
		return nil, fmt.Errorf("not found")
	}
	return &session, nil
}

func (r *memorySessionRepo) BeginAgentSession(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	session := r.sessions[id]
	if session.Status == model.AgentSessionRunning {
		return port.ErrAgentSessionRunning
	}
	session.Status = model.AgentSessionRunning
	r.sessions[id] = session
	return nil
}

func (r *memorySessionRepo) SaveAgentSession(_ context.Context, session *model.AgentSession) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[session.ID] = *session
	return nil
}

func (*memorySessionRepo) RecoverAgentSessions(context.Context) error { return nil }

type memoryConfigRepo struct {
	mu       sync.Mutex
	skills   map[string]model.AgentSkill
	settings map[string]model.AgentToolSetting
	nextID   int
}

func newMemoryConfigRepo() *memoryConfigRepo {
	return &memoryConfigRepo{skills: map[string]model.AgentSkill{}, settings: map[string]model.AgentToolSetting{}}
}

func (r *memoryConfigRepo) ListAgentSkills(_ context.Context, workspaceID string, enabledOnly bool) ([]model.AgentSkill, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []model.AgentSkill
	for _, skill := range r.skills {
		if skill.WorkspaceID == workspaceID && (!enabledOnly || skill.Enabled) {
			out = append(out, skill)
		}
	}
	return out, nil
}

func (r *memoryConfigRepo) CreateAgentSkill(_ context.Context, skill *model.AgentSkill) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, existing := range r.skills {
		if existing.WorkspaceID == skill.WorkspaceID && existing.Slug == skill.Slug {
			return fmt.Errorf("duplicate slug")
		}
	}
	r.nextID++
	if skill.ID == "" {
		skill.ID = fmt.Sprintf("skill-%d", r.nextID)
	}
	r.skills[skill.ID] = *skill
	return nil
}

func (r *memoryConfigRepo) SetAgentSkillEnabled(_ context.Context, workspaceID, id string, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	skill, ok := r.skills[id]
	if !ok || skill.WorkspaceID != workspaceID {
		return fmt.Errorf("not found")
	}
	skill.Enabled = enabled
	r.skills[id] = skill
	return nil
}

func (r *memoryConfigRepo) EnsureBuiltinAgentSkill(_ context.Context, skill *model.AgentSkill) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, existing := range r.skills {
		if existing.WorkspaceID == skill.WorkspaceID && existing.Slug == skill.Slug {
			skill.ID, skill.Enabled = existing.ID, existing.Enabled
			r.skills[id] = *skill
			return nil
		}
	}
	r.nextID++
	skill.ID = fmt.Sprintf("skill-%d", r.nextID)
	r.skills[skill.ID] = *skill
	return nil
}

func (r *memoryConfigRepo) ListAgentToolSettings(_ context.Context, workspaceID string) ([]model.AgentToolSetting, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []model.AgentToolSetting
	for _, setting := range r.settings {
		if setting.WorkspaceID == workspaceID {
			out = append(out, setting)
		}
	}
	return out, nil
}

func (r *memoryConfigRepo) SetAgentToolEnabled(_ context.Context, workspaceID, name string, enabled bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.settings[workspaceID+"/"+name] = model.AgentToolSetting{WorkspaceID: workspaceID, ToolName: name, Enabled: enabled}
	return nil
}

type finalAnswerLLM struct {
	wantPrompt string
	toolCount  int
}

func (l *finalAnswerLLM) Stream(_ context.Context, cc *port.Context, emit func(port.AssistantEvent)) (*port.Message, error) {
	wantTools := l.toolCount
	if wantTools == 0 {
		wantTools = 8
	}
	if len(cc.Tools) != wantTools {
		return nil, fmt.Errorf("工具数量 = %d", len(cc.Tools))
	}
	if l.wantPrompt != "" && !strings.Contains(cc.SystemPrompt, l.wantPrompt) {
		return nil, fmt.Errorf("系统提示词未包含 %q", l.wantPrompt)
	}
	message := &port.Message{Role: port.RoleAssistant, StopReason: port.StopReasonStop,
		Content: []port.Block{{Type: port.BlockText, Text: "已经完成"}}}
	if emit != nil {
		emit(port.AssistantEvent{Type: port.EventTextDelta, Delta: "已经完成", Message: message})
	}
	return message, nil
}

func (l *finalAnswerLLM) Complete(ctx context.Context, cc *port.Context) (*port.Message, error) {
	return l.Stream(ctx, cc, nil)
}
func (*finalAnswerLLM) Ping(context.Context) error { return nil }

type blockingLLM struct{ started chan struct{} }

func (l *blockingLLM) Stream(ctx context.Context, _ *port.Context, _ func(port.AssistantEvent)) (*port.Message, error) {
	close(l.started)
	<-ctx.Done()
	return &port.Message{Role: port.RoleAssistant, StopReason: port.StopReasonAborted}, ctx.Err()
}

func (l *blockingLLM) Complete(ctx context.Context, cc *port.Context) (*port.Message, error) {
	return l.Stream(ctx, cc, nil)
}

func (*blockingLLM) Ping(context.Context) error { return nil }

type fakePlatform struct {
	datasets            []appcatalog.DatasetView
	profiles            []appretrieval.ProfileView
	definitions         []model.TaskDefinition
	registered          *apporchestrator.CreateDefinitionInput
	created             *apporchestrator.CreateExecutionInput
	startedTaskID       string
	createdDefinition   apporchestrator.DefinitionView
	createdTask         apporchestrator.TaskView
	createdTaskSnapshot apporchestrator.TaskSnapshot
}

func (f *fakePlatform) Register(_ context.Context, input apporchestrator.CreateDefinitionInput) (*apporchestrator.DefinitionView, error) {
	f.registered = &input
	view := f.createdDefinition
	if view.ID == "" {
		view = apporchestrator.DefinitionView{ID: "definition-1", Name: input.Name, Status: input.Status}
	}
	return &view, nil
}

func (f *fakePlatform) CreateExecution(_ context.Context, input apporchestrator.CreateExecutionInput) (*apporchestrator.TaskView, error) {
	f.created = &input
	view := f.createdTask
	if view.ID == "" {
		view = apporchestrator.TaskView{ID: "task-1", Title: input.Title, Status: model.TaskStatusPending}
	}
	return &view, nil
}

func (f *fakePlatform) Start(_ context.Context, id string) error {
	f.startedTaskID = id
	return nil
}

func (f *fakePlatform) Snapshot(_ context.Context, id string) (*apporchestrator.TaskSnapshot, error) {
	view := f.createdTaskSnapshot
	if view.Task.ID == "" {
		view.Task = apporchestrator.TaskView{ID: id, Title: "索引任务", Status: model.TaskStatusRunning}
	}
	return &view, nil
}

func (*fakePlatform) ListViews(context.Context, apporchestrator.TaskQuery) ([]apporchestrator.TaskView, error) {
	return []apporchestrator.TaskView{}, nil
}

func (f *fakePlatform) ListDefinitions(context.Context, appcatalog.Query) ([]model.TaskDefinition, error) {
	return f.definitions, nil
}

func (f *fakePlatform) ListDatasets(context.Context, appcatalog.Query) ([]appcatalog.DatasetView, error) {
	return f.datasets, nil
}

func (f *fakePlatform) ListProfileViews(context.Context, string, string, int) ([]appretrieval.ProfileView, error) {
	return f.profiles, nil
}

func (*fakePlatform) ListSnapshotViews(context.Context, string, string, string, int) ([]appretrieval.SnapshotView, error) {
	return []appretrieval.SnapshotView{}, nil
}

func (*fakePlatform) SearchAPI(context.Context, appretrieval.SearchAPIRequest) (*appretrieval.SearchResponse, error) {
	return &appretrieval.SearchResponse{}, nil
}

func testDeps(platform *fakePlatform) Dependencies {
	return Dependencies{Definitions: platform, Runtime: platform, Tasks: platform,
		Catalog: platform, Retrieval: platform}
}

func TestSessionCanResumeWithEightPlatformTools(t *testing.T) {
	repo := newMemorySessionRepo()
	configRepo := newMemoryConfigRepo()
	platform := &fakePlatform{}
	service, err := NewService(repo, configRepo, &finalAnswerLLM{}, testDeps(platform), Options{})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	session, err := service.CreateSession(context.Background(), "", "")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if session.Messages == nil || len(session.Messages) != 0 {
		t.Fatalf("新会话 messages 应为非 nil 空数组: %#v", session.Messages)
	}

	var eventTypes []string
	final, err := service.RunMessage(context.Background(), session.ID, "帮我处理这个任务", func(event StreamEvent) {
		eventTypes = append(eventTypes, event.Type)
	})
	if err != nil {
		t.Fatalf("RunMessage: %v", err)
	}
	if final.Title != "帮我处理这个任务" || final.Status != model.AgentSessionIdle {
		t.Fatalf("会话终态 = title:%q status:%q", final.Title, final.Status)
	}
	if len(final.Messages) != 2 || final.Messages[0].Role != port.RoleUser || final.Messages[1].Role != port.RoleAssistant {
		t.Fatalf("消息序列 = %#v", final.Messages)
	}
	if !contains(eventTypes, "assistant_delta") || !contains(eventTypes, "done") {
		t.Fatalf("事件序列缺少增量或完成: %v", eventTypes)
	}
	persisted, _ := repo.GetAgentSession(context.Background(), session.ID)
	var saved port.Context
	if err := json.Unmarshal(persisted.Context, &saved); err != nil {
		t.Fatalf("解析持久化上下文: %v", err)
	}
	if len(saved.Tools) != 0 || len(saved.Messages) != 2 {
		t.Fatalf("持久化上下文应保留消息并移除运行时工具: %+v", saved)
	}
}

func TestDetachedRunSurvivesClientCancellationUntilExplicitStop(t *testing.T) {
	repo := newMemorySessionRepo()
	configRepo := newMemoryConfigRepo()
	llm := &blockingLLM{started: make(chan struct{})}
	service, err := NewService(repo, configRepo, llm, testDeps(&fakePlatform{}), Options{})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	session, err := service.CreateSession(context.Background(), "", "")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	requestCtx, disconnect := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		_, runErr := service.RunMessage(context.WithoutCancel(requestCtx), session.ID, "持续执行", nil)
		finished <- runErr
	}()
	select {
	case <-llm.started:
	case <-time.After(time.Second):
		t.Fatal("模型未开始执行")
	}

	disconnect()
	select {
	case runErr := <-finished:
		t.Fatalf("客户端断开不应终止后台执行: %v", runErr)
	case <-time.After(50 * time.Millisecond):
	}
	if !service.StopMessage(session.ID) {
		t.Fatal("显式停止未找到运行中的会话")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("显式停止后执行未退出")
	}
	view, err := service.GetSession(context.Background(), session.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if view.Status != model.AgentSessionIdle {
		t.Fatalf("停止后的会话状态 = %q", view.Status)
	}
}

func TestIndexDatasetCreatesWorkflowTaskAndStartsIt(t *testing.T) {
	platform := &fakePlatform{
		datasets: []appcatalog.DatasetView{{ID: "dataset-1", Name: "需求数据", SchemaID: "schema-1", Status: model.DatasetStatusActive}},
		profiles: []appretrieval.ProfileView{{ID: "profile-1", Name: "需求混合索引", DatasetSchemaID: "schema-1"}},
	}
	tool := &indexDatasetTool{platformTool{deps: testDeps(platform), workspaceID: "default"}}
	output := tool.Execute(context.Background(), port.ToolCall{ID: "call-1", Name: "index_dataset",
		Arguments: json.RawMessage(`{"dataset_id":"dataset-1"}`)}, nil)
	if output.IsError {
		t.Fatalf("index_dataset: %s", output.Output)
	}
	if platform.registered == nil || len(platform.registered.Steps) != 1 ||
		platform.registered.Steps[0].Kind != string(model.StepKindRetrievalBuild) {
		t.Fatalf("没有创建最小 retrieval.build 流程: %+v", platform.registered)
	}
	if platform.created == nil || len(platform.created.Bindings) != 1 ||
		platform.created.Bindings[0].ResourceType != string(model.ResourceDatasetBoundary) {
		t.Fatalf("索引任务数据绑定非法: %+v", platform.created)
	}
	if platform.startedTaskID != "task-1" {
		t.Fatalf("索引任务未启动: %q", platform.startedTaskID)
	}
}

func TestSlashSkillInjectsPromptAndDisabledToolIsRemoved(t *testing.T) {
	repo, configRepo := newMemorySessionRepo(), newMemoryConfigRepo()
	llm := &finalAnswerLLM{wantPrompt: "必须输出三条精炼结论", toolCount: 7}
	service, err := NewService(repo, configRepo, llm, testDeps(&fakePlatform{}), Options{})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	skill, err := service.CreateSkill(context.Background(), "", CreateSkillInput{
		Slug: "three-points", Title: "三点结论", Prompt: "必须输出三条精炼结论",
	})
	if err != nil {
		t.Fatalf("CreateSkill: %v", err)
	}
	if err := service.SetToolEnabled(context.Background(), "", "run_task", false); err != nil {
		t.Fatalf("SetToolEnabled: %v", err)
	}
	session, err := service.CreateSession(context.Background(), "", "")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := service.RunMessage(context.Background(), session.ID, "/three-points 总结当前情况", nil); err != nil {
		t.Fatalf("RunMessage: %v", err)
	}
	if err := service.SetSkillEnabled(context.Background(), "", skill.ID, false); err != nil {
		t.Fatalf("SetSkillEnabled: %v", err)
	}
	if _, err := service.RunMessage(context.Background(), session.ID, "/three-points 再总结", nil); err == nil ||
		!strings.Contains(err.Error(), "不存在或已停用") {
		t.Fatalf("停用 Skill 后错误 = %v", err)
	}
}

func contains(items []string, value string) bool {
	for _, item := range items {
		if item == value {
			return true
		}
	}
	return false
}
