// Package workflow exposes the application-facing catalog for the greenfield
// linear workflow system. It does not adapt or expose legacy StepKind values.
package workflow

import (
	"encoding/json"

	domain "reqflow/internal/domain/workflow"
)

func BuiltinCatalog() (*domain.StaticCatalog, error) {
	return domain.NewStaticCatalog(
		capability("source.parse", "解析文件", "把文件集解析为可追溯的文档集合。",
			primaryInput("assets", "待解析文件", domain.ResourceAssetSet),
			primaryOutput("documents", "解析文档", domain.ResourceParsedDocuments)),
		withRules(capability("document.extract", "结构化抽取", "按内联数据合同和抽取规则生成结构化草稿。",
			primaryInput("documents", "解析文档", domain.ResourceParsedDocuments),
			primaryOutput("drafts", "结构化草稿", domain.ResourceRecordDrafts)),
			domain.RuleDataContract, domain.RuleExtraction),
		withRules(capability("data.transform", "确定性清洗", "执行内联归一化规则并保留字段变更。",
			primaryInput("drafts", "结构化草稿", domain.ResourceRecordDrafts),
			primaryOutput("records", "清洗后记录", domain.ResourceTransformedRecords)),
			domain.RuleDataContract),
		withSideInput(withRules(capability("data.validate", "数据校验", "按数据合同和业务规则校验记录。",
			primaryInput("records", "清洗后记录", domain.ResourceTransformedRecords),
			primaryOutput("validation", "校验结果", domain.ResourceValidationResults)),
			domain.RuleDataContract), sideInput("dataset", "目标数据集", domain.ResourceDataset)),
		capability("human.review_records", "人工审核记录", "人工确认、修改或排除校验结果。",
			primaryInput("validation", "校验结果", domain.ResourceValidationResults),
			primaryOutput("approved", "审核通过记录", domain.ResourceApprovedRecords)),
		withDelivery(withSideEffects(capability("data.publish", "发布数据", "原子发布审核通过记录，并交付数据集边界与批次。",
			primaryInput("approved", "审核通过记录", domain.ResourceApprovedRecords),
			primaryOutput("dataset", "发布后数据集", domain.ResourceDatasetBoundary))),
			deliveryOutput("batch", "数据批次", domain.ResourceDatasetBatch)),
		withRules(capability("retrieval.build", "构建检索索引", "按内联搜索规则构建可复现检索快照。",
			primaryInput("dataset", "数据集边界", domain.ResourceDatasetBoundary),
			primaryOutput("snapshot", "检索快照", domain.ResourceRetrievalSnapshot)),
			domain.RuleDataContract, domain.RuleSearch),
		withRules(capability("knowledge.analyze", "知识分析", "基于固定知识快照生成符合输出合同的分析结果。",
			primaryInput("knowledge", "知识快照", domain.ResourceRetrievalSnapshot),
			primaryOutput("analysis", "分析结果", domain.ResourceAnalysisResult)),
			domain.RuleOutputContract),
		capability("human.approve_analysis", "人工确认分析", "人工确认或编辑结构化分析结果后继续。",
			primaryInput("analysis", "分析结果", domain.ResourceAnalysisResult),
			primaryOutput("approved", "已确认分析", domain.ResourceAnalysisResult)),
		withSideEffects(withRules(capability("artifact.render", "生成业务制品", "把分析结果固化为可查看和下载的业务制品。",
			primaryInput("analysis", "分析结果", domain.ResourceAnalysisResult),
			primaryOutput("artifact", "业务制品", domain.ResourceArtifact)),
			domain.RuleOutputContract)),
	)
}

func capability(kind, label, description string, input, output domain.PortDefinition) domain.CapabilityDefinition {
	definition := domain.CapabilityDefinition{
		Ref: domain.CapabilityRef{Kind: kind, Version: 1}, Label: label, Description: description,
		Inputs: []domain.PortDefinition{input}, Outputs: []domain.PortDefinition{output},
		ConfigSchema:  json.RawMessage(`{"type":"object","additionalProperties":false}`),
		DefaultConfig: json.RawMessage(`{}`),
	}
	if kind == "document.extract" || kind == "knowledge.analyze" {
		definition.RequiresLLM = true
		definition.ManualCompletion = true
	}
	return definition
}

func primaryInput(name, label string, resourceType domain.ResourceType) domain.PortDefinition {
	return domain.PortDefinition{Name: name, Label: label, ResourceType: resourceType,
		Role: domain.PortPrimary, Required: true}
}

func sideInput(name, label string, resourceType domain.ResourceType) domain.PortDefinition {
	return domain.PortDefinition{Name: name, Label: label, ResourceType: resourceType,
		Role: domain.PortSide, Required: true}
}

func primaryOutput(name, label string, resourceType domain.ResourceType) domain.PortDefinition {
	return domain.PortDefinition{Name: name, Label: label, ResourceType: resourceType,
		Role: domain.PortPrimary, Required: true}
}

func deliveryOutput(name, label string, resourceType domain.ResourceType) domain.PortDefinition {
	return domain.PortDefinition{Name: name, Label: label, ResourceType: resourceType,
		Role: domain.PortDelivery, Required: true}
}

func withRules(definition domain.CapabilityDefinition, rules ...domain.RuleSection) domain.CapabilityDefinition {
	definition.RuleRequirements = append([]domain.RuleSection(nil), rules...)
	return definition
}

func withSideInput(definition domain.CapabilityDefinition, port domain.PortDefinition) domain.CapabilityDefinition {
	definition.Inputs = append(definition.Inputs, port)
	return definition
}

func withDelivery(definition domain.CapabilityDefinition, port domain.PortDefinition) domain.CapabilityDefinition {
	definition.Outputs = append(definition.Outputs, port)
	return definition
}

func withSideEffects(definition domain.CapabilityDefinition) domain.CapabilityDefinition {
	definition.HasSideEffects = true
	return definition
}
