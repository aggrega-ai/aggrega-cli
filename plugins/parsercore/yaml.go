// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package parsercore

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
	"go.yaml.in/yaml/v3"
)

// ParseYAML reads YAML source into a canonical JSON tree. A YAML mapping becomes
// a JSON object, a sequence a JSON array, and a scalar its JSON scalar; the
// re-encoded tree carries object keys in sorted order. Mapping keys are coerced
// to strings so a YAML mapping keyed by a non-string scalar still encodes as a
// JSON object. Malformed YAML yields an error diagnostic and a null tree rather
// than a crash.
func ParseYAML(src []byte) abi.ParseOutput {
	var value any
	if err := yaml.Unmarshal(src, &value); err != nil {
		return errorOutput("yaml parse: " + err.Error())
	}
	return treeOutput(normalizeYAML(value))
}

// normalizeYAML rewrites a decoded YAML value into a JSON-encodable shape:
// every mapping becomes a map[string]any with string-coerced keys, and sequences
// recurse element-wise. A YAML decode into any already yields map[string]any for
// mappings, but coercing defensively keeps a non-string mapping key (an integer
// or boolean key) from failing the JSON encode.
func normalizeYAML(value any) any {
	switch v := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, elem := range v {
			out[key] = normalizeYAML(elem)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(v))
		for key, elem := range v {
			out[fmt.Sprint(key)] = normalizeYAML(elem)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, elem := range v {
			out[i] = normalizeYAML(elem)
		}
		return out
	default:
		return v
	}
}
