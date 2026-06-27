// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"bytes"

	"go.yaml.in/yaml/v3"
)

// ClampYAMLIndent bounds a YAML indent width to the supported [2,9] range,
// falling back to 2 (the default) for the zero value or out-of-range input.
// It is the single source of the indent-default policy: every marshal site —
// whether it routes through MarshalYAMLIndent or drives its own streaming
// encoder — clamps through here so the configured width is applied uniformly.
func ClampYAMLIndent(indent int) int {
	if indent < 2 || indent > 9 {
		return 2
	}
	return indent
}

// MarshalYAMLIndent is the single YAML marshal entry point for rendered
// artifacts. It encodes v at the given indentation width so every renderer
// emits uniform, configurable indentation. The width is clamped to [2,9]
// (default 2). yaml.v3 sorts map keys and invokes MarshalYAML recursively, so
// byte-identical input produces byte-identical output across compilations.
func MarshalYAMLIndent(v any, indent int) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(ClampYAMLIndent(indent))
	if err := enc.Encode(v); err != nil {
		_ = enc.Close()
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
