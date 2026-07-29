// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// derivedSensitiveKeys reads which of a Recipe's template outputs were computed
// from a declared-sensitive datum, so a display can withhold them.
//
// It exists because withholding by KEY is not enough. A declared-sensitive leaf
// is identifiable wherever the leaf itself appears, but a template that
// interpolates it produces a new string under a key nobody declared — a local
// whose text reads a withheld password prints that password in the clear on every
// surface that shows the resolved block. What can be read to recover the link is
// the reference the author wrote, and nothing else: the resolved value is
// arbitrary text the compiler must not inspect, and a key's name says nothing
// about what it holds.
//
// The analysis is entirely STATIC — authored template text plus the declared-leaf
// path sets. It evaluates nothing, needs no resolved value, and has no ordering
// relationship to the evaluation, which is what lets it run wherever the
// declarations and the Recipe are both in scope.
//
// A reference withholds its output when, after its pillar prefix is stripped, the
// referenced path is EQUAL to, an ANCESTOR of, or a DESCENDANT of a declared
// path:
//
//   - equal is the direct read.
//   - ancestor covers every read that reaches a whole object and therefore
//     reaches each withheld leaf under it: `range .values.creds` references
//     .values.creds, and `index .values $k` references .values. This is what
//     makes a dynamic key and a rebound dot conservative rather than invisible.
//   - descendant covers a declaration on an intermediate object, which already
//     withholds its whole subtree where the pillar is displayed, so a read of one
//     leaf beneath it must withhold too.
//
// Withholding is TRANSITIVE across locals. Entries resolve in author order and
// each may read an earlier sibling, so a local reading an already-withheld local
// is withheld itself; the walk follows the same author order and carries the set
// forward, which is why one pass suffices.
//
// Where a reference cannot be resolved statically the output is withheld. That is
// a deliberate choice of direction, not an accident of implementation:
// over-withholding costs a reader one value they must find another way, while
// under-withholding prints a secret. A template whose text does not parse is
// treated the same way, for the same reason.
//
// declaredValues and declaredInfra are the host Component's declared leaf paths.
// With neither declaring anything, nothing can have been computed from a
// declaration and the zero value is returned.
func derivedSensitiveKeys(recipe *types.Recipe, declaredValues, declaredInfra map[string]bool) redact.Derived {
	if recipe == nil || (len(declaredValues) == 0 && len(declaredInfra) == 0) {
		return redact.Derived{}
	}

	// The reading consults exactly the three pillars a locals-or-source template
	// can read a DECLARED leaf through. The cross-node and delivery-key sets are
	// left empty here on purpose: identifying a computed value from those would
	// take the same reading with a wider set, and widening it is a separate
	// decision about a separate display rather than a side effect of this one.
	// withheld is carried by reference, so each entry the loop marks is visible to
	// the entries after it — which is what makes withholding transitive in one
	// pass.
	withheld := map[string]bool{}
	readable := redact.Keys{Values: declaredValues, Infra: declaredInfra, Locals: withheld}

	for key, tmplText := range recipe.Spec.Locals.All() {
		if templateReadsWithheld(tmplText, readable) {
			withheld[key] = true
		}
	}

	// Which parts of the source carry a template is asked of the one walk that
	// classifies a source's shape — the same walk the evaluation goes through — so
	// a shape the evaluator learns to evaluate is a shape this reading covers
	// without a second edit. Nothing is replaced here: the walk's rebuilt value is
	// discarded and each template's text is returned unchanged, which is what makes
	// this a read of the same classification rather than a second evaluation.
	source := map[string]bool{}
	_, _ = mapRecipeSourceTemplates(recipe.Spec.Source, func(field, tmplText string) (string, error) {
		if templateReadsWithheld(tmplText, readable) {
			source[field] = true
		}
		return tmplText, nil
	})

	derived := redact.Derived{}
	if len(withheld) > 0 {
		derived.Locals = withheld
	}
	if len(source) > 0 {
		derived.Source = source
	}
	return derived
}

// templateReadsWithheld reports whether one template's references reach anything
// the given set withholds. The reference set comes from the over-collecting census
// walk, which is the direction this question needs: a read hidden inside a rebound
// range or with body, or inside a chained defensive wrapper, has still happened,
// and a walk that attributed only root-anchored reads would answer "not withheld"
// for exactly the shapes that most need the conservative answer.
//
// A set that withholds nothing readable short-circuits before the parse, which is
// what lets a capture mode that empties the set turn this reading off without a
// second switch: nothing is withheld, so nothing can have been read from it. A
// template with no actions likewise reads nothing. A template that fails to parse
// yields no reference set, and the unresolvable case is withheld.
func templateReadsWithheld(tmplText string, keys redact.Keys) bool {
	if !keys.AnyWithheld() || tmplText == "" || !strings.Contains(tmplText, "{{") {
		return false
	}
	refs, err := CollectTemplateFieldAccesses(tmplText)
	if err != nil {
		return true
	}
	for _, ref := range refs {
		if referenceReachesWithheld(ref, keys) {
			return true
		}
	}
	return false
}

// referenceReachesWithheld decides one reference against the withheld set. The
// reference is the census spelling — a leading "." then the pillar root then the
// remaining segments.
//
// Every root the set can name is covered, and that completeness is the point: a
// reading that covered a subset would answer "not withheld" for a template that
// reads a withheld datum through one of the uncovered roots, which is the
// under-withholding direction this walk refuses. A root the set cannot name — the
// node's identity, its labels, its metadata — carries nothing any declaration
// governs and reports false.
func referenceReachesWithheld(ref string, keys redact.Keys) bool {
	root, rest := splitPillarReference(ref)
	switch root {
	case "values":
		return pathReachesWithheld(rest, keys.Values)
	case "infra":
		return pathReachesWithheld(rest, keys.Infra)
	case localsContextRoot:
		if rest == "" {
			// A read of the whole pillar reaches every entry in it.
			return len(keys.Locals) > 0
		}
		return keys.Locals[firstPathSegment(rest)]
	case "source":
		if rest == "" {
			// A source declared as a single string is withheld under the
			// whole-source key, so a bare read reaches it as well as reaching
			// every field of a structured one.
			return len(keys.Source) > 0
		}
		return keys.Source[firstPathSegment(rest)]
	case "context":
		return contextReferenceReachesWithheld(rest, keys)
	}
	return false
}

// contextReferenceReachesWithheld decides a reference below the context root,
// where the two withheld surfaces are the delivery pipe and the uses edges.
//
// Both are two levels deep — a mechanism then a delivery key, an alias then a
// provides key — so a read that stops short of the leaf reaches every leaf beneath
// it and withholds on that basis. That is the same ancestor rule the pillar paths
// use, and it is what makes a dynamic lookup such as `index .context.inject.env $k`
// conservative rather than invisible.
func contextReferenceReachesWithheld(rest string, keys redact.Keys) bool {
	surface, below := splitPillarReference("." + rest)
	switch surface {
	case "inject":
		if below == "" {
			return keys.AnyDelivery()
		}
		mechanism, key := splitPillarReference("." + below)
		if key == "" {
			return keys.AnyDeliveryIn(mechanism)
		}
		return keys.Delivery(mechanism, firstPathSegment(key))
	case "uses":
		if below == "" {
			return anyMarked(keys.UsesProvides)
		}
		alias, edgeField := splitPillarReference("." + below)
		marked := keys.UsesProvides[alias]
		if len(marked) == 0 {
			return false
		}
		if edgeField == "" {
			// A read of the whole edge reaches its provides bag.
			return true
		}
		field, provideKey := splitPillarReference("." + edgeField)
		if field != "provides" {
			// Every other field on the edge — its target, socket, labels, and the
			// per-connection values and infra authored on the edge itself — is not
			// what the target's provides declaration governs.
			return false
		}
		if provideKey == "" {
			return true
		}
		return marked[firstPathSegment(provideKey)]
	}
	return false
}

func anyMarked(sets map[string]map[string]bool) bool {
	for _, set := range sets {
		if len(set) > 0 {
			return true
		}
	}
	return false
}

// workloadReadsWithheld reports whether a Recipe's workload template reads
// anything the node's set names — the question that decides whether a display
// shows the emitted workload document or a re-render of it with that data
// substituted.
//
// A template reading nothing named produces a document carrying nothing the set
// governs, so the emitted document is shown as it is and no second evaluation
// happens. That is what keeps the axis free for a node that declares nothing.
//
// The reading is the same static one the locals and source withholding uses, over
// the same reference census — one reading, one rule, a wider set. The set is wider
// because a workload template reads surfaces a locals template's withholding never
// had to cover: the delivery pipe, and a consumed node's published provides.
//
// It is deliberately a question about the TEMPLATE and not about the rendered
// document. Asking the document would mean searching its content for a value,
// which is the meaning-inference the compiler must not do.
func workloadReadsWithheld(tmplText string, keys redact.Keys) bool {
	return templateReadsWithheld(tmplText, keys)
}

// usesWithheld reads, per uses edge, what the edge TARGET's own Component
// declares sensitive among the values that edge carries into the consuming node's
// display.
//
// It is a lookup, not a derivation: declaredProvides is the same memoised
// authored-declaration reader the node's own declarations come from, asked about a
// different Component type. The target's type is read from the edge, which carries
// it — the enriched edge a consumer's display shows names the target Component.
//
// ONE walk produces both halves, because both read the same declaration set over
// the same edges. The published half is keyed by ALIAS, so two edges to two
// different targets resolve independently even when they publish the same key name
// — which is what keeps the withholding attributable to a declaration instead of to
// a spelling. The delivered half is keyed by MECHANISM and delivery key, because
// the delivery pipe is one flat merged map per mechanism and carries no alias; the
// edge's authored `inject` mapping is read exactly as the delivery pass reads it,
// the mapping's value verbatim, so the two cannot disagree about which declaration
// a delivery key resolves against.
//
// An edge whose target declares nothing contributes to neither half, so a node
// consuming only undeclared targets produces two nil maps and every applier is a
// no-op. An edge whose provides come from a boundary-scope FeatureRecipe rather
// than from the target Component's own Recipe resolves against no Component
// declaration and is likewise not covered.
func usesWithheld(
	uses types.OrderedMap[types.EnrichedEdge],
	declaredProvides func(componentType string) map[string]bool,
) redact.CrossNode {
	var out redact.CrossNode
	if uses.Len() == 0 || declaredProvides == nil {
		return out
	}
	for alias, edge := range uses.All() {
		declared := declaredProvides(edge.Component)
		if len(declared) == 0 {
			continue
		}
		if out.Provides == nil {
			out.Provides = map[string]map[string]bool{}
		}
		out.Provides[alias] = declared

		for mechanism, deliveries := range edge.Inject.All() {
			for deliveryKey, providesKey := range deliveries.All() {
				if !declared[providesKey] {
					continue
				}
				if out.Inject == nil {
					out.Inject = redact.DeliveryKeys{}
				}
				if out.Inject[mechanism] == nil {
					out.Inject[mechanism] = map[string]bool{}
				}
				out.Inject[mechanism][deliveryKey] = true
			}
		}
	}
	return out
}

// injectMappingWithheld reads which of the delivery keys a FeatureRecipe's own
// injectMapping put on a node's pipe carry material the attachment's Feature
// declares sensitive.
//
// It exists because a `featuresSchema` slot may declare no delivery of its own,
// in which case the FeatureRecipe controls delivery: the attachment carries no
// `inject` mapping, so the reading that walks that mapping has nothing to walk and
// the delivered key is never marked. The material still reaches the node's display
// through the pipe. This is the reading that covers that route.
//
// The DELIVERY KEY IS ITSELF A TEMPLATE, and that constraint shapes the whole
// derivation. A mapping such as
//
//	env:
//	  {{ .values.key }}: "{{ .values.origin }}"
//
// names neither its delivery key nor its value until it has been evaluated, so
// nothing can be attributed per authored key before the evaluation. Attribution
// therefore runs over the RENDERED overlay's keys, and the rule is per attachment
// rather than per key: when the mapping's authored text references ANY leaf the
// attachment's Feature declares sensitive, every delivery key that attachment's
// overlay produced is withheld.
//
// That OVER-collects by design, and the cost is worth naming rather than leaving
// implicit: a mapping that delivers a public locator alongside the credential puts
// a placeholder over the locator too. It is the fail-safe direction — a reader
// loses one value they can find another way, where the other direction prints a
// secret — and the alternative that would attribute per key is in the decision
// record with the reason it was refused.
//
// The reading is STATIC: the authored mapping text plus the Feature's declared
// leaf paths. It evaluates nothing and inspects no value, and it goes through the
// same reference reading every other withholding question uses, over the
// over-collecting reference census — the direction a must-catch-everything
// question needs, because a read hidden inside a rebound range or with body has
// still happened.
//
// The pillars it compares against are the ATTACHMENT's, not the host node's: an
// injectMapping is evaluated against a context whose values and infra are the
// attachment's own, so the declarations that govern what it can read are the
// Feature's. declared answers that per attachment instance name; an attachment it
// cannot answer for contributes nothing, which is the same answer a Feature
// declaring nothing gives.
func injectMappingWithheld(
	delivered []InjectMappingDelivery,
	declared func(instanceName string) resolver.SensitiveLeaves,
) redact.DeliveryKeys {
	if len(delivered) == 0 || declared == nil {
		return nil
	}
	var out redact.DeliveryKeys
	for _, entry := range delivered {
		if len(entry.Keys) == 0 {
			continue
		}
		leaves := declared(entry.InstanceName)
		if len(leaves.Values) == 0 && len(leaves.Infra) == 0 {
			continue
		}
		if !templateReadsWithheld(entry.Mapping, redact.Keys{Values: leaves.Values, Infra: leaves.Infra}) {
			continue
		}
		for mechanism, keys := range entry.Keys {
			for key := range keys {
				if out == nil {
					out = redact.DeliveryKeys{}
				}
				if out[mechanism] == nil {
					out[mechanism] = map[string]bool{}
				}
				out[mechanism][key] = true
			}
		}
	}
	return out
}

// splitPillarReference splits a census reference into its pillar root and the
// path below that root. rest is empty for a whole-pillar reference.
func splitPillarReference(ref string) (root, rest string) {
	trimmed := strings.TrimPrefix(ref, ".")
	if i := strings.Index(trimmed, "."); i >= 0 {
		return trimmed[:i], trimmed[i+1:]
	}
	return trimmed, ""
}

func firstPathSegment(path string) string {
	if i := strings.Index(path, "."); i >= 0 {
		return path[:i]
	}
	return path
}

// pathReachesWithheld reports whether one referenced path within a pillar is
// equal to, an ancestor of, or a descendant of any declared path in that pillar.
//
// The declared set is a set and is walked to a boolean, so the walk's order
// cannot reach any displayed byte.
func pathReachesWithheld(path string, declared map[string]bool) bool {
	if len(declared) == 0 {
		return false
	}
	if path == "" {
		// A read of the whole pillar reaches every declared leaf under it.
		return true
	}
	for d := range declared {
		if d == path ||
			strings.HasPrefix(d, path+".") ||
			strings.HasPrefix(path, d+".") {
			return true
		}
	}
	return false
}
