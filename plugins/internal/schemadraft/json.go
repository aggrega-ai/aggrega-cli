// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import (
	"bytes"
	"encoding/json"
	"slices"
)

// JSONText reads raw as a JSON string, reporting whether it is one: a value of
// another type, or none, gives no text.
func JSONText(raw json.RawMessage) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '"' {
		return "", false
	}
	var s string
	if json.Unmarshal(trimmed, &s) != nil {
		return "", false
	}
	return s, true
}

// IsJSONObject reports whether raw is a JSON object.
func IsJSONObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{' && json.Valid(trimmed)
}

// Appended is path followed by name, sharing no storage with path, so the
// paths a reader hands to the members of one object never write into one
// another.
func Appended(path []string, name string) []string {
	return append(slices.Clone(path), name)
}
