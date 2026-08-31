package app

import "encoding/json"

func marshalJSON(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
