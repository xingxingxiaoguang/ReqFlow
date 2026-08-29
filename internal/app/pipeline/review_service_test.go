package pipeline

import (
	"encoding/json"
	"testing"

	"reqflow/internal/domain/model"
)

func TestNormalizeReviewInputKeepsEditedFieldsAndStableHash(t *testing.T) {
	left, leftHash, err := normalizeReviewInput("validation-1", ReviewRecordsInput{
		Reviewer: " 审核人 ", Rationale: " 已核对 ",
		Decisions: []ReviewDecisionInput{
			{ValidationResultID: "result-b", Action: model.ReviewActionExclude},
			{ValidationResultID: "result-a", Action: model.ReviewActionEdit,
				Fields: json.RawMessage(`{"name":"产品","sku":"A-1"}`)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(left.Decisions[0].Fields) != `{"name":"产品","sku":"A-1"}` {
		t.Fatalf("edit fields lost or not canonical: %s", left.Decisions[0].Fields)
	}
	_, rightHash, err := normalizeReviewInput("validation-1", ReviewRecordsInput{
		Reviewer: "审核人", Rationale: "已核对",
		Decisions: []ReviewDecisionInput{
			{ValidationResultID: "result-a", Action: model.ReviewActionEdit,
				Fields: json.RawMessage(`{ "sku": "A-1", "name": "产品" }`)},
			{ValidationResultID: "result-b", Action: model.ReviewActionExclude},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if leftHash != rightHash {
		t.Fatalf("equivalent review requests must share hash: %s != %s", leftHash, rightHash)
	}
}

func TestNormalizeReviewInputRejectsClientFieldsOutsideEdit(t *testing.T) {
	_, _, err := normalizeReviewInput("validation-1", ReviewRecordsInput{
		Reviewer: "审核人", Rationale: "已核对",
		Decisions: []ReviewDecisionInput{{ValidationResultID: "result-a",
			Action: model.ReviewActionApprove, Fields: json.RawMessage(`{"sku":"forged"}`)}},
	})
	if err == nil {
		t.Fatal("approve action must not smuggle edited fields")
	}
}
