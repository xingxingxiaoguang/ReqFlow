package analysis

import (
	"encoding/json"
	"testing"
)

func TestRenderArtifactContentSelectsPathAndKind(t *testing.T) {
	output := json.RawMessage(`{"report":"# 结论","data":{"score":9}}`)
	markdown, err := renderArtifactContent(output, "markdown", "$.report")
	if err != nil || string(markdown) != "# 结论" {
		t.Fatalf("markdown=%q err=%v", markdown, err)
	}
	structured, err := renderArtifactContent(output, "json", "$.data")
	if err != nil || string(structured) != "{\n  \"score\": 9\n}\n" {
		t.Fatalf("json=%q err=%v", structured, err)
	}
}

func TestRenderArtifactContentRejectsMissingPath(t *testing.T) {
	if _, err := renderArtifactContent(json.RawMessage(`{"report":"ok"}`), "markdown", "$.missing"); err == nil {
		t.Fatal("missing content path must fail")
	}
}
