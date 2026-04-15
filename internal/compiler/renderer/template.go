// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"text/template"

	sprig "github.com/Masterminds/sprig/v3"
)

// EvalTemplate evaluates a Go template string with Sprig functions and missingkey=zero.
// Data is serialized to JSON and back to map[string]interface{} so that template
// references use lowercase JSON tag names (.context, .values, .infra) matching
// the template-context-v1alpha1.schema.json contract.
func EvalTemplate(name, tmplText string, data interface{}) (string, error) {
	// Convert struct fields to lowercase JSON keys for template consumption
	templateData := toLowercaseMap(data)

	t, err := template.New(name).
		Option("missingkey=zero").
		Funcs(sprig.FuncMap()).
		Parse(tmplText)
	if err != nil {
		return "", fmt.Errorf("template %q parse: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, templateData); err != nil {
		return "", fmt.Errorf("template %q execute: %w", name, err)
	}
	return buf.String(), nil
}

// toLowercaseMap converts a Go struct to map[string]interface{} using JSON
// round-trip so that field names become their lowercase JSON tag values.
func toLowercaseMap(data interface{}) interface{} {
	jsonBytes, err := json.Marshal(data)
	if err != nil {
		return data
	}
	var mapData interface{}
	if err := json.Unmarshal(jsonBytes, &mapData); err != nil {
		return data
	}
	return mapData
}
