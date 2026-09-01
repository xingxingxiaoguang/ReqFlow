// Package platformagent 把 ReqFlow 的流程、任务与数据能力组装为首页数字大脑。
// 会话层复用 internal/app/agent 的 pi 式工具循环，仅负责持久化、平台工具与 SSE 事件。
package platformagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	baseagent "reqflow/internal/app/agent"
	appcatalog "reqflow/internal/app/catalog"
	apporchestrator "reqflow/internal/app/orchestrator"
	appretrieval "reqflow/internal/app/retrieval"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

const (
	maxMessageBytes = 64 << 10
	defaultTitle    = "新会话"
)

var ErrSessionRunning = port.ErrAgentSessionRunning

type TaskQueryAPI interface {
	ListViews(context.Context, apporchestrator.TaskQuery) ([]apporchestrator.TaskView, error)
}

type CatalogAPI interface {
	ListDefinitions(context.Context, appcatalog.Query) ([]model.TaskDefinition, error)
	ListDatasets(context.Context, appcatalog.Query) ([]appcatalog.DatasetView, error)
}

type RetrievalAPI interface {
	ListSnapshotViews(context.Context, string, string, string, int) ([]appretrieval.SnapshotView, error)
	SearchAPI(context.Context, appretrieval.SearchAPIRequest) (*appretrieval.SearchResponse, error)
}

type Dependencies struct {
	Tasks     TaskQueryAPI
	Catalog   CatalogAPI
	Retrieval RetrievalAPI
}

type Options struct{ MaxIterations int }

type Service struct {
	repo       port.AgentSessionRepo
	configRepo port.AgentConfigRepo
	llm        port.LLMClient
	deps       Dependencies
	opts       Options
	locks      sync.Map
	runs       sync.Map
}

func NewService(repo port.AgentSessionRepo, configRepo port.AgentConfigRepo, llm port.LLMClient,
	deps Dependencies, opts Options) (*Service, error) {
	if repo == nil || configRepo == nil || llm == nil || deps.Tasks == nil ||
		deps.Catalog == nil || deps.Retrieval == nil {
		return nil, fmt.Errorf("platform agent 依赖不完整")
	}
	if opts.MaxIterations <= 0 {
		opts.MaxIterations = 12
	}
	return &Service{repo: repo, configRepo: configRepo, llm: llm, deps: deps, opts: opts}, nil
}

type SessionSummary struct {
	ID           string    `json:"id"`
	WorkspaceID  string    `json:"workspace_id"`
	Title        string    `json:"title"`
	Status       string    `json:"status"`
	LastError    string    `json:"last_error,omitempty"`
	MessageCount int       `json:"message_count"`
	LastMessage  string    `json:"last_message,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type SessionView struct {
	SessionSummary
	Messages []port.Message `json:"messages"`
}

func (s *Service) Recover(ctx context.Context) error { return s.repo.RecoverAgentSessions(ctx) }

func (s *Service) CreateSession(ctx context.Context, workspaceID, title string) (*SessionView, error) {
	workspaceID = normalizeWorkspaceID(workspaceID)
	title = cleanTitle(title)
	cc := port.Context{Messages: []port.Message{}}
	raw, _ := json.Marshal(cc)
	session := &model.AgentSession{WorkspaceID: workspaceID, Title: title,
		Status: model.AgentSessionIdle, Context: raw}
	if err := s.repo.CreateAgentSession(ctx, session); err != nil {
		return nil, err
	}
	return sessionView(session)
}

func (s *Service) ListSessions(ctx context.Context, workspaceID string, limit int) ([]SessionSummary, error) {
	workspaceID = normalizeWorkspaceID(workspaceID)
	sessions, err := s.repo.ListAgentSessions(ctx, workspaceID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]SessionSummary, len(sessions))
	for i := range sessions {
		view, viewErr := sessionView(&sessions[i])
		if viewErr != nil {
			return nil, viewErr
		}
		out[i] = view.SessionSummary
	}
	return out, nil
}

func (s *Service) GetSession(ctx context.Context, id string) (*SessionView, error) {
	session, err := s.repo.GetAgentSession(ctx, strings.TrimSpace(id))
	if err != nil {
		return nil, err
	}
	return sessionView(session)
}

type StreamEvent struct {
	Type       string          `json:"type"`
	Delta      string          `json:"delta,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	ToolName   string          `json:"tool_name,omitempty"`
	Arguments  json.RawMessage `json:"arguments,omitempty"`
	Details    string          `json:"details,omitempty"`
	Output     string          `json:"output,omitempty"`
	IsError    bool            `json:"is_error,omitempty"`
	Message    *port.Message   `json:"message,omitempty"`
	Session    *SessionView    `json:"session,omitempty"`
}

// RunMessage 执行一次完整工具循环。HTTP 层必须传入与客户端连接解耦的 context；
// 用户主动停止时通过 StopMessage 取消，页面刷新或切换会话不应影响后台执行。
func (s *Service) RunMessage(ctx context.Context, id, text string, emit func(StreamEvent)) (*SessionView, error) {
	id, text = strings.TrimSpace(id), strings.TrimSpace(text)
	if text == "" || len(text) > maxMessageBytes {
		return nil, fmt.Errorf("消息必须为 1..%d 字节", maxMessageBytes)
	}
	lockValue, _ := s.locks.LoadOrStore(id, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	if !lock.TryLock() {
		return nil, port.ErrAgentSessionRunning
	}
	defer func() {
		lock.Unlock()
		s.locks.Delete(id)
	}()

	runCtx, cancel := context.WithCancel(ctx)
	s.runs.Store(id, cancel)
	defer func() {
		cancel()
		s.runs.Delete(id)
	}()

	session, err := s.repo.GetAgentSession(runCtx, id)
	if err != nil {
		return nil, err
	}
	settings, err := s.configRepo.ListAgentToolSettings(runCtx, session.WorkspaceID)
	if err != nil {
		return nil, err
	}
	skills, err := s.configRepo.ListAgentSkills(runCtx, session.WorkspaceID, true)
	if err != nil {
		return nil, err
	}
	selectedSkill, err := selectSkill(text, skills)
	if err != nil {
		return nil, err
	}
	tools := buildTools(s.deps, session.WorkspaceID, enabledToolSettings(settings))
	if err := s.repo.BeginAgentSession(runCtx, id); err != nil {
		return nil, err
	}

	var cc port.Context
	if err := json.Unmarshal(session.Context, &cc); err != nil {
		session.Status, session.LastError = model.AgentSessionError, "会话上下文损坏"
		_ = s.repo.SaveAgentSession(context.WithoutCancel(ctx), session)
		return nil, fmt.Errorf("会话上下文损坏: %w", err)
	}
	cc.SystemPrompt = buildSystemPrompt(tools, skills, selectedSkill)
	cc.Messages = append(cc.Messages, port.NewUserMessage(text))
	if session.Title == defaultTitle || strings.TrimSpace(session.Title) == "" {
		session.Title = titleFromMessage(text)
	}
	session.Status, session.LastError = model.AgentSessionRunning, ""
	if err := saveContext(context.WithoutCancel(ctx), s.repo, session, &cc); err != nil {
		return nil, err
	}
	if emit != nil {
		view, _ := sessionViewWithContext(session, &cc)
		emit(StreamEvent{Type: "started", Session: view})
	}

	var callbackErr error
	checkpoint := func() {
		if callbackErr != nil {
			return
		}
		if err := saveContext(context.WithoutCancel(runCtx), s.repo, session, &cc); err != nil {
			callbackErr = err
			cancel()
		}
	}
	loop := baseagent.New(s.llm, tools, baseagent.Config{MaxIterations: s.opts.MaxIterations})
	_, runErr := loop.Run(runCtx, &cc, func(event port.AssistantEvent) {
		if emit == nil {
			return
		}
		switch event.Type {
		case port.EventTextDelta:
			emit(StreamEvent{Type: "assistant_delta", Delta: event.Delta})
		case port.EventThinkingDelta:
			emit(StreamEvent{Type: "thinking_delta", Delta: event.Delta})
		}
	}, func(event baseagent.Event) {
		switch event.Type {
		case "message_end":
			checkpoint()
			if emit != nil && event.Message != nil {
				emit(StreamEvent{Type: "message", Message: event.Message})
			}
		case "tool_execution_start":
			if emit != nil {
				emit(StreamEvent{Type: "tool_start", ToolCallID: event.ToolCallID,
					ToolName: event.ToolName, Arguments: event.Args})
			}
		case "tool_execution_end":
			if emit != nil {
				emit(StreamEvent{Type: "tool_end", ToolCallID: event.ToolCallID,
					ToolName: event.ToolName, Details: event.Output.Details,
					Output: truncate(event.Output.Output, 6000), IsError: event.Output.IsError})
			}
		}
	})
	if callbackErr != nil {
		runErr = callbackErr
	}

	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		session.Status, session.LastError = model.AgentSessionError, truncate(runErr.Error(), 4000)
	} else {
		session.Status, session.LastError = model.AgentSessionIdle, ""
	}
	if saveErr := saveContext(context.WithoutCancel(ctx), s.repo, session, &cc); saveErr != nil && runErr == nil {
		runErr = saveErr
	}
	view, viewErr := sessionViewWithContext(session, &cc)
	if viewErr != nil && runErr == nil {
		runErr = viewErr
	}
	if emit != nil && view != nil {
		emit(StreamEvent{Type: "done", Session: view})
	}
	return view, runErr
}

// StopMessage 只取消指定会话当前正在执行的回答，不依赖任何 SSE 客户端仍然在线。
func (s *Service) StopMessage(id string) bool {
	cancelValue, ok := s.runs.Load(strings.TrimSpace(id))
	if !ok {
		return false
	}
	cancelValue.(context.CancelFunc)()
	return true
}

func saveContext(ctx context.Context, repo port.AgentSessionRepo, session *model.AgentSession, cc *port.Context) error {
	copyContext := *cc
	copyContext.Tools = nil
	raw, err := json.Marshal(copyContext)
	if err != nil {
		return err
	}
	session.Context = raw
	return repo.SaveAgentSession(ctx, session)
}

func sessionView(session *model.AgentSession) (*SessionView, error) {
	var cc port.Context
	if err := json.Unmarshal(session.Context, &cc); err != nil {
		return nil, fmt.Errorf("解析 AgentSession %s: %w", session.ID, err)
	}
	return sessionViewWithContext(session, &cc)
}

func sessionViewWithContext(session *model.AgentSession, cc *port.Context) (*SessionView, error) {
	messages := make([]port.Message, len(cc.Messages))
	copy(messages, cc.Messages)
	summary := SessionSummary{ID: session.ID, WorkspaceID: session.WorkspaceID, Title: session.Title,
		Status: session.Status, LastError: session.LastError, MessageCount: len(messages),
		CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt}
	for i := len(messages) - 1; i >= 0; i-- {
		if text := strings.TrimSpace(messages[i].Text()); text != "" {
			summary.LastMessage = truncate(text, 120)
			break
		}
	}
	return &SessionView{SessionSummary: summary, Messages: messages}, nil
}

func cleanTitle(title string) string {
	title = strings.TrimSpace(strings.ReplaceAll(title, "\n", " "))
	if title == "" {
		return defaultTitle
	}
	return truncate(title, 60)
}

func titleFromMessage(message string) string {
	message = strings.Join(strings.Fields(message), " ")
	return truncate(message, 32)
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}
