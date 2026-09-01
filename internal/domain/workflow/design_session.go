package workflow

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type AssistanceMode string

const (
	AssistanceManual AssistanceMode = "manual"
	AssistanceAgent  AssistanceMode = "agent"
)

type DesignSessionStatus string

const (
	DesignManualEditing DesignSessionStatus = "manual_editing"
	DesignAgentRunning  DesignSessionStatus = "agent_running"
	DesignAwaitingHuman DesignSessionStatus = "awaiting_human"
	DesignCompleted     DesignSessionStatus = "completed"
)

type ModelFailureKind string

const (
	ModelUnavailable     ModelFailureKind = "model_unavailable"
	ModelRateLimited     ModelFailureKind = "rate_limited"
	ModelAuthentication  ModelFailureKind = "authentication_failed"
	ModelContextOverflow ModelFailureKind = "context_overflow"
	ModelInvalidOutput   ModelFailureKind = "invalid_output"
	ModelPolicyBlocked   ModelFailureKind = "policy_blocked"
)

type ModelFailure struct {
	Kind       ModelFailureKind `json:"kind"`
	Message    string           `json:"message"`
	OccurredAt time.Time        `json:"occurred_at"`
}

type ProposalStatus string

const (
	ProposalPending  ProposalStatus = "pending"
	ProposalAccepted ProposalStatus = "accepted"
	ProposalRejected ProposalStatus = "rejected"
	ProposalObsolete ProposalStatus = "obsolete"
)

type CommandProposal struct {
	ID            string          `json:"id"`
	DraftRevision int64           `json:"draft_revision"`
	Summary       string          `json:"summary"`
	Command       json.RawMessage `json:"command"`
	Status        ProposalStatus  `json:"status"`
	CreatedAt     time.Time       `json:"created_at"`
}

type HumanQuestion struct {
	ID          string          `json:"id"`
	Path        string          `json:"path"`
	Prompt      string          `json:"prompt"`
	Context     json.RawMessage `json:"context,omitempty"`
	RequestedAt time.Time       `json:"requested_at"`
	AnsweredAt  time.Time       `json:"answered_at,omitempty"`
	AnsweredBy  string          `json:"answered_by,omitempty"`
	Answer      json.RawMessage `json:"answer,omitempty"`
}

type DesignSession struct {
	ID              string              `json:"id"`
	DraftID         string              `json:"draft_id"`
	DraftRevision   int64               `json:"draft_revision"`
	Mode            AssistanceMode      `json:"mode"`
	Status          DesignSessionStatus `json:"status"`
	AgentAvailable  bool                `json:"agent_available"`
	Failure         *ModelFailure       `json:"failure,omitempty"`
	PendingQuestion *HumanQuestion      `json:"pending_question,omitempty"`
	Proposals       []CommandProposal   `json:"proposals,omitempty"`
	CreatedAt       time.Time           `json:"created_at"`
	UpdatedAt       time.Time           `json:"updated_at"`
}

func NewDesignSession(id, draftID string, draftRevision int64, agentAvailable bool,
	now time.Time) (*DesignSession, error) {
	if strings.TrimSpace(id) == "" || strings.TrimSpace(draftID) == "" || draftRevision < 0 || now.IsZero() {
		return nil, fmt.Errorf("design session 的 id、draft_id、draft_revision 和时间必须有效")
	}
	mode, status := AssistanceManual, DesignManualEditing
	if agentAvailable {
		mode, status = AssistanceAgent, DesignAgentRunning
	}
	return &DesignSession{ID: id, DraftID: draftID, DraftRevision: draftRevision,
		Mode: mode, Status: status, AgentAvailable: agentAvailable, CreatedAt: now, UpdatedAt: now}, nil
}

func (s *DesignSession) StartAgent(now time.Time) error {
	if err := s.ensureMutable(now); err != nil {
		return err
	}
	if !s.AgentAvailable {
		return fmt.Errorf("Agent 当前不可用；手动编辑仍可继续")
	}
	s.Mode, s.Status, s.Failure, s.PendingQuestion = AssistanceAgent, DesignAgentRunning, nil, nil
	s.UpdatedAt = now
	return nil
}

func (s *DesignSession) SwitchToManual(kind ModelFailureKind, message string, now time.Time) error {
	if err := s.ensureMutable(now); err != nil {
		return err
	}
	if !validModelFailureKind(kind) {
		return fmt.Errorf("未知模型故障类型 %q", kind)
	}
	s.Mode, s.Status = AssistanceManual, DesignManualEditing
	s.Failure = &ModelFailure{Kind: kind, Message: strings.TrimSpace(message), OccurredAt: now}
	s.PendingQuestion = nil
	for i := range s.Proposals {
		if s.Proposals[i].Status == ProposalPending {
			s.Proposals[i].Status = ProposalObsolete
		}
	}
	s.UpdatedAt = now
	return nil
}

func (s *DesignSession) RequestHuman(question HumanQuestion, now time.Time) error {
	if err := s.ensureMutable(now); err != nil {
		return err
	}
	if s.Mode != AssistanceAgent || s.Status != DesignAgentRunning {
		return fmt.Errorf("只有运行中的 Agent 会话可以请求人工决策")
	}
	if strings.TrimSpace(question.ID) == "" || strings.TrimSpace(question.Path) == "" || strings.TrimSpace(question.Prompt) == "" {
		return fmt.Errorf("人工问题必须包含 id、path 和 prompt")
	}
	if len(question.Context) > 0 && !json.Valid(question.Context) {
		return fmt.Errorf("人工问题 context 必须是合法 JSON")
	}
	question.RequestedAt = now
	s.PendingQuestion = &question
	s.Status, s.UpdatedAt = DesignAwaitingHuman, now
	return nil
}

func (s *DesignSession) AnswerHuman(answer json.RawMessage, actor string, now time.Time) error {
	if err := s.ensureMutable(now); err != nil {
		return err
	}
	if s.Status != DesignAwaitingHuman || s.PendingQuestion == nil {
		return fmt.Errorf("当前没有等待回答的人工问题")
	}
	if strings.TrimSpace(actor) == "" || len(answer) == 0 || !json.Valid(answer) {
		return fmt.Errorf("人工回答必须包含 actor 和合法 JSON")
	}
	s.PendingQuestion.Answer = append(json.RawMessage(nil), answer...)
	s.PendingQuestion.AnsweredAt = now
	s.PendingQuestion.AnsweredBy = strings.TrimSpace(actor)
	s.Status, s.UpdatedAt = DesignAgentRunning, now
	return nil
}

func (s *DesignSession) AddProposal(proposal CommandProposal, now time.Time) error {
	if err := s.ensureMutable(now); err != nil {
		return err
	}
	if s.Mode != AssistanceAgent || s.Status != DesignAgentRunning {
		return fmt.Errorf("只有运行中的 Agent 会话可以提交命令建议")
	}
	if strings.TrimSpace(proposal.ID) == "" || strings.TrimSpace(proposal.Summary) == "" ||
		proposal.DraftRevision != s.DraftRevision || len(proposal.Command) == 0 || !json.Valid(proposal.Command) {
		return fmt.Errorf("命令建议缺少有效 id、summary、draft_revision 或 command")
	}
	for _, existing := range s.Proposals {
		if existing.ID == proposal.ID {
			return fmt.Errorf("命令建议 %s 已存在", proposal.ID)
		}
	}
	proposal.Command = append(json.RawMessage(nil), proposal.Command...)
	proposal.Status, proposal.CreatedAt = ProposalPending, now
	s.Proposals = append(s.Proposals, proposal)
	s.UpdatedAt = now
	return nil
}

func (s *DesignSession) ResolveProposal(id string, accept bool, nextDraftRevision int64, now time.Time) error {
	if err := s.ensureMutable(now); err != nil {
		return err
	}
	for i := range s.Proposals {
		proposal := &s.Proposals[i]
		if proposal.ID != id {
			continue
		}
		if proposal.Status != ProposalPending {
			return fmt.Errorf("命令建议 %s 已处理", id)
		}
		if accept {
			if nextDraftRevision <= s.DraftRevision {
				return fmt.Errorf("接受命令建议后草稿版本必须递增")
			}
			proposal.Status = ProposalAccepted
			s.DraftRevision = nextDraftRevision
			for j := range s.Proposals {
				if s.Proposals[j].Status == ProposalPending && s.Proposals[j].DraftRevision != nextDraftRevision {
					s.Proposals[j].Status = ProposalObsolete
				}
			}
		} else {
			proposal.Status = ProposalRejected
		}
		s.UpdatedAt = now
		return nil
	}
	return fmt.Errorf("命令建议 %s 不存在", id)
}

func (s *DesignSession) Complete(now time.Time) error {
	if err := s.ensureMutable(now); err != nil {
		return err
	}
	if s.PendingQuestion != nil && s.PendingQuestion.AnsweredAt.IsZero() {
		return fmt.Errorf("仍有未回答的人工问题")
	}
	s.Status, s.UpdatedAt = DesignCompleted, now
	return nil
}

func (s *DesignSession) CanEditManually() bool {
	return s != nil && s.Status != DesignCompleted
}

func (s *DesignSession) ensureMutable(now time.Time) error {
	if s == nil {
		return fmt.Errorf("design session 为空")
	}
	if now.IsZero() {
		return fmt.Errorf("操作时间不能为空")
	}
	if s.Status == DesignCompleted {
		return fmt.Errorf("design session 已完成")
	}
	return nil
}

func validModelFailureKind(kind ModelFailureKind) bool {
	switch kind {
	case ModelUnavailable, ModelRateLimited, ModelAuthentication, ModelContextOverflow, ModelInvalidOutput, ModelPolicyBlocked:
		return true
	default:
		return false
	}
}

type ToolOutcomeStatus string

const (
	ToolSucceeded      ToolOutcomeStatus = "succeeded"
	ToolRetryableError ToolOutcomeStatus = "retryable_error"
	ToolNeedsHuman     ToolOutcomeStatus = "needs_human"
	ToolCompleted      ToolOutcomeStatus = "completed"
)

type ToolOutcome struct {
	Status   ToolOutcomeStatus `json:"status"`
	Result   json.RawMessage   `json:"result,omitempty"`
	Error    string            `json:"error,omitempty"`
	Question *HumanQuestion    `json:"question,omitempty"`
}
