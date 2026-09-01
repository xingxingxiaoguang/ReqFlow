package main

// v1 交付种子：业务开箱即用的示例知识库「DH1功能&需求知识库」。
// 四件套（数据结构 → 抽取规则 → 索引规则 → 数据集）全部按 workspace + name
// 幂等：同名资源已存在（含业务后续改动）一律跳过，绝不覆盖或重复创建。
// 数据集本身不带任何数据条目，业务数据由清洗任务发布产生。

import (
	"context"
	"encoding/json"
	"fmt"

	appcatalog "reqflow/internal/app/catalog"
	appextraction "reqflow/internal/app/extraction"
	apppipeline "reqflow/internal/app/pipeline"
	appretrieval "reqflow/internal/app/retrieval"
	"reqflow/internal/domain/model"
)

const (
	starterWorkspace      = "default"
	starterSchemaName     = "HD1-功能原文"
	starterExtractionName = "DH1文档数据清洗"
	starterRetrievalName  = "HD1-语义索引"
	starterDatasetName    = "DH1功能&需求知识库"
)

var starterSchemaJSON = json.RawMessage(`{"type":"object","title":"HD1-功能原文","required":["title","key_word","description"],"properties":{"title":{"type":"string","title":"唯一标识","description":"每条数据的稳定唯一编号，采用文档名(简写不超过8个字符)-功能名（简写不超过8个字符）。例如：\"车辆控制规范-座椅前后调节\""},"key_word":{"type":"string","title":"关键字","description":"提取功能描述中独特的关键字，至少3个至多5个，逗号分隔。例如：\"车辆控制,电动座椅,前后调节,15cm\""},"description":{"type":"string","title":"功能描述","description":"提取文档中功能点描述的原文，若有跨章节的多处描述用换行符拼接全部写到这个字段中。注意：这是正确性非常敏感的数据，一定不要改写其中的描述、数字、单位。"}},"additionalProperties":false}`)

// seedStarterKit 返回本次实际创建的资源名，便于启动日志确认。
func seedStarterKit(ctx context.Context, datasets *apppipeline.DatasetService, extraction *appextraction.Service,
	retrieval *appretrieval.Service, catalog *appcatalog.Service) ([]string, error) {
	var created []string

	schemaExists := false
	schemas, err := catalog.ListSchemas(ctx, appcatalog.Query{WorkspaceID: starterWorkspace, Limit: 200})
	if err != nil {
		return nil, fmt.Errorf("读取数据结构: %w", err)
	}
	for _, item := range schemas {
		if item.Name == starterSchemaName {
			schemaExists = true
			break
		}
	}
	schemaID := ""
	if schemaExists {
		for _, item := range schemas {
			if item.Name == starterSchemaName {
				schemaID = item.ID
				break
			}
		}
	} else {
		schema, err := datasets.CreateSchema(ctx, apppipeline.CreateSchemaInput{
			WorkspaceID: starterWorkspace, Name: starterSchemaName, Description: "功能原文",
			JSONSchema: starterSchemaJSON, UISchema: json.RawMessage(`{}`),
		})
		if err != nil {
			return nil, fmt.Errorf("种子数据结构 %s: %w", starterSchemaName, err)
		}
		schemaID = schema.ID
		created = append(created, "结构:"+starterSchemaName)
	}

	extractionExists := false
	profiles, err := catalog.ListExtractionProfiles(ctx, appcatalog.Query{WorkspaceID: starterWorkspace, Limit: 200})
	if err != nil {
		return nil, fmt.Errorf("读取抽取规则: %w", err)
	}
	for _, item := range profiles {
		if item.Name == starterExtractionName {
			extractionExists = true
			break
		}
	}
	if !extractionExists {
		if _, err := extraction.CreateProfile(ctx, appextraction.CreateExtractionProfileInput{
			WorkspaceID: starterWorkspace, Name: starterExtractionName, TargetSchemaID: schemaID,
			RecordGranularity: "每个功能点或需求点作为一条记录。",
			SystemInstruction: "你需要将文档原文中语义化描述功能和需求的部分提取成规定的结构化数据，用来作为产品规格说明书。" +
				"注意：忽略文档中功能描述和需求描述不相关的点，忽略文档中大段有关Vspec 信号的描述，这不属于可以语义话表达的功能和需求。",
			FieldGuides: json.RawMessage(`{}`), Examples: json.RawMessage(`[]`),
			NormalizationRules: json.RawMessage(`[]`), ValidationRules: json.RawMessage(`[]`),
		}); err != nil {
			return nil, fmt.Errorf("种子抽取规则 %s: %w", starterExtractionName, err)
		}
		created = append(created, "抽取规则:"+starterExtractionName)
	}

	retrievalExists := false
	retrievalProfiles, err := retrieval.ListProfileViews(ctx, starterWorkspace, "", 200)
	if err != nil {
		return nil, fmt.Errorf("读取索引规则: %w", err)
	}
	for _, item := range retrievalProfiles {
		if item.Name == starterRetrievalName {
			retrievalExists = true
			break
		}
	}
	if !retrievalExists {
		if _, err := retrieval.CreateProfile(ctx, appretrieval.CreateProfileInput{
			WorkspaceID: starterWorkspace, Name: starterRetrievalName, DatasetSchemaID: schemaID,
			Lexical:     model.LexicalConfig{Fields: map[string]float64{"key_word": 1, "description": 1}, Analyzer: "standard"},
			Vector:      model.VectorConfig{Fields: []string{"description", "key_word"}, ChunkSize: 800,
				ChunkOverlap: 100, ChunkerVersion: "rune_v1", EmbeddingModel: "platform_default"},
			FilterFields: []string{"title"},
			Fusion:       model.FusionConfig{Method: "rrf", RankConstant: 60, LexicalCandidates: 100, VectorCandidates: 100},
		}); err != nil {
			return nil, fmt.Errorf("种子索引规则 %s: %w", starterRetrievalName, err)
		}
		created = append(created, "索引规则:"+starterRetrievalName)
	}

	datasetExists := false
	datasetList, err := catalog.ListDatasets(ctx, appcatalog.Query{WorkspaceID: starterWorkspace, Limit: 200})
	if err != nil {
		return nil, fmt.Errorf("读取数据集: %w", err)
	}
	for _, item := range datasetList {
		if item.Name == starterDatasetName {
			datasetExists = true
			break
		}
	}
	if !datasetExists {
		if _, err := datasets.CreateDataset(ctx, apppipeline.CreateDatasetInput{
			WorkspaceID: starterWorkspace, Name: starterDatasetName, Description: "DH1产品功能+需求说明书",
			Purpose: model.DatasetPurposeQuery, SchemaID: schemaID, KeyFields: []string{"title", "key_word"},
		}); err != nil {
			return nil, fmt.Errorf("种子数据集 %s: %w", starterDatasetName, err)
		}
		created = append(created, "数据集:"+starterDatasetName)
	}

	return created, nil
}
