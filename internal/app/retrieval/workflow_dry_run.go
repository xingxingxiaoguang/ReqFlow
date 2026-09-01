package retrieval

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"reqflow/internal/domain/model"
	domain "reqflow/internal/domain/workflow"
	"reqflow/internal/port"
)

// WorkflowBuildDryRunner 处理 retrieval.build 的预览。真实构建会写
// OpenSearch 索引与向量，预览禁止触碰；dry-run 只读取目标 Dataset 做真实
// 合同一致性检查，并输出标记模拟的索引计划。
type WorkflowBuildDryRunner struct {
	reader datasetReader
}

type datasetReader interface {
	GetAppendDataset(ctx context.Context, id string) (*model.Dataset, error)
	GetDatasetSchema(ctx context.Context, id string) (*model.DatasetSchemaDefinition, error)
}

func NewWorkflowBuildDryRunner(reader datasetReader) (*WorkflowBuildDryRunner, error) {
	if reader == nil {
		return nil, fmt.Errorf("workflow retrieval.build dry-run: dataset reader is required")
	}
	return &WorkflowBuildDryRunner{reader: reader}, nil
}

func (*WorkflowBuildDryRunner) Capability() domain.CapabilityRef {
	return domain.CapabilityRef{Kind: "retrieval.build", Version: 1}
}

func (e *WorkflowBuildDryRunner) DryRun(ctx context.Context, execution port.WorkflowDryRunExecution) (port.WorkflowDryRunResult, error) {
	datasetID := ""
	if upstream, ok := execution.Samples.Upstream("dataset"); ok {
		var sample struct {
			DatasetID string `json:"dataset_id"`
		}
		if err := json.Unmarshal(upstream.Payload, &sample); err != nil || strings.TrimSpace(sample.DatasetID) == "" {
			return port.WorkflowDryRunResult{}, fmt.Errorf("dataset 边界样本必须携带 dataset_id")
		}
		datasetID = sample.DatasetID
	} else if input, bound := dryRunInput(execution.Inputs, "dataset"); bound {
		datasetID = strings.TrimSpace(input.ResourceID)
	}
	if datasetID == "" {
		return port.WorkflowDryRunResult{}, fmt.Errorf("retrieval.build 预览缺少 dataset 输入")
	}
	if execution.Rules.DataContract == nil || execution.Rules.Search == nil {
		return port.WorkflowDryRunResult{}, fmt.Errorf("retrieval.build 预览需要内联 DataContract 与 SearchSpec")
	}
	dataset, err := e.reader.GetAppendDataset(ctx, datasetID)
	if err != nil {
		return port.WorkflowDryRunResult{}, fmt.Errorf("读取目标 Dataset: %w", err)
	}
	if dataset.Status != model.DatasetStatusActive {
		return port.WorkflowDryRunResult{}, fmt.Errorf("目标 Dataset %s 当前状态 %s 不允许构建索引", dataset.ID, dataset.Status)
	}
	schema, err := e.reader.GetDatasetSchema(ctx, dataset.SchemaID)
	if err != nil {
		return port.WorkflowDryRunResult{}, fmt.Errorf("读取目标 DatasetSchema: %w", err)
	}
	_, schemaHash, err := domain.CompileDataContract(*execution.Rules.DataContract)
	if err != nil {
		return port.WorkflowDryRunResult{}, fmt.Errorf("编译 DataContract: %w", err)
	}
	if schema.SchemaHash != schemaHash {
		return port.WorkflowDryRunResult{}, fmt.Errorf("目标 Dataset 与内联 DataContract 不一致，预览终止")
	}
	search := *execution.Rules.Search
	plan, err := json.Marshal(map[string]any{"dataset_id": dataset.ID, "item_count": dataset.ItemCount,
		"preset": search.Preset, "lexical_fields": search.LexicalFields, "vector_fields": search.VectorFields,
		"filter_fields": search.FilterFields, "chunk_size": search.ChunkSize,
		"chunk_overlap": search.ChunkOverlap, "simulated": true})
	if err != nil {
		return port.WorkflowDryRunResult{}, err
	}
	return port.WorkflowDryRunResult{
		Simulated: true,
		Outputs: []domain.NodeResourceBinding{{Port: "snapshot",
			ResourceType: domain.ResourceRetrievalSnapshot,
			ResourceID:   domain.TemporaryResourceID(execution.PreviewID, execution.Node.ID, "snapshot")}},
		Samples: map[string]json.RawMessage{"snapshot": plan},
		Metrics: map[string]any{"dataset_items": dataset.ItemCount,
			"lexical_fields": len(search.LexicalFields), "vector_fields": len(search.VectorFields)},
	}, nil
}

func dryRunInput(inputs []domain.NodeResourceBinding, portName string) (domain.NodeResourceBinding, bool) {
	for _, input := range inputs {
		if input.Port == portName && input.Direction == domain.BindingInput {
			return input, true
		}
	}
	return domain.NodeResourceBinding{}, false
}
