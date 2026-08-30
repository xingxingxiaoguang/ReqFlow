package logic

import (
	"encoding/json"
	"strings"
	"testing"

	"reqflow/internal/domain/model"
)

func validTaskDefinition() model.TaskDefinition {
	return model.TaskDefinition{
		Key:    "product_spec_clean",
		Name:   "产品规格清洗",
		Status: model.TaskDefinitionDraft,
		InputPorts: map[string]model.PortDefinition{
			"documents": {ResourceType: model.ResourceAssetSet, Required: true},
			"target":    {ResourceType: model.ResourceDataset, Required: true},
		},
		OutputPorts: map[string]model.PortDefinition{
			"batch": {ResourceType: model.ResourceDatasetBatch},
		},
		OutputBindings: map[string]string{"batch": "$step.publish_batch.batch"},
		Steps: []model.StepDefinition{
			{
				ID: "parse_documents", Name: "解析文档", Kind: model.StepKindSourceParse,
				Inputs:  map[string]string{"assets": "$task.documents"},
				Outputs: map[string]model.ResourceType{"documents": model.ResourceParsedDocuments},
			},
			{
				ID: "extract_records", Name: "抽取记录", Kind: model.StepKindLLMExtract,
				DependsOn: []string{"parse_documents"},
				Inputs:    map[string]string{"documents": "$step.parse_documents.documents"},
				Outputs:   map[string]model.ResourceType{"drafts": model.ResourceRecordDrafts},
				Config:    json.RawMessage(`{"profile_id":"p1"}`),
			},
			{
				ID: "refine_records", Name: "二次抽取", Kind: model.StepKindLLMExtract,
				DependsOn: []string{"extract_records"},
				Inputs:    map[string]string{"drafts": "$step.extract_records.drafts"},
				Outputs:   map[string]model.ResourceType{"validation": model.ResourceValidationResults},
			},
			{
				ID: "review_records", Name: "人工审核", Kind: model.StepKindHumanReview,
				DependsOn: []string{"refine_records"},
				Inputs:    map[string]string{"validation": "$step.refine_records.validation"},
				Outputs:   map[string]model.ResourceType{"approved": model.ResourceApprovedRecords},
			},
			{
				ID: "publish_batch", Name: "提交批次", Kind: model.StepKindDataPublish,
				DependsOn: []string{"review_records"},
				Inputs:    map[string]string{"approved": "$step.review_records.approved"},
				Outputs:   map[string]model.ResourceType{"batch": model.ResourceDatasetBatch},
			},
		},
	}
}

func TestValidateTaskDefinitionAllowsRepeatedKindsAndTypedPorts(t *testing.T) {
	def := validTaskDefinition()
	if err := ValidateTaskDefinition(def); err != nil {
		t.Fatalf("合法定义应通过: %v", err)
	}
	order, err := TaskDefinitionOrder(def)
	if err != nil {
		t.Fatalf("拓扑排序失败: %v", err)
	}
	want := []string{"parse_documents", "extract_records", "refine_records", "review_records", "publish_batch"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("order=%v, want=%v", order, want)
	}
}

func TestValidateTaskDefinitionAllowsBusinessAnalysisWorkflow(t *testing.T) {
	def := model.TaskDefinition{
		Key:  "bug_analysis",
		Name: "Bug 分析",
		InputPorts: map[string]model.PortDefinition{
			"knowledge": {ResourceType: model.ResourceRetrievalSnapshot, Required: true},
			"target":    {ResourceType: model.ResourceDatasetBoundary, Required: true},
		},
		OutputPorts: map[string]model.PortDefinition{
			"batch":    {ResourceType: model.ResourceDatasetBatch},
			"artifact": {ResourceType: model.ResourceArtifact},
		},
		OutputBindings: map[string]string{
			"batch":    "$step.publish.batch",
			"artifact": "$step.render.artifact",
		},
		Steps: []model.StepDefinition{
			{
				ID: "analyze", Name: "结构化分析", Kind: model.StepKindAgentAnalyze,
				Inputs:  map[string]string{"knowledge": "$task.knowledge"},
				Outputs: map[string]model.ResourceType{"analysis": model.ResourceAnalysisResult},
			},
			{
				ID: "review", Name: "人工确认", Kind: model.StepKindHumanReview,
				DependsOn: []string{"analyze"},
				Inputs:    map[string]string{"analysis": "$step.analyze.analysis"},
				Outputs:   map[string]model.ResourceType{"analysis": model.ResourceAnalysisResult},
			},
			{
				ID: "publish", Name: "发布分析记录", Kind: model.StepKindAnalysisPublish,
				DependsOn: []string{"review"},
				Inputs: map[string]string{
					"analysis": "$step.review.analysis",
					"target":   "$task.target",
				},
				Outputs: map[string]model.ResourceType{"batch": model.ResourceDatasetBatch},
			},
			{
				ID: "render", Name: "生成制品", Kind: model.StepKindArtifactRender,
				DependsOn: []string{"review"},
				Inputs:    map[string]string{"analysis": "$step.review.analysis"},
				Outputs:   map[string]model.ResourceType{"artifact": model.ResourceArtifact},
			},
		},
	}

	if err := ValidateTaskDefinition(def); err != nil {
		t.Fatalf("阶段 F 业务分析定义应通过: %v", err)
	}
}

func TestValidateTaskDefinitionRejectsCycle(t *testing.T) {
	def := validTaskDefinition()
	def.Steps[0].DependsOn = []string{"publish_batch"}
	err := ValidateTaskDefinition(def)
	if err == nil || !strings.Contains(err.Error(), "存在环") {
		t.Fatalf("期望环错误，got %v", err)
	}
}

func TestValidateTaskDefinitionRejectsNonAncestorReference(t *testing.T) {
	def := validTaskDefinition()
	def.Steps[1].DependsOn = nil
	err := ValidateTaskDefinition(def)
	if err == nil || !strings.Contains(err.Error(), "依赖祖先") {
		t.Fatalf("期望祖先引用错误，got %v", err)
	}
}

func TestValidateTaskDefinitionRejectsOutputTypeMismatch(t *testing.T) {
	def := validTaskDefinition()
	def.OutputPorts["batch"] = model.PortDefinition{ResourceType: model.ResourceArtifact}
	err := ValidateTaskDefinition(def)
	if err == nil || !strings.Contains(err.Error(), "类型") {
		t.Fatalf("期望类型错误，got %v", err)
	}
}

func TestValidateTaskDefinitionRejectsUnknownExecutor(t *testing.T) {
	def := validTaskDefinition()
	def.Steps[0].Kind = model.StepKind("shell.exec")
	err := ValidateTaskDefinition(def)
	if err == nil || !strings.Contains(err.Error(), "执行器类型非法") {
		t.Fatalf("期望执行器错误，got %v", err)
	}
}
