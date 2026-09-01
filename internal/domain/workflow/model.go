// Package workflow defines the greenfield ReqFlow workflow model.
//
// It intentionally has no dependency on the legacy TaskDefinition/orchestrator
// model. A workflow is authored as capability instances plus explicit
// connections, and published as a self-contained immutable revision.
package workflow

import (
	"encoding/json"
	"fmt"
	"time"
)

type ResourceType string

const (
	ResourceAssetSet           ResourceType = "asset_set"
	ResourceParsedDocuments    ResourceType = "parsed_documents"
	ResourceRecordDrafts       ResourceType = "record_drafts"
	ResourceTransformedRecords ResourceType = "transformed_records"
	ResourceValidationResults  ResourceType = "validation_results"
	ResourceApprovedRecords    ResourceType = "approved_records"
	ResourceDataset            ResourceType = "dataset"
	ResourceDatasetBoundary    ResourceType = "dataset_boundary"
	ResourceDatasetBatch       ResourceType = "dataset_batch"
	ResourceRetrievalSnapshot  ResourceType = "retrieval_snapshot"
	ResourceAnalysisResult     ResourceType = "analysis_result"
	ResourceArtifact           ResourceType = "artifact"
)

type CapabilityRef struct {
	Kind    string `json:"kind"`
	Version int    `json:"version"`
}

type PortRole string

const (
	// PortPrimary participates in the single node-to-node data chain.
	PortPrimary PortRole = "primary"
	// PortSide is supplied by a workflow input and never creates a branch.
	PortSide PortRole = "side"
	// PortDelivery is a node result that may be exposed as a workflow output but
	// is not used to continue the primary chain in linear.v1.
	PortDelivery PortRole = "delivery"
)

type PortDefinition struct {
	Name         string       `json:"name"`
	Label        string       `json:"label"`
	ResourceType ResourceType `json:"resource_type"`
	Role         PortRole     `json:"role"`
	Required     bool         `json:"required"`
	Multiple     bool         `json:"multiple,omitempty"`
	Description  string       `json:"description,omitempty"`
}

type CapabilityDefinition struct {
	Ref              CapabilityRef    `json:"ref"`
	Label            string           `json:"label"`
	Description      string           `json:"description"`
	Inputs           []PortDefinition `json:"inputs"`
	Outputs          []PortDefinition `json:"outputs"`
	RuleRequirements []RuleSection    `json:"rule_requirements,omitempty"`
	ConfigSchema     json.RawMessage  `json:"config_schema,omitempty"`
	DefaultConfig    json.RawMessage  `json:"default_config,omitempty"`
	HasSideEffects   bool             `json:"has_side_effects,omitempty"`
	RequiresLLM      bool             `json:"requires_llm,omitempty"`
	ManualCompletion bool             `json:"manual_completion,omitempty"`
}

type RuleSection string

const (
	RuleDataContract   RuleSection = "data_contract"
	RuleExtraction     RuleSection = "extraction"
	RuleSearch         RuleSection = "search"
	RuleOutputContract RuleSection = "output_contract"
)

type WorkflowPort struct {
	Name         string       `json:"name"`
	Label        string       `json:"label"`
	ResourceType ResourceType `json:"resource_type"`
	Required     bool         `json:"required"`
	Description  string       `json:"description,omitempty"`
}

type EndpointKind string

const (
	EndpointWorkflowInput  EndpointKind = "workflow_input"
	EndpointNodeOutput     EndpointKind = "node_output"
	EndpointNodeInput      EndpointKind = "node_input"
	EndpointWorkflowOutput EndpointKind = "workflow_output"
)

type Endpoint struct {
	Kind   EndpointKind `json:"kind"`
	NodeID string       `json:"node_id,omitempty"`
	Port   string       `json:"port"`
}

type Connection struct {
	From Endpoint `json:"from"`
	To   Endpoint `json:"to"`
}

type WorkflowNode struct {
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Capability CapabilityRef   `json:"capability"`
	Config     json.RawMessage `json:"config,omitempty"`
}

type FieldType string

const (
	FieldString   FieldType = "string"
	FieldInteger  FieldType = "integer"
	FieldNumber   FieldType = "number"
	FieldBoolean  FieldType = "boolean"
	FieldDate     FieldType = "date"
	FieldDateTime FieldType = "datetime"
	FieldObject   FieldType = "object"
	FieldArray    FieldType = "array"
)

type FieldContract struct {
	Key         string          `json:"key"`
	Label       string          `json:"label"`
	Description string          `json:"description,omitempty"`
	Type        FieldType       `json:"type"`
	Required    bool            `json:"required,omitempty"`
	Enum        []string        `json:"enum,omitempty"`
	Items       *FieldContract  `json:"items,omitempty"`
	Properties  []FieldContract `json:"properties,omitempty"`
}

type DataContract struct {
	RecordGranularity string          `json:"record_granularity"`
	KeyFields         []string        `json:"key_fields"`
	Fields            []FieldContract `json:"fields"`
}

type FieldGuide struct {
	Description  string   `json:"description"`
	Examples     []string `json:"examples,omitempty"`
	EvidenceOnly bool     `json:"evidence_only,omitempty"`
}

type NormalizationOperation string

const (
	NormalizeEnumAlias    NormalizationOperation = "enum_alias"
	NormalizeBooleanAlias NormalizationOperation = "boolean_alias"
	NormalizeDate         NormalizationOperation = "date"
	NormalizeUnitScale    NormalizationOperation = "unit_scale"
	NormalizeSplit        NormalizationOperation = "split"
	NormalizeConcat       NormalizationOperation = "concat"
)

type NormalizationRule struct {
	ID           string                 `json:"id"`
	Description  string                 `json:"description"`
	Field        string                 `json:"field"`
	Operation    NormalizationOperation `json:"operation"`
	Aliases      map[string]any         `json:"aliases,omitempty"`
	TrueValues   []string               `json:"true_values,omitempty"`
	FalseValues  []string               `json:"false_values,omitempty"`
	Layouts      []string               `json:"layouts,omitempty"`
	Units        map[string]float64     `json:"units,omitempty"`
	Separator    string                 `json:"separator,omitempty"`
	SourceFields []string               `json:"source_fields,omitempty"`
}

type ValidationOperation string

const (
	ValidateRequired ValidationOperation = "required"
	ValidateRegex    ValidationOperation = "regex"
	ValidateRange    ValidationOperation = "range"
	ValidateLength   ValidationOperation = "length"
	ValidateOneOf    ValidationOperation = "one_of"
	ValidateCompare  ValidationOperation = "compare"
)

type ValidationRule struct {
	ID          string              `json:"id"`
	Description string              `json:"description"`
	Field       string              `json:"field"`
	Operation   ValidationOperation `json:"operation"`
	Severity    IssueSeverity       `json:"severity,omitempty"`
	Message     string              `json:"message,omitempty"`
	Pattern     string              `json:"pattern,omitempty"`
	Minimum     *float64            `json:"minimum,omitempty"`
	Maximum     *float64            `json:"maximum,omitempty"`
	MinLength   *int                `json:"min_length,omitempty"`
	MaxLength   *int                `json:"max_length,omitempty"`
	Values      []any               `json:"values,omitempty"`
	OtherField  string              `json:"other_field,omitempty"`
	Operator    string              `json:"operator,omitempty"`
}

type ExtractionSpec struct {
	Instruction        string                `json:"instruction"`
	FieldGuides        map[string]FieldGuide `json:"field_guides,omitempty"`
	NormalizationRules []NormalizationRule   `json:"normalization_rules,omitempty"`
	ValidationRules    []ValidationRule      `json:"validation_rules,omitempty"`
}

type WeightedField struct {
	Field  string  `json:"field"`
	Weight float64 `json:"weight"`
}

type SearchPreset string

const (
	SearchBalanced      SearchPreset = "balanced"
	SearchPrecise       SearchPreset = "precise"
	SearchSemantic      SearchPreset = "semantic"
	SearchFilterFocused SearchPreset = "filter_focused"
)

type SearchSpec struct {
	Preset            SearchPreset    `json:"preset"`
	LexicalFields     []WeightedField `json:"lexical_fields"`
	VectorFields      []string        `json:"vector_fields"`
	FilterFields      []string        `json:"filter_fields,omitempty"`
	ChunkSize         int             `json:"chunk_size"`
	ChunkOverlap      int             `json:"chunk_overlap"`
	LexicalCandidates int             `json:"lexical_candidates"`
	VectorCandidates  int             `json:"vector_candidates"`
}

type OutputContract struct {
	Fields []FieldContract `json:"fields"`
}

type DecisionSource string

const (
	DecisionObserved      DecisionSource = "observed"
	DecisionInferred      DecisionSource = "inferred"
	DecisionPreset        DecisionSource = "preset"
	DecisionUserConfirmed DecisionSource = "user_confirmed"
	DecisionUserDefined   DecisionSource = "user_defined"
)

type DecisionRisk string

const (
	RiskLow  DecisionRisk = "low"
	RiskHigh DecisionRisk = "high"
)

type EvidenceRef struct {
	ResourceID string `json:"resource_id"`
	Location   string `json:"location"`
	Quote      string `json:"quote,omitempty"`
}

type RuleDecision struct {
	Path        string          `json:"path"`
	Value       json.RawMessage `json:"value"`
	Source      DecisionSource  `json:"source"`
	Risk        DecisionRisk    `json:"risk"`
	Confidence  float64         `json:"confidence,omitempty"`
	Reason      string          `json:"reason"`
	Evidence    []EvidenceRef   `json:"evidence,omitempty"`
	ConfirmedBy string          `json:"confirmed_by,omitempty"`
	ConfirmedAt time.Time       `json:"confirmed_at,omitempty"`
}

type RuleBundle struct {
	DataContract   *DataContract   `json:"data_contract,omitempty"`
	Extraction     *ExtractionSpec `json:"extraction,omitempty"`
	Search         *SearchSpec     `json:"search,omitempty"`
	OutputContract *OutputContract `json:"output_contract,omitempty"`
	Decisions      []RuleDecision  `json:"decisions,omitempty"`
}

type AcceptanceCase struct {
	ID                 string          `json:"id"`
	Name               string          `json:"name"`
	Input              json.RawMessage `json:"input"`
	Expectation        json.RawMessage `json:"expectation"`
	LastPassed         bool            `json:"last_passed"`
	LastPassedRevision int64           `json:"last_passed_revision,omitempty"`
	LastPreviewID      string          `json:"last_preview_id,omitempty"`
	LastRunAt          time.Time       `json:"last_run_at,omitempty"`
}

type WorkflowDraft struct {
	ID              string           `json:"id"`
	WorkspaceID     string           `json:"workspace_id"`
	Key             string           `json:"key"`
	Name            string           `json:"name"`
	Description     string           `json:"description,omitempty"`
	Revision        int64            `json:"revision"`
	Inputs          []WorkflowPort   `json:"inputs"`
	Outputs         []WorkflowPort   `json:"outputs"`
	Nodes           []WorkflowNode   `json:"nodes"`
	Connections     []Connection     `json:"connections"`
	Rules           RuleBundle       `json:"rules"`
	AcceptanceCases []AcceptanceCase `json:"acceptance_cases,omitempty"`
	CreatedAt       time.Time        `json:"created_at,omitempty"`
	UpdatedAt       time.Time        `json:"updated_at,omitempty"`
}

type ResolvedNode struct {
	ID         string               `json:"id"`
	Name       string               `json:"name"`
	Capability CapabilityDefinition `json:"capability"`
	Config     json.RawMessage      `json:"config,omitempty"`
}

type WorkflowRevision struct {
	ID              string           `json:"id"`
	WorkflowID      string           `json:"workflow_id"`
	RevisionNo      int64            `json:"revision_no"`
	WorkspaceID     string           `json:"workspace_id"`
	Key             string           `json:"key"`
	Name            string           `json:"name"`
	Description     string           `json:"description,omitempty"`
	Inputs          []WorkflowPort   `json:"inputs"`
	Outputs         []WorkflowPort   `json:"outputs"`
	Nodes           []ResolvedNode   `json:"nodes"`
	Connections     []Connection     `json:"connections"`
	Rules           RuleBundle       `json:"rules"`
	AcceptanceCases []AcceptanceCase `json:"acceptance_cases,omitempty"`
	ContentHash     string           `json:"content_hash"`
	PublishedBy     string           `json:"published_by"`
	PublishedAt     time.Time        `json:"published_at"`
}

type PreviewStatus string

const (
	PreviewPassed PreviewStatus = "passed"
	PreviewFailed PreviewStatus = "failed"
)

// TemporaryResourceID 生成 Preview 临时资源的确定性 ID。它只出现在
// OutputManifest 的 temporary 绑定里，数据库中不存在对应资源行。
func TemporaryResourceID(previewID, nodeID, port string) string {
	return fmt.Sprintf("preview:%s:nodes:%s:%s", previewID, nodeID, port)
}

type WorkflowPreview struct {
	ID             string            `json:"id"`
	WorkflowID     string            `json:"workflow_id"`
	DraftRevision  int64             `json:"draft_revision"`
	Status         PreviewStatus     `json:"status"`
	Input          json.RawMessage   `json:"input"`
	OutputManifest json.RawMessage   `json:"output_manifest"`
	Issues         []ValidationIssue `json:"issues,omitempty"`
	StartedBy      string            `json:"started_by"`
	StartedAt      time.Time         `json:"started_at"`
	FinishedAt     time.Time         `json:"finished_at,omitempty"`
	Temporary      bool              `json:"temporary"`
}
