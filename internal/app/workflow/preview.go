package workflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

type CreatePreviewRequest struct {
	DraftRevision int64           `json:"draft_revision"`
	Input         json.RawMessage `json:"input"`
}

type AcceptanceMismatch struct {
	Path     string `json:"path"`
	Expected any    `json:"expected"`
	Actual   any    `json:"actual"`
}

type RunAcceptanceResponse struct {
	Draft      domain.WorkflowDraft    `json:"draft"`
	Preview    *domain.WorkflowPreview `json:"preview"`
	Passed     bool                    `json:"passed"`
	Mismatches []AcceptanceMismatch    `json:"mismatches,omitempty"`
}

type PreviewService struct {
	repo    port.WorkflowDraftRepo
	catalog domain.CapabilityCatalog
	dryRuns *DryRunRegistry
	now     func() time.Time
}

func NewPreviewService(repo port.WorkflowDraftRepo, catalog domain.CapabilityCatalog, dryRuns *DryRunRegistry) (*PreviewService, error) {
	if repo == nil || catalog == nil || dryRuns == nil {
		return nil, fmt.Errorf("workflow preview service: repository, catalog and dry-run registry are required")
	}
	return &PreviewService{repo: repo, catalog: catalog, dryRuns: dryRuns, now: time.Now}, nil
}

// Create 按当前 Draft revision 顺序执行真实 dry-run。确定性 Capability 运行
// 真实内核，LLM/人工节点必须由 input.samples 提供显式人工模拟样本。
func (s *PreviewService) Create(ctx context.Context, workflowID string, request CreatePreviewRequest) (*domain.WorkflowPreview, error) {
	draft, err := s.repo.GetDraft(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	if request.DraftRevision != 0 && request.DraftRevision != draft.Revision {
		return nil, port.ErrRevisionConflict
	}
	if len(request.Input) == 0 || !json.Valid(request.Input) {
		return nil, fmt.Errorf("预览 input 必须是合法 JSON")
	}
	preview, err := s.execute(ctx, *draft, request.Input)
	if err != nil {
		return nil, err
	}
	if err := s.repo.CreatePreview(ctx, *preview); err != nil {
		return nil, err
	}
	return preview, nil
}

func (s *PreviewService) Get(ctx context.Context, previewID string) (*domain.WorkflowPreview, error) {
	return s.repo.GetPreview(ctx, previewID)
}

// RunAcceptance 用验收用例自己的 input 重跑整条 dry-run 链，把真实
// OutputManifest 与结构化 expectation 比较，只有本次运行全部通过才原子
// 更新 LastPassedRevision/LastPreviewID。
func (s *PreviewService) RunAcceptance(ctx context.Context, workflowID, caseID string) (*RunAcceptanceResponse, error) {
	draft, err := s.repo.GetDraft(ctx, workflowID)
	if err != nil {
		return nil, err
	}
	var testCase *domain.AcceptanceCase
	for index := range draft.AcceptanceCases {
		if strings.TrimSpace(draft.AcceptanceCases[index].ID) == caseID {
			testCase = &draft.AcceptanceCases[index]
			break
		}
	}
	if testCase == nil {
		return nil, port.ErrAcceptanceNotFound
	}
	preview, execErr := s.execute(ctx, *draft, testCase.Input)
	if execErr != nil {
		return nil, execErr
	}
	if err := s.repo.CreatePreview(ctx, *preview); err != nil {
		return nil, err
	}
	mismatches := compareExpectation(preview.OutputManifest, testCase.Expectation)
	passed := preview.Status == domain.PreviewPassed && len(mismatches) == 0
	if !passed {
		return &RunAcceptanceResponse{Draft: *draft, Preview: preview, Passed: false, Mismatches: mismatches}, nil
	}
	updated, err := s.repo.MarkAcceptancePassed(ctx, workflowID, caseID, draft.Revision, preview.ID, s.now())
	if err != nil {
		return nil, err
	}
	return &RunAcceptanceResponse{Draft: *updated, Preview: preview, Passed: true}, nil
}

func (s *PreviewService) execute(ctx context.Context, draft domain.WorkflowDraft, input json.RawMessage) (*domain.WorkflowPreview, error) {
	issues := domain.Validate(draft, s.catalog, domain.ValidateDraft)
	if domain.HasErrors(issues) {
		return nil, domain.ValidationError{Issues: issues}
	}
	parsed, err := decodePreviewInput(input)
	if err != nil {
		return nil, err
	}
	now := s.now()
	engine, err := newPreviewEngine(uuid.NewString(), draft, s.catalog, s.dryRuns, parsed)
	if err != nil {
		return nil, err
	}
	manifest, runIssues := engine.run(ctx)
	allIssues := append(append([]domain.ValidationIssue(nil), issues...), runIssues...)
	rawManifest, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("序列化预览 manifest: %w", err)
	}
	status := domain.PreviewPassed
	if len(runIssues) > 0 {
		status = domain.PreviewFailed
	}
	return &domain.WorkflowPreview{ID: engine.previewID, WorkflowID: draft.ID, DraftRevision: draft.Revision,
		Status: status, Input: append(json.RawMessage(nil), input...), OutputManifest: rawManifest,
		Issues: allIssues, StartedBy: LocalActorID, StartedAt: now, FinishedAt: now, Temporary: true}, nil
}

type expectationOutput struct {
	Metrics map[string]json.RawMessage `json:"metrics,omitempty"`
}

type expectationNode struct {
	Status    *string                      `json:"status,omitempty"`
	Simulated *bool                        `json:"simulated,omitempty"`
	Outputs   map[string]expectationOutput `json:"outputs,omitempty"`
}

type expectationRoot struct {
	Nodes map[string]expectationNode `json:"nodes,omitempty"`
}

type actualOutput struct {
	ResourceType domain.ResourceType        `json:"resource_type"`
	ResourceID   string                     `json:"resource_id"`
	Temporary    bool                       `json:"temporary"`
	Metrics      map[string]json.RawMessage `json:"metrics"`
}

type actualNode struct {
	Status    string                  `json:"status"`
	Simulated bool                    `json:"simulated"`
	Outputs   map[string]actualOutput `json:"outputs"`
}

type actualRoot struct {
	Temporary bool                  `json:"temporary"`
	Nodes     map[string]actualNode `json:"nodes"`
}

// compareExpectation 只比较 expectation 显式声明的字段：节点状态、模拟标记
// 与输出 metrics 子集。空 expectation 仅要求预览本身通过。
func compareExpectation(manifest json.RawMessage, expectation json.RawMessage) []AcceptanceMismatch {
	if len(strings.TrimSpace(string(expectation))) == 0 {
		return nil
	}
	var expected expectationRoot
	if err := json.Unmarshal(expectation, &expected); err != nil {
		return []AcceptanceMismatch{{Path: "expectation", Expected: "合法 expectation JSON", Actual: err.Error()}}
	}
	var actual actualRoot
	if err := json.Unmarshal(manifest, &actual); err != nil {
		return []AcceptanceMismatch{{Path: "manifest", Expected: "合法 manifest", Actual: err.Error()}}
	}
	mismatches := []AcceptanceMismatch{}
	for nodeID, nodeExpectation := range expected.Nodes {
		node, ok := actual.Nodes[nodeID]
		if !ok {
			mismatches = append(mismatches, AcceptanceMismatch{Path: "nodes." + nodeID,
				Expected: "节点出现在 manifest", Actual: "缺失"})
			continue
		}
		if nodeExpectation.Status != nil && *nodeExpectation.Status != node.Status {
			mismatches = append(mismatches, AcceptanceMismatch{Path: "nodes." + nodeID + ".status",
				Expected: *nodeExpectation.Status, Actual: node.Status})
		}
		if nodeExpectation.Simulated != nil && *nodeExpectation.Simulated != node.Simulated {
			mismatches = append(mismatches, AcceptanceMismatch{Path: "nodes." + nodeID + ".simulated",
				Expected: *nodeExpectation.Simulated, Actual: node.Simulated})
		}
		for port, outputExpectation := range nodeExpectation.Outputs {
			output, ok := node.Outputs[port]
			if !ok {
				mismatches = append(mismatches, AcceptanceMismatch{Path: "nodes." + nodeID + ".outputs." + port,
					Expected: "输出出现在 manifest", Actual: "缺失"})
				continue
			}
			for metric, want := range outputExpectation.Metrics {
				have, ok := output.Metrics[metric]
				if !ok || string(have) != string(want) {
					mismatches = append(mismatches, AcceptanceMismatch{
						Path:     "nodes." + nodeID + ".outputs." + port + ".metrics." + metric,
						Expected: json.RawMessage(want), Actual: json.RawMessage(have)})
				}
			}
		}
	}
	return mismatches
}
