// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// EvaluateRecipeSource renders Go templates inside a Recipe's source field with
// the same self-contained context as Recipe.Spec.Locals — .context.instance_id /
// .values / .infra / .locals, no .context.inject / .context.uses and no .provides.
//
// Which parts of a source carry a template is decided by
// mapRecipeSourceTemplates rather than here, because the sensitivity reading asks
// the same question of the same value and the two answers must be one answer.
//
// Self-containment holds by sharing LocalsContext with EvaluateRecipeLocals: the
// context this surface renders against defines no cross-node pillar, so a
// .context.inject or .context.uses reference halts before the template executes.
// Sharing the type AND the scope also means the two surfaces admit and refuse
// exactly the same references — including .provides, which the type does not
// define, and .attachment_name, which the scope refuses because a Recipe's source
// resolves for a node rather than for an attachment.
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
	ctx := LocalsContext{
		Context: ProvidesContextBlock{InstanceID: nodeID},
		Values:  values,
		Infra:   infra,
		Locals:  locals,
	}

	return mapRecipeSourceTemplates(source, func(field, tmplText string) (string, error) {
		return renderSourceString(tmplText, ctx, sourceFieldLabel(field))
	})
}

// mapRecipeSourceTemplates is the ONE place a Recipe source's shape is
// classified: which of its parts carry a template, what each of those parts is
// named, and what the rebuilt value looks like once each has been replaced.
// It walks the source, calls fn for every template-bearing part, and returns the
// source rebuilt from fn's results with every other part carried verbatim.
//
// It exists because two independent readers need that classification and must not
// each carry their own copy of it. The evaluator replaces each template with its
// rendered text; the sensitivity reading replaces nothing and only records which
// parts read a declared-sensitive datum. Two switches over one shape union would
// agree until someone taught the evaluator a third shape, at which point the
// sensitivity reading would silently stop covering it and a computed secret would
// print in the clear. Routing both through this walk makes that divergence
// impossible rather than merely unlikely: a new shape is taught here, once, and
// both readers gain it together.
//
// The shapes, and why each is what it is:
//
//   - nil: a sourceless Recipe. Nothing to walk.
//   - string: a single URI or version reference, evaluated whole. It has no field
//     name, so the field passed to fn is redact.WholeSourceKey — the same
//     spelling the display uses for it, taken from the display's own declaration
//     so the walk and the display cannot disagree about it.
//   - map[string]any: a structured source. Only the string-valued fields are
//     evaluated as templates; every other field travels verbatim, which is why
//     fn is not called for it.
//   - anything else: a shape that reached here already typed. No template is
//     evaluated against it, so it is returned untouched.
//
// Other places in the compiler also switch on this union, and they are
// deliberately NOT folded in here, because they ask a different question of the
// same value: whether a source is declared at all, whether its URL is local, and
// which kind and URI it names at lock time. Only the two readers above ask which
// of its parts carry a template and what each of those parts is called, so only
// those two share this walk. Folding a different question in would produce a
// classifier that answers none of them cleanly.
//
// fn's error is returned unwrapped and stops the walk. Map iteration order is
// Go's own and therefore unspecified; the walk's result is an unordered map, so
// no displayed or rendered byte depends on it. The only order-sensitive outcome
// is WHICH of several failing fields reports first, which is the behaviour this
// walk inherited and deliberately does not change.
func mapRecipeSourceTemplates(source any, fn func(field, tmplText string) (string, error)) (any, error) {
	switch s := source.(type) {
	case nil:
		return nil, nil
	case string:
		return fn(redact.WholeSourceKey, s)
	case map[string]any:
		out := make(map[string]any, len(s))
		for k, v := range s {
			str, ok := v.(string)
			if !ok {
				out[k] = v
				continue
			}
			replaced, err := fn(k, str)
			if err != nil {
				return nil, err
			}
			out[k] = replaced
		}
		return out, nil
	default:
		return source, nil
	}
}

// sourceFieldLabel names one template-bearing part of a source for a diagnostic.
// The unnamed whole-source template is labelled by the block it is; a structured
// field is labelled by its own name beneath it.
func sourceFieldLabel(field string) string {
	if field == redact.WholeSourceKey {
		return "source"
	}
	return "source." + field
}

// renderSourceString evaluates a single string field as a template. Empty strings
// and strings without template actions short-circuit (no parsing cost).
//
// The locals reference check runs against the whole resolved block, so a source
// template naming a local the block does not declare halts rather than rendering
// the key away. There is no forward class on this surface: every local is resolved
// before a source template runs.
//
// The surface shares the base Recipe's locals scope, so it refuses
// .attachment_name for the same reason and by the same declaration: a Recipe's
// source resolves for a node, not for an attachment.
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
	out, err := evalValueTemplate(fmt.Sprintf("recipe-source[%s]", label), tmplText, recipeLocalsScope.with(ctx), "{{", "}}")
	if err != nil {
		return "", cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: label}, fmt.Sprintf("Recipe source %q", label))
	}
	return strings.TrimSpace(out), nil
}
