package logic

import "testing"

func TestIsValidIdentifier(t *testing.T) {
	for _, test := range []struct {
		value string
		valid bool
	}{
		{value: "record_id", valid: true},
		{value: "a1", valid: true},
		{value: "", valid: false},
		{value: "RecordID", valid: false},
		{value: "1record", valid: false},
		{value: "record-id", valid: false},
	} {
		if got := IsValidIdentifier(test.value); got != test.valid {
			t.Fatalf("IsValidIdentifier(%q) = %t, want %t", test.value, got, test.valid)
		}
	}
}
