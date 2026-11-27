// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compactform

// RequiredField is a field a compiled pillar requires and declares no default
// for: its name and its compiled schema, nil where the pillar's properties do
// not declare it.
type RequiredField struct {
	Name   string
	Schema map[string]any
}

// PillarRequired returns the fields a compiled pillar
// (compiled.properties.<pillar>) requires and declares no default for, in the
// order its required list names them: the values no build can leave out and no
// schema supplies. A pillar declaring no properties, or requiring nothing,
// yields none.
func PillarRequired(compiled Schema, pillar string) []RequiredField {
	top, ok := defaultsMapAt(compiled, "properties")
	if !ok {
		return nil
	}
	pillarNode, ok := defaultsMapAt(top, pillar)
	if !ok {
		return nil
	}
	props, ok := defaultsMapAt(pillarNode, "properties")
	if !ok {
		return nil
	}
	var out []RequiredField
	for _, name := range RequiredNames(pillarNode["required"]) {
		if name == "" {
			continue
		}
		field, _ := props[name].(map[string]any)
		if _, hasDefault := field["default"]; hasDefault {
			continue
		}
		out = append(out, RequiredField{Name: name, Schema: field})
	}
	return out
}

// RequiredNames returns the names a compiled schema's required keyword lists,
// raw, in order, read in either the []string or the []any shape a compiled
// schema carries it in; none where raw is neither.
func RequiredNames(raw any) []string {
	var out []string
	switch list := raw.(type) {
	case []string:
		out = append(out, list...)
	case []any:
		for _, item := range list {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}
