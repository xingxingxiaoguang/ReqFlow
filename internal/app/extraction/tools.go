package extraction

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"reqflow/internal/app/agent"
	"reqflow/internal/domain/logic"
	"reqflow/internal/port"
)

const (
	extractionReadDefault = 6
	extractionReadMax     = 20
	extractionSearchMax   = 50
	extractionWriteMax    = 50
)

type extractionSourceRef struct {
	BlockID string `json:"block_id"`
	Quote   string `json:"quote"`
}

type extractionCandidate struct {
	DraftKey        string                `json:"draft_key"`
	Fields          map[string]any        `json:"fields"`
	FieldConfidence map[string]float64    `json:"field_confidence,omitempty"`
	SourceRefs      []extractionSourceRef `json:"source_refs"`
}

type extractionAgentState struct {
	Drafts         []extractionCandidate `json:"drafts,omitempty"`
	ReadSegmentIDs []string              `json:"read_segment_ids,omitempty"`
	Validated      bool                  `json:"validated,omitempty"`
	Finished       bool                  `json:"finished,omitempty"`
	Outcome        string                `json:"outcome,omitempty"`

	draftIndex map[string]int
	read       map[string]bool
}

func (s *extractionAgentState) prepare() {
	s.draftIndex = make(map[string]int, len(s.Drafts))
	for i := range s.Drafts {
		s.draftIndex[s.Drafts[i].DraftKey] = i
	}
	s.read = make(map[string]bool, len(s.ReadSegmentIDs))
	for _, id := range s.ReadSegmentIDs {
		s.read[id] = true
	}
}

func (s *extractionAgentState) markRead(ids ...string) {
	if s.read == nil {
		s.prepare()
	}
	for _, id := range ids {
		if s.read[id] {
			continue
		}
		s.read[id] = true
		s.ReadSegmentIDs = append(s.ReadSegmentIDs, id)
		s.Validated = false
	}
}

func (s *extractionAgentState) upsert(candidate extractionCandidate) bool {
	if s.draftIndex == nil {
		s.prepare()
	}
	if index, ok := s.draftIndex[candidate.DraftKey]; ok {
		s.Drafts[index] = candidate
		s.Validated = false
		return false
	}
	s.draftIndex[candidate.DraftKey] = len(s.Drafts)
	s.Drafts = append(s.Drafts, candidate)
	s.Validated = false
	return true
}

func (s *extractionAgentState) delete(keys []string) (deleted, missing []string) {
	remove := make(map[string]bool, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if _, ok := s.draftIndex[key]; ok {
			remove[key] = true
			deleted = append(deleted, key)
		} else {
			missing = append(missing, key)
		}
	}
	if len(remove) == 0 {
		return deleted, missing
	}
	next := s.Drafts[:0]
	for _, draft := range s.Drafts {
		if !remove[draft.DraftKey] {
			next = append(next, draft)
		}
	}
	s.Drafts = next
	s.Validated = false
	s.prepare()
	return deleted, missing
}

type extractionToolBase struct {
	plan   plannedExtractionUnit
	state  *extractionAgentState
	fields map[string]struct{}
	schema json.RawMessage
}

type listSourceBlocksTool struct{ extractionToolBase }

func (*listSourceBlocksTool) Spec() port.ToolSpec {
	return port.ToolSpec{Name: "list_source_blocks",
		Description: "列出当前抽取单元负责的全部原文区块元数据，不返回正文。先调用它规划读取顺序；工具作用域已由服务端固定，不能传文档 ID。",
		Parameters:  json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)}
}

func (t *listSourceBlocksTool) Execute(_ context.Context, call port.ToolCall, _ func(string)) agent.ToolOutput {
	if err := decodeExtractionToolArgs(call.Arguments, &struct{}{}); err != nil {
		return extractionToolError("INVALID_ARGUMENTS", err.Error(), "请使用空 object 调用")
	}
	type item struct {
		SegmentID   string `json:"segment_id"`
		BlockID     string `json:"block_id"`
		Ordinal     int    `json:"ordinal"`
		BlockType   string `json:"block_type"`
		PageNo      int    `json:"page_no,omitempty"`
		SectionPath string `json:"section_path,omitempty"`
		Characters  int    `json:"characters"`
		Read        bool   `json:"read"`
	}
	items := make([]item, len(t.plan.blocks))
	for i, block := range t.plan.blocks {
		items[i] = item{SegmentID: block.SegmentID, BlockID: block.ID, Ordinal: block.Ordinal,
			BlockType: block.BlockType, PageNo: block.PageNo, SectionPath: block.SectionPath,
			Characters: len([]rune(block.Text)), Read: t.state.read[block.SegmentID]}
	}
	return extractionToolOK(map[string]any{"blocks": items, "total": len(items)},
		fmt.Sprintf("列出 %d 个来源区块", len(items)))
}

type readSourceBlocksTool struct{ extractionToolBase }

func (*readSourceBlocksTool) Spec() port.ToolSpec {
	return port.ToolSpec{Name: "read_source_blocks",
		Description: fmt.Sprintf("分页读取当前抽取单元的精确原文。offset 是 list_source_blocks 顺序中的 0 起位置；单次默认 %d 段、最多 %d 段。提交完成前必须读完全部区块。", extractionReadDefault, extractionReadMax),
		Parameters:  json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{"offset":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":1,"maximum":%d}},"additionalProperties":false}`, extractionReadMax))}
}

func (t *readSourceBlocksTool) Execute(_ context.Context, call port.ToolCall, _ func(string)) agent.ToolOutput {
	var args struct {
		Offset int `json:"offset"`
		Limit  int `json:"limit"`
	}
	if err := decodeExtractionToolArgs(call.Arguments, &args); err != nil {
		return extractionToolError("INVALID_ARGUMENTS", err.Error(), "使用 offset 和 limit 分页读取")
	}
	if args.Offset < 0 || args.Offset >= len(t.plan.blocks) {
		return extractionToolError("OFFSET_OUT_OF_RANGE",
			fmt.Sprintf("offset=%d 超出范围，当前共有 %d 段", args.Offset, len(t.plan.blocks)), "从 offset=0 开始读取")
	}
	if args.Limit <= 0 {
		args.Limit = extractionReadDefault
	}
	if args.Limit > extractionReadMax {
		args.Limit = extractionReadMax
	}
	end := args.Offset + args.Limit
	if end > len(t.plan.blocks) {
		end = len(t.plan.blocks)
	}
	blocks := append([]extractionBlock(nil), t.plan.blocks[args.Offset:end]...)
	ids := make([]string, len(blocks))
	for i := range blocks {
		ids[i] = blocks[i].SegmentID
	}
	t.state.markRead(ids...)
	next := any(nil)
	if end < len(t.plan.blocks) {
		next = end
	}
	return extractionToolOK(map[string]any{"blocks": blocks, "next_offset": next,
		"read": len(t.state.ReadSegmentIDs), "total": len(t.plan.blocks)},
		fmt.Sprintf("读取来源区块 %d-%d / %d", args.Offset+1, end, len(t.plan.blocks)))
}

type searchSourceBlocksTool struct{ extractionToolBase }

func (*searchSourceBlocksTool) Spec() port.ToolSpec {
	return port.ToolSpec{Name: "search_source_blocks",
		Description: "在当前抽取单元原文内做正则或字面量检索，返回稳定 segment_id、block_id 和命中上下文。检索仅用于定位，不能代替完整阅读。",
		Parameters:  json.RawMessage(fmt.Sprintf(`{"type":"object","properties":{"pattern":{"type":"string"},"literal":{"type":"boolean"},"ignore_case":{"type":"boolean"},"limit":{"type":"integer","minimum":1,"maximum":%d}},"required":["pattern"],"additionalProperties":false}`, extractionSearchMax))}
}

func (t *searchSourceBlocksTool) Execute(_ context.Context, call port.ToolCall, _ func(string)) agent.ToolOutput {
	var args struct {
		Pattern    string `json:"pattern"`
		Literal    bool   `json:"literal"`
		IgnoreCase bool   `json:"ignore_case"`
		Limit      int    `json:"limit"`
	}
	if err := decodeExtractionToolArgs(call.Arguments, &args); err != nil {
		return extractionToolError("INVALID_ARGUMENTS", err.Error(), "提供非空 pattern")
	}
	args.Pattern = strings.TrimSpace(args.Pattern)
	if args.Pattern == "" {
		return extractionToolError("EMPTY_PATTERN", "pattern 不能为空", "提供字段名、编号或原文关键词")
	}
	if args.Limit <= 0 || args.Limit > extractionSearchMax {
		args.Limit = 20
	}
	expression := args.Pattern
	if args.Literal {
		expression = regexp.QuoteMeta(expression)
	}
	if args.IgnoreCase {
		expression = "(?i)" + expression
	}
	re, err := regexp.Compile(expression)
	if err != nil {
		return extractionToolError("INVALID_PATTERN", err.Error(), "按字面量搜索时设置 literal=true")
	}
	type hit struct {
		SegmentID string `json:"segment_id"`
		BlockID   string `json:"block_id"`
		Ordinal   int    `json:"ordinal"`
		PageNo    int    `json:"page_no,omitempty"`
		Snippet   string `json:"snippet"`
	}
	var hits []hit
	truncated := false
	for _, block := range t.plan.blocks {
		location := re.FindStringIndex(block.Text)
		if location == nil {
			continue
		}
		if len(hits) >= args.Limit {
			truncated = true
			break
		}
		hits = append(hits, hit{SegmentID: block.SegmentID, BlockID: block.ID,
			Ordinal: block.Ordinal, PageNo: block.PageNo, Snippet: extractionSnippet(block.Text, location[0], location[1])})
	}
	return extractionToolOK(map[string]any{"hits": hits, "count": len(hits), "truncated": truncated},
		fmt.Sprintf("检索 %q：命中 %d 段", args.Pattern, len(hits)))
}

type upsertRecordDraftsTool struct {
	extractionToolBase
	schema json.RawMessage
}

func (t *upsertRecordDraftsTool) Spec() port.ToolSpec {
	parameters, err := extractionAgentWriteSchema(t.schema)
	if err != nil {
		parameters = json.RawMessage(`{"type":"object"}`)
	}
	return port.ToolSpec{Name: "upsert_record_drafts",
		Description: fmt.Sprintf("分批新增或修订结构化候选记录，单次最多 %d 条。同一 draft_key 再次提交会覆盖旧版本。每条都要包含当前原文中的精确 source_refs；回执会逐条接受或拒绝，请修正被拒条目后重交。", extractionWriteMax),
		Parameters:  parameters}
}

func (t *upsertRecordDraftsTool) Execute(_ context.Context, call port.ToolCall, _ func(string)) agent.ToolOutput {
	var args struct {
		Records []extractionCandidate `json:"records"`
	}
	if err := decodeExtractionToolArgs(call.Arguments, &args); err != nil {
		return extractionToolError("INVALID_ARGUMENTS", err.Error(), "按工具 Schema 提交 records 数组")
	}
	if len(args.Records) == 0 || len(args.Records) > extractionWriteMax {
		return extractionToolError("INVALID_RECORD_COUNT",
			fmt.Sprintf("records 必须包含 1..%d 条", extractionWriteMax), "拆成多次工具调用提交")
	}
	type rejection struct {
		Index    int    `json:"index"`
		DraftKey string `json:"draft_key,omitempty"`
		Code     string `json:"code"`
		Message  string `json:"message"`
		Hint     string `json:"hint,omitempty"`
	}
	accepted, updated := []string{}, []string{}
	var rejected []rejection
	seen := map[string]bool{}
	for i, candidate := range args.Records {
		candidate.DraftKey = strings.TrimSpace(candidate.DraftKey)
		if seen[candidate.DraftKey] {
			rejected = append(rejected, rejection{Index: i, DraftKey: candidate.DraftKey,
				Code: "DUPLICATE_DRAFT_KEY", Message: "同一次调用中 draft_key 重复", Hint: "合并为一条最终版本"})
			continue
		}
		seen[candidate.DraftKey] = true
		if code, message, hint := validateExtractionCandidate(candidate, t.fields, t.schema, t.plan.blocks); code != "" {
			rejected = append(rejected, rejection{Index: i, DraftKey: candidate.DraftKey,
				Code: code, Message: message, Hint: hint})
			continue
		}
		if _, exists := t.state.draftIndex[candidate.DraftKey]; !exists && len(t.state.Drafts) >= maxRecordsPerUnit {
			rejected = append(rejected, rejection{Index: i, DraftKey: candidate.DraftKey,
				Code: "DRAFT_LIMIT_REACHED", Message: fmt.Sprintf("单个抽取单元最多保留 %d 条草稿", maxRecordsPerUnit),
				Hint: "删除误提取或重复草稿后再新增"})
			continue
		}
		if t.state.upsert(candidate) {
			accepted = append(accepted, candidate.DraftKey)
		} else {
			updated = append(updated, candidate.DraftKey)
		}
	}
	receipt := map[string]any{"accepted": accepted, "updated": updated, "rejected": rejected,
		"total_drafts": len(t.state.Drafts)}
	return agent.ToolOutput{Output: compactExtractionJSON(receipt),
		Details: fmt.Sprintf("候选记录：新增 %d、修订 %d、拒绝 %d、累计 %d",
			len(accepted), len(updated), len(rejected), len(t.state.Drafts)),
		IsError: len(accepted)+len(updated) == 0 && len(rejected) > 0}
}

type listRecordDraftsTool struct{ extractionToolBase }

func (*listRecordDraftsTool) Spec() port.ToolSpec {
	return port.ToolSpec{Name: "list_record_drafts", Description: "分页查看当前已接受的全部候选草稿，核对多轮写入后的真实状态。",
		Parameters: json.RawMessage(`{"type":"object","properties":{"offset":{"type":"integer","minimum":0},"limit":{"type":"integer","minimum":1,"maximum":100}},"additionalProperties":false}`)}
}

func (t *listRecordDraftsTool) Execute(_ context.Context, call port.ToolCall, _ func(string)) agent.ToolOutput {
	var args struct {
		Offset int `json:"offset"`
		Limit  int `json:"limit"`
	}
	if err := decodeExtractionToolArgs(call.Arguments, &args); err != nil {
		return extractionToolError("INVALID_ARGUMENTS", err.Error(), "使用 offset 和 limit 分页")
	}
	if args.Offset < 0 || args.Offset > len(t.state.Drafts) {
		return extractionToolError("OFFSET_OUT_OF_RANGE", "offset 超出草稿范围", "从 offset=0 开始查看")
	}
	if args.Limit <= 0 || args.Limit > 100 {
		args.Limit = 20
	}
	end := args.Offset + args.Limit
	if end > len(t.state.Drafts) {
		end = len(t.state.Drafts)
	}
	next := any(nil)
	if end < len(t.state.Drafts) {
		next = end
	}
	return extractionToolOK(map[string]any{"records": t.state.Drafts[args.Offset:end],
		"next_offset": next, "total": len(t.state.Drafts)}, fmt.Sprintf("查看候选草稿 %d 条", end-args.Offset))
}

type deleteRecordDraftsTool struct{ extractionToolBase }

func (*deleteRecordDraftsTool) Spec() port.ToolSpec {
	return port.ToolSpec{Name: "delete_record_drafts", Description: "按 draft_key 删除误提取或重复草稿。重复删除安全，不存在的 key 会在回执中列出。",
		Parameters: json.RawMessage(`{"type":"object","properties":{"draft_keys":{"type":"array","minItems":1,"maxItems":100,"items":{"type":"string"}}},"required":["draft_keys"],"additionalProperties":false}`)}
}

func (t *deleteRecordDraftsTool) Execute(_ context.Context, call port.ToolCall, _ func(string)) agent.ToolOutput {
	var args struct {
		DraftKeys []string `json:"draft_keys"`
	}
	if err := decodeExtractionToolArgs(call.Arguments, &args); err != nil || len(args.DraftKeys) == 0 {
		if err == nil {
			err = fmt.Errorf("draft_keys 不能为空")
		}
		return extractionToolError("INVALID_ARGUMENTS", err.Error(), "提供要删除的 draft_key 数组")
	}
	deleted, missing := t.state.delete(args.DraftKeys)
	return extractionToolOK(map[string]any{"deleted": deleted, "missing": missing,
		"total_drafts": len(t.state.Drafts)}, fmt.Sprintf("删除 %d 条候选草稿", len(deleted)))
}

type validateRecordDraftsTool struct{ extractionToolBase }

func (*validateRecordDraftsTool) Spec() port.ToolSpec {
	return port.ToolSpec{Name: "validate_record_drafts", Description: "检查来源区块覆盖度、候选字段、原文引用和重复记录，不修改草稿。finish 前必须先调用。",
		Parameters: json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)}
}

func (t *validateRecordDraftsTool) Execute(_ context.Context, call port.ToolCall, _ func(string)) agent.ToolOutput {
	if err := decodeExtractionToolArgs(call.Arguments, &struct{}{}); err != nil {
		return extractionToolError("INVALID_ARGUMENTS", err.Error(), "请使用空 object 调用")
	}
	issues := extractionValidationIssues(t.plan, t.state, t.fields, t.schema)
	t.state.Validated = len(issues) == 0
	return agent.ToolOutput{Output: compactExtractionJSON(map[string]any{"valid": len(issues) == 0,
		"issues": issues, "draft_count": len(t.state.Drafts), "read_blocks": len(t.state.ReadSegmentIDs),
		"total_blocks": len(t.plan.blocks)}), Details: fmt.Sprintf("抽取校验：%d 个问题", len(issues)),
		IsError: len(issues) > 0}
}

type finishExtractionUnitTool struct{ extractionToolBase }

func (*finishExtractionUnitTool) Spec() port.ToolSpec {
	return port.ToolSpec{Name: "finish_extraction_unit",
		Description: "显式完成当前抽取单元。服务端会再次校验全部区块已读、草稿与来源合法；失败会返回可修正问题并继续运行，成功后才封存结果。",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"outcome":{"type":"string","enum":["records","no_records"]},"summary":{"type":"string","maxLength":1000}},"required":["outcome"],"additionalProperties":false}`)}
}

func (t *finishExtractionUnitTool) Execute(_ context.Context, call port.ToolCall, _ func(string)) agent.ToolOutput {
	var args struct {
		Outcome string `json:"outcome"`
		Summary string `json:"summary"`
	}
	if err := decodeExtractionToolArgs(call.Arguments, &args); err != nil {
		return extractionToolError("INVALID_ARGUMENTS", err.Error(), "outcome 只能是 records 或 no_records")
	}
	issues := extractionValidationIssues(t.plan, t.state, t.fields, t.schema)
	if !t.state.Validated {
		issues = append(issues, extractionValidationIssue{Code: "VALIDATION_REQUIRED",
			Message: "当前草稿状态尚未通过 validate_record_drafts", Hint: "先调用 validate_record_drafts 并修正全部问题"})
	}
	if args.Outcome == "records" && len(t.state.Drafts) == 0 {
		issues = append(issues, extractionValidationIssue{Code: "NO_DRAFTS",
			Message: "outcome=records 时至少需要一条候选草稿", Hint: "先调用 upsert_record_drafts"})
	}
	if args.Outcome == "no_records" && len(t.state.Drafts) != 0 {
		issues = append(issues, extractionValidationIssue{Code: "DRAFTS_NOT_EMPTY",
			Message: "outcome=no_records 但仍存在候选草稿", Hint: "改用 records 或先删除全部草稿"})
	}
	if args.Outcome != "records" && args.Outcome != "no_records" {
		issues = append(issues, extractionValidationIssue{Code: "INVALID_OUTCOME",
			Message: "outcome 只能是 records 或 no_records"})
	}
	if len(issues) > 0 {
		return agent.ToolOutput{Output: compactExtractionJSON(map[string]any{"finished": false, "issues": issues}),
			Details: fmt.Sprintf("完成校验未通过：%d 个问题", len(issues)), IsError: true}
	}
	t.state.Finished, t.state.Outcome = true, args.Outcome
	return agent.ToolOutput{Output: compactExtractionJSON(map[string]any{"finished": true,
		"outcome": args.Outcome, "draft_count": len(t.state.Drafts)}),
		Details: fmt.Sprintf("抽取单元已完成：%d 条候选记录", len(t.state.Drafts)), Terminate: true}
}

type extractionValidationIssue struct {
	Code     string `json:"code"`
	DraftKey string `json:"draft_key,omitempty"`
	Message  string `json:"message"`
	Hint     string `json:"hint,omitempty"`
}

func extractionValidationIssues(plan plannedExtractionUnit, state *extractionAgentState,
	fields map[string]struct{}, schema json.RawMessage) []extractionValidationIssue {
	var issues []extractionValidationIssue
	if len(state.Drafts) > maxRecordsPerUnit {
		issues = append(issues, extractionValidationIssue{Code: "TOO_MANY_DRAFTS",
			Message: fmt.Sprintf("当前有 %d 条草稿，单元上限为 %d", len(state.Drafts), maxRecordsPerUnit),
			Hint:    "删除误提取或重复草稿"})
	}
	for _, block := range plan.blocks {
		if !state.read[block.SegmentID] {
			issues = append(issues, extractionValidationIssue{Code: "UNREAD_SOURCE_BLOCK",
				Message: fmt.Sprintf("来源区块 %s 尚未读取", block.SegmentID),
				Hint:    "调用 read_source_blocks 继续阅读全文"})
		}
	}
	fingerprints := map[string]string{}
	for _, draft := range state.Drafts {
		if code, message, hint := validateExtractionCandidate(draft, fields, schema, plan.blocks); code != "" {
			issues = append(issues, extractionValidationIssue{Code: code, DraftKey: draft.DraftKey,
				Message: message, Hint: hint})
			continue
		}
		raw, _ := json.Marshal(draft.Fields)
		digest := sha256.Sum256(raw)
		fingerprint := hex.EncodeToString(digest[:])
		if existing := fingerprints[fingerprint]; existing != "" && existing != draft.DraftKey {
			issues = append(issues, extractionValidationIssue{Code: "DUPLICATE_RECORD",
				DraftKey: draft.DraftKey, Message: fmt.Sprintf("与草稿 %s 的字段完全相同", existing),
				Hint: "删除重复草稿或补充区分字段"})
		} else {
			fingerprints[fingerprint] = draft.DraftKey
		}
	}
	return issues
}

func validateExtractionCandidate(candidate extractionCandidate, fields map[string]struct{},
	schema json.RawMessage, blocks []extractionBlock) (code, message, hint string) {
	if candidate.DraftKey == "" || len(candidate.DraftKey) > 200 {
		return "INVALID_DRAFT_KEY", "draft_key 必须为 1..200 字符", "使用当前单元内稳定、简短的逻辑键"
	}
	if len(candidate.Fields) == 0 {
		return "EMPTY_FIELDS", "fields 不能为空", "根据目标 Schema 提取至少一个字段"
	}
	for field := range candidate.Fields {
		if _, ok := fields[field]; !ok {
			return "UNKNOWN_FIELD", fmt.Sprintf("目标 Schema 未声明字段 %s", field), "删除该字段或改用 Schema 中的字段名"
		}
	}
	for field, confidence := range candidate.FieldConfidence {
		if _, ok := candidate.Fields[field]; !ok {
			return "UNKNOWN_CONFIDENCE_FIELD", fmt.Sprintf("字段置信度引用了未提取字段 %s", field), "删除该置信度或补充对应字段"
		}
		if confidence < 0 || confidence > 1 {
			return "INVALID_CONFIDENCE", fmt.Sprintf("字段 %s 的置信度必须在 0..1", field), "修正为 0 到 1 之间的小数"
		}
	}
	fieldsRaw, err := json.Marshal(candidate.Fields)
	if err != nil {
		return "INVALID_FIELDS", err.Error(), "修正 fields 为可序列化的 JSON object"
	}
	_, schemaIssues, err := logic.ValidateTransformedRecord(schema, nil, fieldsRaw)
	if err != nil {
		return "SCHEMA_VALIDATION_FAILED", err.Error(), "按目标 Schema 修正 fields"
	}
	for _, issue := range schemaIssues {
		if issue.Severity == "error" {
			return "SCHEMA_INVALID", issue.Message, "补齐必填字段并修正字段类型或值域"
		}
	}
	if len(candidate.SourceRefs) == 0 {
		return "MISSING_SOURCE_REFS", "每条候选记录至少需要一个 source_ref", "从 read_source_blocks 返回的原文中复制连续引用"
	}
	seen := map[string]bool{}
	for _, reference := range candidate.SourceRefs {
		quote := strings.TrimSpace(reference.Quote)
		matched := false
		for _, block := range blocks {
			if block.ID == reference.BlockID && quote != "" && strings.Contains(block.Text, quote) {
				matched = true
				break
			}
		}
		if !matched {
			return "SOURCE_QUOTE_NOT_FOUND", fmt.Sprintf("block_id=%s 中找不到连续原文 quote", reference.BlockID),
				"重新调用 read_source_blocks，并逐字复制对应原文"
		}
		key := reference.BlockID + "\x00" + quote
		if seen[key] {
			return "DUPLICATE_SOURCE_REF", "候选记录包含重复 source_ref", "删除重复引用"
		}
		seen[key] = true
	}
	return "", "", ""
}

func extractionAgentWriteSchema(target json.RawMessage) (json.RawMessage, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(target, &root); err != nil {
		return nil, err
	}
	var properties map[string]json.RawMessage
	if err := json.Unmarshal(root["properties"], &properties); err != nil || len(properties) == 0 {
		return nil, fmt.Errorf("目标 JSON Schema 缺少 properties")
	}
	var required []string
	if len(root["required"]) > 0 {
		if err := json.Unmarshal(root["required"], &required); err != nil {
			return nil, fmt.Errorf("目标 JSON Schema required 非法: %w", err)
		}
	}
	fieldSchema := map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
	if len(required) > 0 {
		fieldSchema["required"] = required
	}
	parameters := map[string]any{"type": "object", "additionalProperties": false,
		"required": []string{"records"}, "properties": map[string]any{"records": map[string]any{
			"type": "array", "minItems": 1, "maxItems": extractionWriteMax,
			"items": map[string]any{"type": "object", "additionalProperties": false,
				"required": []string{"draft_key", "fields", "source_refs"}, "properties": map[string]any{
					"draft_key": map[string]any{"type": "string", "minLength": 1, "maxLength": 200},
					"fields":    fieldSchema,
					"field_confidence": map[string]any{"type": "object",
						"additionalProperties": map[string]any{"type": "number", "minimum": 0, "maximum": 1}},
					"source_refs": map[string]any{"type": "array", "minItems": 1,
						"items": map[string]any{"type": "object", "additionalProperties": false,
							"required": []string{"block_id", "quote"}, "properties": map[string]any{
								"block_id": map[string]any{"type": "string"}, "quote": map[string]any{"type": "string"}}}},
				},
			},
		}}}
	return json.Marshal(parameters)
}

func decodeExtractionToolArgs(raw json.RawMessage, destination any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage(`{}`)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return fmt.Errorf("参数只能包含一个 JSON object")
	}
	return nil
}

func extractionToolOK(payload any, details string) agent.ToolOutput {
	return agent.ToolOutput{Output: compactExtractionJSON(payload), Details: details}
}

func extractionToolError(code, message, hint string) agent.ToolOutput {
	return agent.ToolOutput{Output: compactExtractionJSON(map[string]any{"ok": false,
		"error": map[string]any{"code": code, "recoverable": true, "message": message, "hint": hint}}),
		Details: code + "：" + message, IsError: true}
}

func compactExtractionJSON(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

func extractionSnippet(text string, start, end int) string {
	const radius = 180
	runes := []rune(text)
	// regexp indexes are bytes; convert the matched prefix/suffix into rune offsets.
	runeStart := len([]rune(text[:start]))
	runeEnd := len([]rune(text[:end]))
	lo, hi := runeStart-radius, runeEnd+radius
	if lo < 0 {
		lo = 0
	}
	if hi > len(runes) {
		hi = len(runes)
	}
	return string(runes[lo:hi])
}
