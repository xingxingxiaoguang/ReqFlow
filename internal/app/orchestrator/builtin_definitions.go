package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
)

// v1 固定的两个任务流程。抽取/检索规则不在定义里写死（config 留空占位），
// 由创建任务时按目标数据集的 schema 落实。流程管理界面在 v1 隐藏，
// 这两个定义就是用户可见的全部流程目录。
const (
	BuiltinCleaningDefinitionKey = "data_clean_import"
	BuiltinIndexDefinitionKey    = "retrieval_index"

	BuiltinDefinitionWorkspace = "default"
)

// EnsureBuiltinDefinitions 按 (workspace_id, key) insert-if-missing 幂等种子；
// 已存在的同 key 定义（含历史版本）一律不动。返回本次实际种子的 key。
func (s *DefinitionService) EnsureBuiltinDefinitions(ctx context.Context) ([]string, error) {
	return s.ensureBuiltinDefinitions(ctx, BuiltinDefinitionWorkspace)
}

func (s *DefinitionService) ensureBuiltinDefinitions(ctx context.Context, workspaceID string) ([]string, error) {
	var created []string
	for _, definition := range builtinDefinitions() {
		_, found, err := s.repo.GetTaskDefinitionByKey(ctx, workspaceID, definition.Key)
		if err != nil {
			return created, fmt.Errorf("读取固定流程 %s: %w", definition.Key, err)
		}
		if found {
			continue
		}
		definition.WorkspaceID = workspaceID
		if _, err := s.Create(ctx, definitionFromInput(definition)); err != nil {
			return created, fmt.Errorf("种子固定流程 %s: %w", definition.Key, err)
		}
		created = append(created, definition.Key)
	}
	return created, nil
}

func builtinDefinitions() []CreateDefinitionInput {
	emptyConfig := json.RawMessage(`{}`)
	return []CreateDefinitionInput{{
		Key:  BuiltinCleaningDefinitionKey,
		Name: "数据清洗入库",
		Description: "文件解析、结构化抽取、清洗校验、人工审核并原子发布到数据集。" +
			"抽取规则在创建任务时按目标数据集的字段结构选择。",
		Status: "active",
		InputPorts: map[string]PortInput{
			"assets": {ResourceType: "asset_set", Required: true, Description: "待解析文件集"},
			"target": {ResourceType: "dataset", Required: true, Description: "写入目标数据集"},
		},
		OutputPorts:    map[string]PortInput{"batch": {ResourceType: "dataset_batch", Description: "已提交数据批次"}},
		OutputBindings: map[string]string{"batch": "$step.publish.batch"},
		Steps: []StepDefinitionInput{
			{ID: "parse", Name: "解析文件", Kind: "source.parse",
				Inputs: map[string]string{"assets": "$task.assets"}, Outputs: map[string]string{"documents": "parsed_documents"}, Config: emptyConfig},
			{ID: "extract", Name: "结构化抽取", Kind: "document.extract", DependsOn: []string{"parse"},
				Inputs:  map[string]string{"documents": "$step.parse.documents"},
				Outputs: map[string]string{"drafts": "record_drafts"}, Config: emptyConfig},
			{ID: "transform", Name: "确定性清洗", Kind: "data.transform", DependsOn: []string{"extract"},
				Inputs:  map[string]string{"drafts": "$step.extract.drafts"},
				Outputs: map[string]string{"records": "transformed_records"}, Config: emptyConfig},
			{ID: "validate", Name: "Schema 与业务校验", Kind: "data.validate", DependsOn: []string{"transform"},
				Inputs:  map[string]string{"records": "$step.transform.records", "dataset": "$task.target"},
				Outputs: map[string]string{"validation": "validation_results"}, Config: emptyConfig},
			{ID: "review", Name: "人工审核", Kind: "human.review", DependsOn: []string{"validate"},
				Inputs:  map[string]string{"validation": "$step.validate.validation"},
				Outputs: map[string]string{"approved": "approved_records"},
				Config:  json.RawMessage(`{"allow_edit":true}`)},
			{ID: "publish", Name: "原子发布", Kind: "data.publish", DependsOn: []string{"review"},
				Inputs:  map[string]string{"approved": "$step.review.approved"},
				Outputs: map[string]string{"batch": "dataset_batch", "dataset": "dataset_boundary"}, Config: emptyConfig},
		},
	}, {
		Key:  BuiltinIndexDefinitionKey,
		Name: "建立检索索引",
		Description: "对数据集固定边界构建精准与语义混合检索快照。" +
			"索引规则在创建任务时按目标数据集的字段结构选择。",
		Status:         "active",
		InputPorts:     map[string]PortInput{"dataset": {ResourceType: "dataset_boundary", Required: true, Description: "需要建立索引的数据集固定边界"}},
		OutputPorts:    map[string]PortInput{"snapshot": {ResourceType: "retrieval_snapshot", Description: "可复现检索快照"}},
		OutputBindings: map[string]string{"snapshot": "$step.build.snapshot"},
		Steps: []StepDefinitionInput{
			{ID: "build", Name: "构建混合检索索引", Kind: "retrieval.build",
				Inputs:  map[string]string{"dataset": "$task.dataset"},
				Outputs: map[string]string{"snapshot": "retrieval_snapshot"}, Config: emptyConfig},
		},
	}}
}
