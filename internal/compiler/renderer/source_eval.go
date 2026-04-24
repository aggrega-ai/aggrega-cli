// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"strings"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// EvaluateRecipeSource renders Go templates inside a Recipe's source field with
// the same self-contained context as Recipe.Spec.Locals — .values / .infra /
// .provides, no .context.inject / .context.uses. Supports three source shapes:
//
//   - nil: returned as-is (sourceless mode).
//   - string: a single URI or version reference; the whole value is treated as a
//     template.
//   - map[string]interface{}: a structured source (OCI descriptor, etc.); each
//     string-valued field is template-evaluated, non-string fields pass through.
//
// Self-containment rules match EvaluateRecipeLocals: templates may not reference
// .context.inject or .context.uses. Violations surface as a wrapped error.
func EvaluateRecipeSource(
	source interface{},
	nodeID string,
	values types.OrderedMap[interface{}],
	infra types.OrderedMap[interface{}],
	provides types.OrderedMap[string],
) (interface{}, error) {
	if source == nil {
		return nil, nil
	}
	ctx := LocalsContext{
		Context:  ProvidesContextBlock{InstanceID: nodeID},
		Values:   values,
		Infra:    infra,
		Provides: provides,
	}

	switch s := source.(type) {
	case string:
		return renderSourceString(s, ctx, "source")
	case map[string]interface{}:
		out := make(map[string]interface{}, len(s))
		for k, v := range s {
			if str, ok := v.(string); ok {
				rendered, err := renderSourceString(str, ctx, "source."+k)
				if err != nil {
					return nil, err
				}
				out[k] = rendered
			} else {
				out[k] = v
			}
		}
		return out, nil
	default:
		// Unknown shape — pass through untouched (e.g., typed OCI descriptor).
		return source, nil
	}
}

// renderSourceString evaluates a single string field as a template. Empty strings
// and strings without template actions short-circuit (no parsing cost). The
// self-containment check reuses the Recipe-locals forbidden-reference regex.
func renderSourceString(tmplText string, ctx LocalsContext, label string) (string, error) {
	if tmplText == "" {
		return "", nil
	}
	if !strings.Contains(tmplText, "{{") {
		return tmplText, nil
	}
	if err := CheckLocalsSelfContainment(tmplText, label, label); err != nil {
		return "", fmt.Errorf("Recipe source %q: %w", label, err)
	}
	out, err := EvalTemplate(fmt.Sprintf("recipe-source[%s]", label), tmplText, ctx)
	if err != nil {
		return "", fmt.Errorf("Recipe source %q: %w", label, err)
	}
	return strings.TrimSpace(out), nil
}
