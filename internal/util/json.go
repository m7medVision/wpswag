package util

import (
	"bytes"
	"encoding/json"
)

// CleanJSON removes UTF BOMs and any leading junk before '{' or '['.
func CleanJSON(b []byte) []byte {
	// UTF-8 BOM
	if len(b) >= 3 && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		b = b[3:]
	}
	// Trim common whitespace
	b = bytes.TrimLeft(b, "\r\n\t ")

	// Jump to first '{' or '[' in case of injected text
	if i := bytes.IndexAny(b, "{["); i > 0 {
		b = b[i:]
	}
	return b
}

// ParseArgs parses a WordPress args field. Accepts {}, [], or null.
func ParseArgs(raw json.RawMessage) map[string]any {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err == nil {
		return obj
	}
	var arr []any
	if err := json.Unmarshal(raw, &arr); err == nil {
		return map[string]any{}
	}
	return map[string]any{}
}
