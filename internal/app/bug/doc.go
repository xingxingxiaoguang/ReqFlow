// Package bug 为第二波「Bug 反馈处理链路」预留的用例占位。
// 第一波只落地骨架与扩展点，本包不含实现。
//
// 第二波设计（已定稿，接入时按此扩展，不动存量包）：
//
// 数据模型（新增迁移，不在第一波建表）：
//   - bug_batches:  id, file_name, source_path, status(imported|matched|confirmed|synced), created_at
//   - bug_rows:     id, batch_id, raw_json(原始行), 编号, 标题, 描述, 复现步骤等归一化字段,
//                   analyzed_priority(p0|p1|p2|p3), priority_rationale, status
//   - bug_matches:  id, bug_row_id, candidate_work_item_id, score, match_type(exact|semantic),
//                   rank(1-3), human_decision(confirmed|rejected|pending)
//
// 用例流（各为一个方法，与现有用例同构）：
//   1. ImportBatch:  Excel(xlsx 行级解析已就绪于 infra/parser.ParseXLSXRows) → bug_batches/bug_rows
//   2. MatchBatch:   有编号 → normalize 后与 work_items.identifier/标题精确匹配；
//                    无编号 → 语义匹配（复用 MatchService 的两层策略，取 top3 供人工确认）
//   3. ConfirmMatch: 人工确认/否决候选关联（human_decision）
//   4. Prioritize:   批量 LLM 定级 p0(阻塞性)/p1(核心功能性)/p2(次要功能性)/p3(边缘性趋近无效)
//   5. Sync:         确认关联的 bug 以 type_id=bug 建到平台，关联关系写入描述
//                    （关联需求写入字段「关联需求: <标题>」，以需求数据集为匹配底料）
package bug
