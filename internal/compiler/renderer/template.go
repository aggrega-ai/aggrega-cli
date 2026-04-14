// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"bytes"
	"fmt"
	"text/template"

	sprig "github.com/Masterminds/sprig/v3"
)

// EvalTemplate evaluates a Go template string with Sprig functions and missingkey=zero.
// Returns the rendered output as a string.
func EvalTemplate(name, tmplText string, data interface{}) (string, error) {
	t, err := template.New(name).
		Option("missingkey=zero").
		Funcs(sprig.FuncMap()).
		Parse(tmplText)
	if err != nil {
		return "", fmt.Errorf("template %q parse: %w", name, err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("template %q execute: %w", name, err)
	}
	return buf.String(), nil
}
