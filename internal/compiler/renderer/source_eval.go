// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// EvaluateRecipeSource renders Go templates inside a Recipe's source field with
// the same self-contained context as Recipe.Spec.Locals — .context.instance_id /
// .values / .infra / .locals, no .context.inject / .context.uses and no .provides.
// Supports three source shapes:
//
//   - nil: returned as-is (sourceless mode).
//   - string: a single URI or version reference; the whole value is treated as a
//     template.
//   - map[string]any: a structured source (OCI descriptor, etc.); each
//     string-valued field is template-evaluated, non-string fields pass through.
//
// Self-containment holds by sharing LocalsContext with EvaluateRecipeLocals: the
// context this surface renders against defines no cross-node pillar, so a
// .context.inject or .context.uses reference halts before the template executes.
// Sharing the type also means the two surfaces admit and refuse exactly the same
// references — including .provides, which neither admits.
//
// locals is the node's RESOLVED locals. A source template is evaluated after the
// whole block has resolved, so it reads every local rather than a prefix; a
// reference to a key the block does not declare halts with
// ErrLocalsUnknownReference instead of resolving to an empty string, which is the
// same refusal a locals entry gets for the same mistake. Passing the resolved map
// rather than resolving it here keeps one evaluation behind every reader of a
// node's locals.
func EvaluateRecipeSource(
	source any,
	nodeID string,
	values types.OrderedMap[any],
	infra types.OrderedMap[any],
	locals types.OrderedMap[string],
) (any, error) {
	if source == nil {
		return nil, nil
	}
	ctx := LocalsContext{
		Context: ProvidesContextBlock{InstanceID: nodeID},
		Values:  values,
		Infra:   infra,
		Locals:  locals,
	}

	switch s := source.(type) {
	case string:
		return renderSourceString(s, ctx, "source")
	case map[string]any:
		out := make(map[string]any, len(s))
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
// and strings without template actions short-circuit (no parsing cost).
//
// The locals reference check runs against the whole resolved block, so a source
// template naming a local the block does not declare halts rather than rendering
// the key away. There is no forward class on this surface: every local is resolved
// before a source template runs.
func renderSourceString(tmplText string, ctx LocalsContext, label string) (string, error) {
	if tmplText == "" {
		return "", nil
	}
	if !strings.Contains(tmplText, "{{") {
		return tmplText, nil
	}
	site := localsRefSite{Where: fmt.Sprintf("Recipe source %q", label), File: label}
	if err := checkLocalsReferenceOrder(tmplText, site, ctx.Locals, ctx.Locals.Keys()); err != nil {
		return "", err
	}
	out, err := EvalTemplate(fmt.Sprintf("recipe-source[%s]", label), tmplText, ctx)
	if err != nil {
		return "", cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: label}, fmt.Sprintf("Recipe source %q", label))
	}
	return strings.TrimSpace(out), nil
}
