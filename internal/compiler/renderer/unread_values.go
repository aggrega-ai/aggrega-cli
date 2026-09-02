// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"strings"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// unreadValueWarnings reports the authored keys reaching this node that the
// Recipe's valuesMapping never references — a knob an author added and nothing
// consumes, which is a smell rather than a fault, so each one is a warning and
// none of them halts the build.
//
// Three groups of authored keys are read: the node's own values, the node's own
// infra, and the per-connection values carried on each of the node's outgoing
// uses edges, which a template reaches at .context.uses.<alias>.values. The
// per-edge group is consumer-scoped for free, because the uses map holds only
// the edges this node is the source of — so an unread per-edge value is always
// attributed to the node that authored it and never to the edge's target.
//
// Only value INSTANCES are compared. A socket's valueSchema is a contract rather
// than an authored instance and is not among the inputs here, so it can never be
// reported; what a socket's declared default produces is an instance like any
// other, and is read or unread on its own merits.
//
// A static walk cannot tell a deliberate don't-care from a mistake, which is why
// the reading only ever advises. The bounds that follow from what can be read at
// all:
//
//   - Only an inline valuesMapping is read. A mapping the Recipe imports from a
//     file or a library is not in hand here, so its references cannot be
//     collected and no claim is made about the node's keys.
//   - A mapping that fails to parse yields no reference set. Reporting every key
//     as unread on a parse failure would bury the real diagnostic the evaluation
//     raises for the same template, so nothing is reported instead.
//   - The per-edge group is reported only where a reference names the literal
//     path .context.uses.<alias>.values.<key>. A mapping that reaches the edge
//     through an enclosing expression — iterating the uses map, indexing one edge
//     out of it, or rebinding it — names only the enclosing path, and every key
//     beneath it then counts as read. That enclosing form is the only one open to
//     a mapping whose socket is optional, because the literal path evaluates
//     against an absent edge when the socket is unwired and every guard that
//     would prevent that is itself an enclosing read. So the per-connection
//     values carried on an optional socket lie outside what this reading reports.
//
// Warnings come back in a fixed order — the node's values, then its infra, then
// each uses alias in the order the edges were emitted, each group in the order
// the census returns — so the same input produces the same advisory stream on
// every run.
//
// Each advisory is located at the node and names the unread key as the element
// it concerns, so a reader that renders no text can act on the finding without
// recovering the key from a sentence. There is no class code for this reading,
// so none is carried.
func unreadValueWarnings(
	nodeID string,
	recipe *types.Recipe,
	values, infra types.OrderedMap[any],
	uses types.OrderedMap[types.EnrichedEdge],
) []Diagnostic {
	if recipe == nil || recipe.Spec.ValuesMapping.Inline == "" {
		return nil
	}
	refs, err := gotmpl.CollectTemplateFieldAccesses(recipe.Spec.ValuesMapping.Inline)
	if err != nil {
		return nil
	}
	// A slice rather than a map: the groups are reported in this order on every
	// run, which a map's iteration could not promise. The uses aliases extend it
	// in edge-emission order, which the uses map preserves.
	groups := []struct {
		root string
		data types.OrderedMap[any]
	}{
		{".values", values},
		{".infra", infra},
	}
	for _, alias := range uses.Keys() {
		edge, ok := uses.Get(alias)
		if !ok {
			continue
		}
		groups = append(groups, struct {
			root string
			data types.OrderedMap[any]
		}{".context.uses." + alias + ".values", edge.Values})
	}
	var warnings []Diagnostic
	for _, g := range groups {
		if enclosingPathRead(refs, g.root) {
			continue
		}
		for _, k := range gotmpl.UnreadKeys(refs, g.root, g.data) {
			warnings = append(warnings, Diagnostic{
				Severity:  abi.SeverityWarn,
				SourceRef: k,
				Location:  &DiagnosticLocation{URI: nodeID},
				Message: fmt.Sprintf("unused %s key %q (not referenced by valuesMapping of recipe %q)",
					g.root, k, recipe.Metadata.Name),
			})
		}
	}
	return warnings
}

// enclosingPathRead reports whether any reference names a path that ENCLOSES
// root, rather than a key beneath it.
//
// A template that iterates the whole uses map, or reaches one edge through
// index, reads the per-edge keys through a value the template itself bound, and
// the census then records only the enclosing path the expression started from —
// .context.uses, or .context.uses.<alias>. Reading that as "everything beneath
// it was read" is the same allowance the key census already makes for a
// reference naming a group's own path, one level out; without it every
// per-connection value read that way would be reported unread.
//
// It is deliberately kept here, beside the group list, rather than folded into
// the key census: only a nested path has an enclosing path to name, and the
// node's own values and infra are top-level, so the check is inert for them and
// the census stays a plain question about keys.
func enclosingPathRead(references []string, root string) bool {
	for _, ref := range references {
		if ref != "" && strings.HasPrefix(root, ref+".") {
			return true
		}
	}
	return false
}
