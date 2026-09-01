package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	baseagent "reqflow/internal/app/agent"
	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

type DesignSessionView struct {
	Session domain.DesignSession    `json:"session"`
	Trace   baseagent.TraceEnvelope `json:"trace"`
}

type DesignService struct {
	designRepo port.WorkflowDesignRepo
	drafts     *DraftService
	llm        port.LLMClient
	catalog    domain.CapabilityCatalog
	now        func() time.Time
}

func NewDesignService(designRepo port.WorkflowDesignRepo, drafts *DraftService, llm port.LLMClient,
	catalog domain.CapabilityCatalog) (*DesignService, error) {
	if designRepo == nil || drafts == nil || llm == nil || catalog == nil {
		return nil, fmt.Errorf("workflow design service: dependencies are required")
	}
	return &DesignService{designRepo: designRepo, drafts: drafts, llm: llm, catalog: catalog, now: time.Now}, nil
}

func (s *DesignService) Create(ctx context.Context, workflowID string, agentAvailable bool) (*DesignSessionView, error) {
	draft, err := s.drafts.Get(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	session, err := domain.NewDesignSession(uuid.NewString(), draft.Draft.ID, draft.Draft.Revision, agentAvailable, now)
	if err != nil {
		return nil, err
	}
	record := port.DesignSessionRecord{Session: *session}
	record.AgentState, _ = json.Marshal(baseagent.RunState{Context: port.Context{Messages: []port.Message{}}})
	record.Trace, _ = json.Marshal(baseagent.TraceEnvelope{})
	if err := s.designRepo.CreateDesignSession(ctx, record); err != nil {
		return nil, err
	}
	return &DesignSessionView{Session: *session}, nil
}

func (s *DesignService) Get(ctx context.Context, id string) (*DesignSessionView, error) {
	record, err := s.designRepo.GetDesignSession(ctx, id)
	if err != nil {
		return nil, err
	}
	return recordView(record)
}

func (s *DesignService) Run(ctx context.Context, id, request string) (*DesignSessionView, error) {
	record, err := s.designRepo.GetDesignSession(ctx, id)
	if err != nil {
		return nil, err
	}
	draft, err := s.drafts.Get(ctx, record.Session.DraftID)
	if err != nil {
		return nil, err
	}
	if record.Session.DraftRevision != draft.Draft.Revision {
		if err := record.Session.RefreshDraftRevision(draft.Draft.Revision, s.now()); err != nil {
			return nil, err
		}
	}
	state, trace, err := decodeDesignState(record)
	if err != nil {
		return nil, err
	}
	tools := []baseagent.Tool{&baseagent.ProposalTool{Sink: &record.Session, Now: s.now}, &baseagent.HumanQuestionTool{Session: &record.Session, Now: s.now}}
	designer := &baseagent.DesignAgent{LLM: s.llm, Catalog: s.catalog}
	options := baseagent.RunOptions{ID: record.Session.ID, Label: "workflow-design", Ordinal: 0,
		Loop: baseagent.Config{MaxIterations: 8, RequireToolTermination: true}, TraceEnvelope: &trace}
	options.OnFlush = func(baseagent.RunTrace) error { return s.save(ctx, record, state, trace) }
	options.OnNeedsHuman = func(domain.HumanQuestion) error { return s.save(ctx, record, state, trace) }
	if err := designer.Run(ctx, &record.Session, &state, tools, request, options); err != nil {
		var modelErr *baseagent.ModelError
		if errors.As(err, &modelErr) {
			_ = record.Session.SwitchToManual(modelErr.FailureKind(), modelErr.Error(), s.now())
			if saveErr := s.save(ctx, record, state, trace); saveErr != nil {
				return nil, saveErr
			}
			return &DesignSessionView{Session: record.Session, Trace: trace}, nil
		}
		_ = s.save(ctx, record, state, trace)
		return nil, err
	}
	if err := s.save(ctx, record, state, trace); err != nil {
		return nil, err
	}
	return &DesignSessionView{Session: record.Session, Trace: trace}, nil
}

func (s *DesignService) Answer(ctx context.Context, id string, answer json.RawMessage, actor string) (*DesignSessionView, error) {
	record, err := s.designRepo.GetDesignSession(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := record.Session.AnswerHuman(answer, actor, s.now()); err != nil {
		return nil, err
	}
	state, trace, err := decodeDesignState(record)
	if err != nil {
		return nil, err
	}
	if err := s.save(ctx, record, state, trace); err != nil {
		return nil, err
	}
	return &DesignSessionView{Session: record.Session, Trace: trace}, nil
}

func (s *DesignService) AcceptProposal(ctx context.Context, id, proposalID string) (*DesignSessionView, error) {
	record, err := s.designRepo.GetDesignSession(ctx, id)
	if err != nil {
		return nil, err
	}
	var proposal *domain.CommandProposal
	for index := range record.Session.Proposals {
		if record.Session.Proposals[index].ID == proposalID {
			proposal = &record.Session.Proposals[index]
			break
		}
	}
	if proposal == nil || proposal.Status != domain.ProposalPending {
		return nil, fmt.Errorf("命令建议不存在或已处理")
	}
	var command struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(proposal.Command)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&command); err != nil || command.Type == "" || !json.Valid(command.Payload) {
		return nil, fmt.Errorf("Proposal command 必须是 type/payload")
	}
	result, err := s.drafts.ExecuteCommand(ctx, record.Session.DraftID, CommandRequest{CommandID: proposal.ID,
		ExpectedRevision: proposal.DraftRevision, Type: command.Type, Payload: command.Payload})
	if err != nil {
		return nil, err
	}
	if err := record.Session.ResolveProposal(proposalID, true, result.Draft.Revision, s.now()); err != nil {
		return nil, err
	}
	state, trace, err := decodeDesignState(record)
	if err != nil {
		return nil, err
	}
	if err := s.save(ctx, record, state, trace); err != nil {
		return nil, err
	}
	return &DesignSessionView{Session: record.Session, Trace: trace}, nil
}

func (s *DesignService) RejectProposal(ctx context.Context, id, proposalID string) (*DesignSessionView, error) {
	record, err := s.designRepo.GetDesignSession(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := record.Session.ResolveProposal(proposalID, false, record.Session.DraftRevision, s.now()); err != nil {
		return nil, err
	}
	state, trace, err := decodeDesignState(record)
	if err != nil {
		return nil, err
	}
	if err := s.save(ctx, record, state, trace); err != nil {
		return nil, err
	}
	return &DesignSessionView{Session: record.Session, Trace: trace}, nil
}

func (s *DesignService) SwitchToManual(ctx context.Context, id string) (*DesignSessionView, error) {
	record, err := s.designRepo.GetDesignSession(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := record.Session.SwitchToManual(domain.ModelUnavailable, "用户切换为手动编辑", s.now()); err != nil {
		return nil, err
	}
	state, trace, err := decodeDesignState(record)
	if err != nil {
		return nil, err
	}
	if err := s.save(ctx, record, state, trace); err != nil {
		return nil, err
	}
	return &DesignSessionView{Session: record.Session, Trace: trace}, nil
}

func (s *DesignService) save(ctx context.Context, record *port.DesignSessionRecord, state baseagent.RunState, trace baseagent.TraceEnvelope) error {
	record.AgentState, _ = json.Marshal(state)
	record.Trace, _ = json.Marshal(trace)
	return s.designRepo.SaveDesignSession(ctx, *record)
}

func decodeDesignState(record *port.DesignSessionRecord) (baseagent.RunState, baseagent.TraceEnvelope, error) {
	var state baseagent.RunState
	var trace baseagent.TraceEnvelope
	if err := json.Unmarshal(record.AgentState, &state); err != nil {
		return state, trace, fmt.Errorf("decode design agent state: %w", err)
	}
	if len(record.Trace) > 0 {
		if err := json.Unmarshal(record.Trace, &trace); err != nil {
			return state, trace, fmt.Errorf("decode design trace: %w", err)
		}
	}
	return state, trace, nil
}

func recordView(record *port.DesignSessionRecord) (*DesignSessionView, error) {
	var trace baseagent.TraceEnvelope
	if len(record.Trace) > 0 {
		if err := json.Unmarshal(record.Trace, &trace); err != nil {
			return nil, err
		}
	}
	return &DesignSessionView{Session: record.Session, Trace: trace}, nil
}
