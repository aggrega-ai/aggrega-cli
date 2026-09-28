// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// NodeResolvesToARecipe reports each component instance in the composition that
// the Manifest's mapping does not resolve to a Recipe the catalog holds.
//
// Resolution is mapping-only: the catalog never chooses a Recipe by what one
// declares it implements, so an instance no mapping reaches halts however many
// Recipes implement its type. The implementing Recipes travel with the finding
// as pin candidates — what a Recipe declares informs the choice, the author
// makes it, and the mapping entry records it.
//
// # Why it reads the flattened composition
//
// The node set it walks has to be complete, and it is complete only once the
// composition is flattened. A component a sub-aggregate contributes is declared
// in that sub-aggregate's document, which is read at flattening, so a rule asked
// over the Aggregate's own instance list would never be invoked for one — and
// a rule that is never invoked reports exactly what a rule that ran and approved
// reports. That silence is the failure this placement exists to prevent: an
// unmapped component inside a module would reach rendered output with nothing
// said about it.
//
// Everything else it reads is complete earlier — the mapping table is an input,
// and the catalog is reachable from the stage before this one — so the node set
// is what places the rule, and this is the earliest stage that holds it. It is
// walked there too, over the composition's own node axis.
//
// # Why it refuses only a run that emits
//
// An instance that resolved to no Recipe has nothing to render through, so a run
// that emits artifacts from this composition would be emitting them for a node
// it cannot render — which is why the finding refuses such a run. A run that
// only DESCRIBES the composition is in the opposite position: a picture of the
// composition, with this finding drawn on the instance it is about, is exactly
// what an author needs in order to write the mapping entry. Refusing there would
// withhold the answer at the moment the reader is trying to act on it, so the
// severity says "refuse a run that emits" rather than "refuse".
//
// # The three outcomes it passes over, and why each is somebody else's
//
// A lookup that REFUSED because the instance's type matched several
// version-distinct mapping keys is passed over here and reported by the rule
// beside this one. The refusal names no reference, so it reads as unmapped, and
// reporting it here would tell the author to write an entry the mapping table
// already carries twice.
//
// A reference whose SHAPE no Recipe reference can express is passed over too.
// The Recipe it names may well be in the catalog, so calling it absent would
// send the author looking for the wrong thing; the shape is reported by the
// reading that owns it, both over the authored mapping entry and at the instance
// whose resolution missed. A reference whose version pin is malformed is passed
// over for the same reason and reported by the same instance reading: the pin
// names no version, so the fault is its text rather than an absent Recipe.
//
// A node whose resolution was never performed — a build whose catalog holds no
// recipe index — reads as an instance nothing maps, which is the same reading
// the catalog-presence facts take for a build without a catalog.
var NodeResolvesToARecipe = constraint.Check[
	constraint.TopologyView,
	constraint.NodeSubject,
]{
	Sentinel: ErrNoRecipeResolved(),
	Severity: constraint.SeverityEmissionFatal,
	Run: func(v constraint.TopologyView, s constraint.NodeSubject) []constraint.Objection {
		node := s.NodeID()
		// The refusal is read first, because it answers "not mapped" to the
		// question below and the two faults have opposite remedies.
		if len(v.NodeRecipeMappingAmbiguousKeys(node)) > 0 {
			return nil
		}
		componentType, _ := v.FlattenedNodeComponentType(node)

		reference, mapped := v.NodeMappedRecipeReference(node)
		if mapped {
			if v.NodeMappedRecipePresent(node) {
				return nil
			}
			if _, malformed := resolver.RecipeReferenceSegments(reference); malformed {
				return nil
			}
			if types.ParseVersionPin(reference).Fault() != nil {
				return nil
			}
			return []constraint.Objection{s.RaiseWithAdvice(
				ErrNoRecipeResolved(),
				"correct the mapping value in the Manifest, or publish the recipe it names",
				constraint.Uncaptioned(node),
				constraint.Captioned("of type", componentType),
				constraint.Captioned("mapped to recipe", reference),
			)}
		}

		candidates := v.NodeRecipePinCandidates(node)
		if len(candidates) == 0 {
			return []constraint.Objection{s.RaiseWithAdvice(
				ErrNoRecipeResolved(),
				"no recipe in the catalog implements this type; write one, then map the type to it",
				constraint.Uncaptioned(node),
				constraint.Captioned("of type", componentType),
			)}
		}
		// The candidates travel as one value rather than one detail each: they are
		// a single answer to one question — which Recipes could be pinned — and a
		// reader needs them together to choose between them.
		return []constraint.Objection{s.RaiseWithAdvice(
			ErrNoRecipeResolved(),
			"pin one of them in the Manifest",
			constraint.Uncaptioned(node),
			constraint.Captioned("of type", componentType),
			constraint.Captioned("recipes implementing it", strings.Join(candidates, ", ")),
		)}
	},
}

// NodeTypeMappingIsUnambiguous reports each component instance whose type the
// Manifest's mapping table matches at more than one version-distinct key, with
// no key matching it exactly.
//
// The lookup cannot choose between such keys without guessing, so it refuses
// rather than resolving whichever came first — a silent first-wins would pin a
// source the author never selected.
//
// # It is a fault of the instance against the table, not of the table alone
//
// A mapping table holding one type at two distinct versions is legal authoring,
// and a rule asked once per mapping entry would have to refuse it — which would
// reject documents that build today. An exact key match wins before any base
// match is considered, so the same table resolves an instance whose authored
// type pins a version and refuses one that does not. The rule is therefore
// asked once per instance, where both halves of the question are in hand.
//
// It reads the flattened composition for the reason the rule beside it does:
// the instance set has to be complete, and a component a sub-aggregate
// contributes joins it only when the composition is flattened. It is walked
// where that rule is walked, so the pair reports on one channel rather than
// splitting one question across two, and it refuses at the same severity for the
// same reason: an instance whose lookup refused resolved to no Recipe, so a run
// that emits has nothing to render it through while a run that describes the
// composition can show the author which keys collided.
var NodeTypeMappingIsUnambiguous = constraint.Check[
	constraint.TopologyView,
	constraint.NodeSubject,
]{
	Sentinel: ErrAmbiguousTypeMapping(),
	Severity: constraint.SeverityEmissionFatal,
	Run: func(v constraint.TopologyView, s constraint.NodeSubject) []constraint.Objection {
		node := s.NodeID()
		keys := v.NodeRecipeMappingAmbiguousKeys(node)
		if len(keys) == 0 {
			return nil
		}
		componentType, _ := v.FlattenedNodeComponentType(node)
		// The colliding keys travel as one value: they are the two ends of a
		// single fact — which keys the type matched — and a reader needs them side
		// by side to see that the pair is the finding.
		return []constraint.Objection{s.RaiseWithAdvice(
			ErrAmbiguousTypeMapping(),
			"pin the authored type's version, or remove the duplicate mapping keys",
			constraint.Uncaptioned(componentType),
			constraint.Captioned("declared by component", node),
			constraint.Captioned("matches version-distinct keys", strings.Join(keys, ", ")),
		)}
	},
}
