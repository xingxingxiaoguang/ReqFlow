//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/infra/database"
	"reqflow/internal/port"
)

func TestIntegrationWorkflowRuntimeLeaseRecoveryAndManualCompletion(t *testing.T) {
	db, err := database.Connect(testDSN(), 3, 500)
	if err != nil {
		t.Skipf("本地 PG 不可用，跳过集成测试: %v", err)
	}
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	repo := NewWorkflowRepo(db)
	revision := runtimeIntegrationRevision(t)
	if err := seedWorkflowRevision(ctx, repo, revision); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Exec(`DELETE FROM workflow_runs WHERE workflow_id = ?`, revision.WorkflowID).Error
		_ = db.Exec(`DELETE FROM workflow_revisions WHERE workflow_id = ?`, revision.WorkflowID).Error
		_ = db.Exec(`DELETE FROM workflows WHERE id = ?`, revision.WorkflowID).Error
	})

	run, nodes, err := domain.NewWorkflowRun(uuid.NewString(), revision, []domain.NodeResourceBinding{{
		ID: uuid.NewString(), NodeID: "first", Port: "in", Direction: domain.BindingInput,
		ResourceType: domain.ResourceAssetSet, ResourceID: uuid.NewString(),
	}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateWorkflowRun(ctx, *run, nodes); err != nil {
		t.Fatal(err)
	}
	created, err := repo.GetWorkflowRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if created.Run.Status != domain.RunQueued || len(created.Nodes) != 2 || len(created.Bindings) != 1 {
		t.Fatalf("创建快照非法: %+v", created)
	}
	if err := repo.StartWorkflowRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	first, err := repo.ClaimWorkflowNode(ctx, "owner-a", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := json.RawMessage(`{"cursor":7}`)
	if err := repo.SaveWorkflowNodeCheckpoint(ctx, first.ID, first.Attempt, "owner-a", checkpoint); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveWorkflowNodeProgress(ctx, first.ID, first.Attempt, "owner-a", json.RawMessage(`{"completed":1}`)); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveWorkflowNodeCheckpoint(ctx, first.ID, first.Attempt+1, "owner-a", checkpoint); !errors.Is(err, port.ErrRunLeaseLost) {
		t.Fatalf("错误 attempt 未被 fence: %v", err)
	}
	if err := db.Model(&workflowNodeRunRow{}).Where("id = ?", first.ID).Update("lease_until", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	if recovered, err := repo.RecoverWorkflowNodeLeases(ctx); err != nil || recovered != 1 {
		t.Fatalf("回收 lease: recovered=%d err=%v", recovered, err)
	}
	resumed, err := repo.ClaimWorkflowNode(ctx, "owner-b", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Attempt != first.Attempt+1 || string(resumed.Checkpoint) != string(checkpoint) {
		t.Fatalf("checkpoint 恢复失败: %+v", resumed)
	}
	output := domain.NodeResourceBinding{ID: uuid.NewString(), Port: "out", Direction: domain.BindingOutput,
		ResourceType: domain.ResourceParsedDocuments, ResourceID: uuid.NewString()}
	if err := repo.CompleteWorkflowNode(ctx, first.ID, first.Attempt, "owner-a", []domain.NodeResourceBinding{output}); !errors.Is(err, port.ErrRunLeaseLost) {
		t.Fatalf("过期 owner 未被 fence: %v", err)
	}
	if err := repo.CompleteWorkflowNode(ctx, resumed.ID, resumed.Attempt, "owner-b", []domain.NodeResourceBinding{output}); err != nil {
		t.Fatal(err)
	}
	secondInputs, err := repo.GetNodeInputs(ctx, created.Nodes[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondInputs) != 1 || secondInputs[0].Port != "in" || secondInputs[0].ResourceID != output.ResourceID {
		t.Fatalf("下游输入未按 Connection 传播: %+v", secondInputs)
	}
	second, err := repo.ClaimWorkflowNode(ctx, "owner-c", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AwaitWorkflowNodeManual(ctx, second.ID, second.Attempt, "owner-c", "needs_human", "请提交产物"); err != nil {
		t.Fatal(err)
	}
	manualOutput := domain.NodeResourceBinding{ID: uuid.NewString(), Port: "out", Direction: domain.BindingOutput,
		ResourceType: domain.ResourceArtifact, ResourceID: uuid.NewString()}
	if err := repo.CompleteWorkflowNodeManual(ctx, second.ID, second.Attempt, "reviewer", []domain.NodeResourceBinding{manualOutput}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CompleteWorkflowNodeManual(ctx, second.ID, second.Attempt, "reviewer", []domain.NodeResourceBinding{manualOutput}); !errors.Is(err, port.ErrRunInvalidTransition) {
		t.Fatalf("重复人工提交未被拒绝: %v", err)
	}
	finished, err := repo.GetWorkflowRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Run.Status != domain.RunSucceeded || len(finished.Bindings) != 4 {
		t.Fatalf("运行终态非法: %+v", finished)
	}
}

func runtimeIntegrationRevision(t *testing.T) domain.WorkflowRevision {
	t.Helper()
	first := domain.CapabilityDefinition{Ref: domain.CapabilityRef{Kind: "runtime.integration.first", Version: 1}, Label: "第一节点", Description: "集成测试",
		Inputs:       []domain.PortDefinition{{Name: "in", ResourceType: domain.ResourceAssetSet, Role: domain.PortPrimary, Required: true}},
		Outputs:      []domain.PortDefinition{{Name: "out", ResourceType: domain.ResourceParsedDocuments, Role: domain.PortPrimary, Required: true}},
		ConfigSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`), DefaultConfig: json.RawMessage(`{}`)}
	second := domain.CapabilityDefinition{Ref: domain.CapabilityRef{Kind: "runtime.integration.second", Version: 1}, Label: "第二节点", Description: "集成测试",
		Inputs:       []domain.PortDefinition{{Name: "in", ResourceType: domain.ResourceParsedDocuments, Role: domain.PortPrimary, Required: true}},
		Outputs:      []domain.PortDefinition{{Name: "out", ResourceType: domain.ResourceArtifact, Role: domain.PortPrimary, Required: true}},
		ConfigSchema: json.RawMessage(`{"type":"object","additionalProperties":false}`), DefaultConfig: json.RawMessage(`{}`), ManualCompletion: true}
	return domain.WorkflowRevision{ID: uuid.NewString(), WorkflowID: uuid.NewString(), RevisionNo: 1,
		WorkspaceID: "default", Key: fmt.Sprintf("runtime_integration_%d", time.Now().UnixNano()), Name: "运行时集成",
		Nodes: []domain.ResolvedNode{{ID: "first", Name: "第一节点", Capability: first, Config: json.RawMessage(`{}`)},
			{ID: "second", Name: "第二节点", Capability: second, Config: json.RawMessage(`{}`)}},
		Connections: []domain.Connection{{From: domain.Endpoint{Kind: domain.EndpointNodeOutput, NodeID: "first", Port: "out"},
			To: domain.Endpoint{Kind: domain.EndpointNodeInput, NodeID: "second", Port: "in"}}},
		ContentHash: uuid.NewString(), PublishedBy: "integration-test", PublishedAt: time.Now()}
}

func seedWorkflowRevision(ctx context.Context, repo *WorkflowRepo, revision domain.WorkflowRevision) error {
	now := time.Now()
	draft := domain.WorkflowDraft{ID: revision.WorkflowID, WorkspaceID: revision.WorkspaceID, Key: revision.Key,
		Name: revision.Name, Revision: 0, CreatedAt: now, UpdatedAt: now}
	if err := repo.CreateDraft(ctx, draft); err != nil {
		return err
	}
	content, err := json.Marshal(revision)
	if err != nil {
		return err
	}
	return repo.db.WithContext(ctx).Create(&workflowRevisionRow{ID: revision.ID, WorkflowID: revision.WorkflowID,
		RevisionNo: revision.RevisionNo, Content: string(content), ContentHash: revision.ContentHash,
		PublishedBy: revision.PublishedBy, PublishedAt: revision.PublishedAt}).Error
}
