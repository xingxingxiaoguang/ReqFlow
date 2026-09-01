package repository

import (
	"encoding/json"
	"testing"
)

func TestMarshalJSONBArrayNilIsEmptyArray(t *testing.T) {
	raw, err := marshalJSONBArray[string](nil)
	if err != nil || string(raw) != `[]` {
		t.Fatalf("nil slice must serialize as [] for jsonb typeof=array columns: %s err=%v", raw, err)
	}
	raw, err = marshalJSONBArray([]int{1, 2})
	if err != nil || string(raw) != `[1,2]` {
		t.Fatalf("non-empty slice must serialize as array: %s err=%v", raw, err)
	}
}

func TestRequireJSONBObjectMatchesDatabaseCheck(t *testing.T) {
	for name, value := range map[string]string{
		"null":    `null`,
		"array":   `[1]`,
		"string":  `"x"`,
		"number":  `1`,
		"invalid": `{`,
		"empty":   ``,
	} {
		if err := requireJSONBObject("fields", json.RawMessage(value)); err == nil {
			t.Fatalf("%s must be rejected like CHECK(jsonb_typeof='object'): %s", name, value)
		}
	}
	if err := requireJSONBObject("fields", json.RawMessage(` {"a":1} `)); err != nil {
		t.Fatalf("object with surrounding whitespace must pass: %v", err)
	}
}
