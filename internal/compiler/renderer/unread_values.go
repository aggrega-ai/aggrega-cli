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

// unreadValueWarnings reports the authored keys reaching this node that no
// template the Recipe declares ever references — a knob an author added and
// nothing consumes, which is a smell rather than a fault, so each one is a
// warning and none of them halts the build.
//
// Three groups of authored keys are read: the node's own values, the node's own
// infra, and the per-connection values carried on each of the node's outgoing
// uses edges, which a template reaches at .context.uses.<alias>.values. The
// per-edge group is consumer-scoped for free, because the uses map holds only
// the edges this node is the source of — so an unread per-edge value is always
// attributed to the node that authored it and never to the edge's target.
//
// The references are collected from EVERY authored template the Recipe declares,
// not from the values mapping alone — see recipeReadReferences for the set and
// for why a partial set would make this reading assert something false. A key
// consumed only by a source coordinate, a local, a provides entry, an assertion,
// the workload body, an artifacts entry or a pipeline step is read, and this
// reading says so.
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
//   - A Recipe with no inline valuesMapping is not read. That bound predates the
//     wider reference set and is kept deliberately: it is the one authoring shape
//     whose keys have never been compared, so turning the reading on for it would
//     change which Recipes are examined rather than which templates are consulted,
//     and the two are separate decisions.
//   - A surface whose content the Recipe imports from a file or a library is not
//     in hand here, so its references cannot be collected and no claim is made
//     about the node's keys.
//   - A template that fails to parse yields no reference set. Reporting every key
//     as unread on a parse failure would bury the real diagnostic the evaluation
//     raises for the same template, so nothing is reported instead.
//   - The per-edge group is reported only where a reference names the literal
//     path .context.uses.<alias>.values.<key>. A template that reaches the edge
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
	refs, readable := recipeReadReferences(recipe)
	if !readable {
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
				// The sentence states the fact and then the two things its reader
				// can do about it, and it names no field of the Recipe. Whoever
				// authored the key is the developer or the operator, not the
				// Recipe's author: naming the block the reference was looked for in
				// would hand them a coordinate in a document they do not edit, and a
				// reader who has to go and find that document to learn what the
				// advisory means has been told less than nothing.
				Message: fmt.Sprintf("%s key %q is read by no template in recipe %q — correct the spelling, or remove the key",
					g.root, k, recipe.Metadata.Name),
			})
		}
	}
	return warnings
}

// recipeReadReferences collects the references every authored template on this
// Recipe makes, as the one corpus the unread-key reading compares a node's
// authored keys against. readable is false where that corpus cannot be assembled
// — some surface's content is not in hand, or one of them does not parse — and a
// caller with no corpus makes no claim.
//
// # Why the set is every template rather than the values mapping
//
// The reading asserts that a key is read by NOTHING. That is a claim over the
// whole Recipe, so a corpus narrower than the whole Recipe makes it false rather
// than approximate: a key a source coordinate or a provides entry consumes is
// reported unread by the same run that obeys it. Every block below is evaluated
// as a Go template against a context carrying the node's own .values and .infra,
// which is exactly the property that puts it in the corpus:
//
//   - the values mapping, the workload body, each artifacts entry and each
//     pipeline step's input and gate, taken from [RecipeTemplateFields] rather
//     than re-enumerated — that function is the one home of which fields those
//     are, and a second list here would be free to drift from it;
//   - the source block, walked by mapRecipeSourceTemplates, which is the one home
//     of which parts of a source carry a template. It is asked rather than
//     answered here for the same reason, and it covers a scalar source whole as
//     well as every string-valued field of the structured form;
//   - each locals entry, each provides entry and each assertion, which the
//     enumeration above deliberately leaves out because each has its own rules
//     elsewhere. Those rules govern what a template may REFERENCE; this reading
//     asks what it DOES reference, so the exclusion does not carry over.
//
// # The direction a doubtful case is resolved in
//
// Every bound below silences the reading rather than widening it, which is the
// fail-safe direction here: an unreported key costs a reader one advisory they
// can still find by reading the Recipe, where a wrongly reported one tells them
// their working configuration was ignored.
//
// An imported workload or artifacts entry carries no text at this point, so its
// references cannot be collected and no claim is made — the same bound an
// imported values mapping already carries. An artifacts body is parsed with the
// default delimiter pair, which is not the pair every provider declares for that
// field: read with the wrong pair such a body yields references its author never
// made, or does not parse at all, and both outcomes only ever silence a warning.
//
// The collected order follows the enumerations except within a structured
// source, whose walk is over a Go map. Nothing depends on it: the result is
// consulted as a membership set and never emitted.
func recipeReadReferences(recipe *types.Recipe) ([]string, bool) {
	if recipe.Spec.Workload.Import != "" {
		return nil, false
	}
	for _, entry := range recipe.Spec.Artifacts.All() {
		if entry.Import != "" {
			return nil, false
		}
	}

	var refs []string
	collect := func(text string) bool {
		if text == "" {
			return true
		}
		found, err := gotmpl.CollectTemplateFieldAccesses(text)
		if err != nil {
			return false
		}
		refs = append(refs, found...)
		return true
	}

	for _, field := range RecipeTemplateFields(recipe) {
		if !collect(field.Text) {
			return nil, false
		}
	}
	for _, block := range []types.OrderedMap[string]{
		recipe.Spec.Locals, recipe.Spec.Provides, recipe.Spec.Assertions,
	} {
		for _, text := range block.All() {
			if !collect(text) {
				return nil, false
			}
		}
	}

	sourceReadable := true
	_, _ = mapRecipeSourceTemplates(recipe.Spec.Source, func(_, tmplText string) (string, error) {
		if !collect(tmplText) {
			sourceReadable = false
		}
		return tmplText, nil
	})
	if !sourceReadable {
		return nil, false
	}
	return refs, true
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
