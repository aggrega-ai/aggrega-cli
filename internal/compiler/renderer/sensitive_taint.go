// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
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

	withheld := derivedLocals(recipe.Spec.Locals, declaredValues, declaredInfra)
	readable := redact.Keys{Values: declaredValues, Infra: declaredInfra, Locals: withheld}

	// Which parts of the source carry a template is asked of the one walk that
	// classifies a source's shape — the same walk the evaluation goes through — so
	// a shape the evaluator learns to evaluate is a shape this reading covers
	// without a second edit. Nothing is replaced: it is a read of the same
	// classification rather than a second evaluation, and it is the reading a
	// refusal raised while resolving the source asks too.
	source := sourcePartsReadingWithheld(recipe.Spec.Source, readable)

	derived := redact.Derived{}
	if len(withheld) > 0 {
		derived.Locals = withheld
	}
	if len(source) > 0 {
		derived.Source = source
	}
	return derived
}

// derivedLocals reads which entries of one locals block were computed from a
// declared-sensitive datum: an entry whose template reads a declared values or
// infra leaf, or reads an earlier entry already withheld. It is the locals half of
// derivedSensitiveKeys, and it is shared because a FeatureRecipe's own block is
// read the same way against the attachment's declarations.
//
// The reading consults exactly the three pillars a locals template can read a
// DECLARED leaf through.
//
// A block reading nothing declared, or no declaration at all, returns an empty
// map rather than nil, so a caller can add to it.
func derivedLocals(locals types.OrderedMap[string], declaredValues, declaredInfra map[string]bool) map[string]bool {
	if len(declaredValues) == 0 && len(declaredInfra) == 0 {
		return map[string]bool{}
	}
	return localsReading(locals, redact.Keys{Values: declaredValues, Infra: declaredInfra})
}

// localsReading reads which entries of one locals block read a datum readable
// names, or read an earlier entry so marked. readable's own locals are not
// consulted: the block's entries are the locals this reading decides. The marked
// set is carried by reference in the set the templates are read against, so each
// entry the loop marks is visible to the entries after it — which is what makes
// withholding transitive in one pass, in author order.
func localsReading(locals types.OrderedMap[string], readable redact.Keys) map[string]bool {
	withheld := map[string]bool{}
	readable.Locals = withheld
	readable.Lists.Locals = nil
	for key, tmplText := range locals.All() {
		if templateReadsWithheld(tmplText, readable) {
			withheld[key] = true
		}
	}
	return withheld
}

// templateReadsWithheld reports whether one template's references reach anything
// the given set withholds. The reference set comes from the over-collecting census
// walk, which is the direction this question needs: a read hidden inside a rebound
// range or with body, or inside a chained defensive wrapper, has still happened,
// and a walk that attributed only root-anchored reads would answer "not withheld"
// for exactly the shapes that most need the conservative answer. A template
// handing on the whole of its data — `toJson .`, a bare `$`, the dot passed to a
// template it invokes by name — reads every datum in it, and the census reports
// that too.
//
// Whether a reference reaches a withheld datum is answered by the set itself,
// in the coordinates a template reads its data by — the same coordinates the
// substitution of a template's data writes at — so this reading and that
// substitution cannot disagree about a root.
//
// A set that withholds nothing readable short-circuits before the parse, which is
// what lets a capture mode that empties the set turn this reading off without a
// second switch: nothing is withheld, so nothing can have been read from it. A
// template with no actions likewise reads nothing. A template that fails to parse
// yields no reference set, and the unresolvable case is withheld.
func templateReadsWithheld(tmplText string, keys redact.Keys) bool {
	return templateReadsWithheldDelims(tmplText, "{{", "}}", keys)
}

// templateReadsWithheldDelims is templateReadsWithheld for a template evaluated
// with its own delimiter pair — an artifact entry, which a provider may have the
// host evaluate with a pair its own target syntax does not collide with. The
// reading is the same one; only the actions it finds depend on the pair, so a
// template is read with the pair it is evaluated with, never the default.
func templateReadsWithheldDelims(tmplText, leftDelim, rightDelim string, keys redact.Keys) bool {
	// An empty delimiter is text/template's own default, as the evaluation reads it.
	if leftDelim == "" {
		leftDelim = "{{"
	}
	if rightDelim == "" {
		rightDelim = "}}"
	}
	if !keys.AnyWithheld() || tmplText == "" || !strings.Contains(tmplText, leftDelim) {
		return false
	}
	root, err := gotmpl.ParseTree(tmplText, leftDelim, rightDelim)
	if err != nil {
		return true
	}
	return keys.ReachedBy(gotmpl.TreeDataReads(root))
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
//
// The same walk reads the third thing an edge displays: the attachments written on
// the connection, under the slot each fills. Each is governed by its own Feature,
// so declaredAttachment is asked about the attachment's Feature type — the reader
// every attachment's declarations come from, which is what makes a connection's
// credential withheld exactly where the same Feature attached to the node itself
// would be. Nothing is read out of the slot: a slot declaring itself sensitive
// marks the attachment whole, which no display reads as a statement about any one
// of its leaves. A nil reader reads nothing, and an attachment whose Feature
// declares nothing contributes nothing.
func usesWithheld(
	uses types.OrderedMap[types.EnrichedEdge],
	declaredProvides func(componentType string) map[string]bool,
	declaredAttachment func(featureType string) resolver.SensitiveLeaves,
) redact.CrossNode {
	var out redact.CrossNode
	if uses.Len() == 0 {
		return out
	}
	for alias, edge := range uses.All() {
		if declaredAttachment != nil {
			for slot, attachment := range edge.Features.All() {
				leaves := declaredAttachment(attachment.Type)
				if !leaves.Any() {
					continue
				}
				if out.Attachments == nil {
					out.Attachments = map[string]map[string]redact.AttachmentKeys{}
				}
				if out.Attachments[alias] == nil {
					out.Attachments[alias] = map[string]redact.AttachmentKeys{}
				}
				out.Attachments[alias][slot] = redact.AttachmentKeys{
					Values:   leaves.Values,
					Infra:    leaves.Infra,
					Provides: leaves.Provides,
				}
			}
		}
		if declaredProvides == nil {
			continue
		}
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

// connectionsWithheld reads, per connection a node draws, the leaves of the
// connection's own values and infra the graph's edge marks — the configuration
// the per-edge contract the node's Component declares on the socket covers.
//
// It is a reading of the graph, not of any declaration: the marking was computed
// once, when the graph was built, from the contract the connection was validated
// against, and the graph's own withholding reads the same marking. So a template's
// view of a connection withholds exactly the leaves the graph withholds of it, and
// no surface comes to a different answer by reading the contract a second time.
//
// It is keyed by the alias each connection is displayed under, the key the
// render context's uses map is built with, and an edge marking nothing is left
// out, so a node whose connections carry nothing declared yields nil.
func connectionsWithheld(nodeID string, graph *types.IRGraph) map[string]redact.ConnectionKeys {
	if graph == nil {
		return nil
	}
	var out map[string]redact.ConnectionKeys
	for _, edge := range graph.Spec.Edges {
		if edge.Source != nodeID {
			continue
		}
		marked := redact.ConnectionOf(edge)
		if !marked.Any() {
			continue
		}
		if out == nil {
			out = map[string]redact.ConnectionKeys{}
		}
		out[connectionAlias(edge)] = marked
	}
	return out
}

// selfInjectWithheld reads which of the delivery keys a node's OWN
// component-level self-inject put on its pipe carry material that node's
// Component declares sensitive.
//
// It exists because every other reading of the delivery pipe asks about material
// arriving from somewhere else — an attachment's mapping, an edge target's
// provides, a FeatureRecipe's mapping. Self-inject is the one route where a node
// puts its own declared-sensitive datum onto its own delivery keys, so a reading
// that only ever looked outward left it in the clear.
//
// The two authored halves resolve against DIFFERENT declarations, and that is the
// whole content of this reading:
//
//   - `from:` names one of the node's own `provides` keys, so the declaration that
//     governs it is the Component's `provides` schema. The comparison is on the
//     authored key exactly as the delivery pass resolves it, so the two cannot
//     disagree about which declaration a delivery key answers to.
//   - `value:` is a template evaluated against a context that scopes the node's own
//     `values` and `infra` — and carries no parameters field at all, so the
//     pillars a declared-sensitive leaf lives in are precisely what is in scope.
//     The reading is therefore the same reference census every other withholding
//     question uses, over the over-collecting walk, against the node's `values`
//     and `infra` declarations.
//
// A `value:` carrying no template reference is an authored literal: the author
// typed the delivered string, no declaration stands between them and it, so it
// contributes nothing. Mechanisms outside env, file and arg contribute nothing
// either, because the self-inject pass delivers those three and a key never
// placed on the pipe has nothing to withhold.
//
// The reading is STATIC: authored entries plus declared leaf paths. It resolves no
// `from:` and evaluates no template, and it never inspects a delivered value.
func selfInjectWithheld(
	selfInject *types.InjectBlock,
	declaredProvides map[string]bool,
	declaredValues, declaredInfra map[string]bool,
) redact.DeliveryKeys {
	if selfInject == nil {
		return nil
	}
	if len(declaredProvides) == 0 && len(declaredValues) == 0 && len(declaredInfra) == 0 {
		return nil
	}
	templateKeys := redact.Keys{Values: declaredValues, Infra: declaredInfra}
	var out redact.DeliveryKeys
	for _, mechanism := range []struct {
		name string
		raw  any
	}{
		{"env", selfInject.Env},
		{"file", selfInject.File},
		{"arg", selfInject.Arg},
	} {
		entries, err := normalizeSelfInjectEntries(mechanism.raw)
		if err != nil {
			// An entry shape this reading cannot open is a shape the delivery pass
			// cannot open either, so it puts nothing on the pipe. The pass reports
			// the refusal; withholding is not the surface that should.
			continue
		}
		for deliveryKey, entry := range entries {
			withheld := false
			switch {
			case entry.from != "":
				withheld = declaredProvides[entry.from]
			case entry.value != "":
				withheld = templateReadsWithheld(entry.value, templateKeys)
			}
			if !withheld {
				continue
			}
			if out == nil {
				out = redact.DeliveryKeys{}
			}
			if out[mechanism.name] == nil {
				out[mechanism.name] = map[string]bool{}
			}
			out[mechanism.name][deliveryKey] = true
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
