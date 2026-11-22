// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package rendercore

import (
	"fmt"
	"sort"

	abi "github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
)

// SensitivityNotConveyedCode is the stable, machine-matchable code carried by
// the advisory raised when a node's render cannot carry a sensitivity
// declaration the author wrote, and the author holds a lever that would let it.
//
// It is a code of its own rather than a member of the unsupported-field class,
// and the suppress flag for that class does not cover it, for the same reason
// SensitiveByValueCode is separate: this reports a judgement about the author's
// own declaration — "what you declared reaches nothing here" — where that class
// reports a target failing to express an authored field. An operator silences
// the two for unrelated reasons, and a single switch over both would let a
// target-fidelity preference also silence a security-adjacent notice.
const SensitivityNotConveyedCode = "sensitivity-not-conveyed"

// SensitivityNotConveyedDiagnostic builds the advisory for one `values` leaf a
// Component declares sensitive on a node whose Recipe declares a values
// mapping. coordinate is the declaration's authored coordinate, spelled from
// the pillar root exactly as every other surface spells one; recipe is the
// bound Recipe's own name, because the declaration and the mapping are
// authored in two different entities and a reader has to be told both. The
// caller attaches the location naming the node and the target.
//
// The diagnostic is warn-severity and out-of-band: the build succeeds and the
// rendered bytes are unchanged. What it reports is an absence — no rendered key
// carries the marking — so there is nothing for it to halt over, and a Recipe
// that maps its values is an ordinary, supported shape rather than a fault.
func SensitivityNotConveyedDiagnostic(recipe, coordinate string) renderctx.Diagnostic {
	return sensitivityNotConveyed("recipe", recipe, coordinate)
}

// FeatureRecipeSensitivityNotConveyedDiagnostic builds the same advisory for
// one `values` leaf a Feature declares sensitive on an attachment whose
// FeatureRecipe declares a values mapping. attachment is the name the Feature
// is attached under; coordinate is the Feature's own authored coordinate,
// spelled from the pillar root as the Recipe case spells a Component's;
// featureRecipe is the bound FeatureRecipe's own name, because it is the entity
// whose mapping gates the join.
//
// It is the Recipe case's advisory under the same code, with the same
// severity and the same sentence, naming the entity kind that holds the lever:
// the attachment's mapped output reaches its target exactly as a Recipe's does,
// and removing the mapping restores the join exactly as it does there. The
// sentence leads with the attachment, as the by-value advisory does, because one
// node can carry several attachments of one Feature through one FeatureRecipe,
// and without the name their lines would be identical.
func FeatureRecipeSensitivityNotConveyedDiagnostic(attachment, featureRecipe, coordinate string) renderctx.Diagnostic {
	d := sensitivityNotConveyed("FeatureRecipe", featureRecipe, coordinate)
	d.Message = fmt.Sprintf("attachment %q: %s", attachment, d.Message)
	return d
}

// sensitivityNotConveyed is the one statement of the advisory's shape and
// wording. gateKind names the kind of entity whose values mapping gates the
// join, as the sentence spells it, and gate is that entity's name.
func sensitivityNotConveyed(gateKind, gate, coordinate string) renderctx.Diagnostic {
	return renderctx.Diagnostic{
		Severity:  abi.SeverityWarn,
		Code:      SensitivityNotConveyedCode,
		SourceRef: coordinate,
		Message: fmt.Sprintf(
			"%q is declared sensitive, but %s %q maps its values through a valuesMapping whose output keys no declaration names, so the target receives no per-key sensitivity marking for it; the mapping's whole output is marked as carrying a value declared sensitive instead wherever the mapping reads such a value and its output changes, or its evaluation fails, when every value declared sensitive or computed from one is replaced by either of two stand-in texts; a %s declaring no valuesMapping renders the declared leaf paths as its own keys, and the per-key marking travels with them",
			coordinate, gateKind, gate, gateKind),
	}
}

// UnconveyedValueLeafDeclarations names the authored `values` coordinates whose
// sensitivity a node's render provably cannot convey while the author still
// holds a lever over it, in the order the advisory reports them. It returns
// nothing when there is nothing to say.
//
// It is the complement of [SensitiveRenderedKeys]'s values join, and it reads
// the same DeclaredSensitivity that join reads, so the advisory and the marking
// cannot describe different declarations: the join is off exactly when
// ValuesAreDeclaredLeaves is false, and that field is recorded by the sub-pass
// that resolved the values rather than re-answered from the Recipe here.
//
// sourced reports that the node dispatches a source-based render, which is the
// only crossing carrying a resolved values map into a guest. It is part of the
// condition rather than an optimisation, and the narrowing is deliberate:
//
//   - On an artifacts placement the dispatch carries no values map at all, so a
//     values leaf is unconveyable there permanently — the shape produces no key
//     a declaration could name. There is nothing an author can do about it, and
//     an advisory naming no available action fires on every build of every such
//     node forever.
//   - On a workload-only render the dispatch carries the evaluated workload and
//     an empty values map, so the same holds for the same reason: what that
//     shape can convey is a slot declaration, not a values leaf.
//   - On a sourced render whose Recipe declares a values mapping, the keys are
//     the mapping's own invention and the join is gated off — but the mapping is
//     authored, so removing it restores the join. That is the one shape where
//     the author is told, because it is the one shape where being told names
//     something to do. A component-scope attachment's sourced render whose
//     FeatureRecipe declares a values mapping is the same shape, asked of the
//     attachment's own declarations, and is answered by this same reading. A
//     boundary-scope attachment's is not asked at all: its values pillar is
//     empty by construction, so no declared leaf can carry a datum there and
//     removing the mapping restores nothing — the lever this case names is
//     absent, as it is on the two shapes above.
//
// The coordinates come back sorted. Their source is a declaration set keyed by
// leaf path with no author order to preserve, so sorted is the only
// deterministic order available — the same reason the marking's own keys arrive
// sorted on the wire — and a deterministic order is what keeps the advisory
// stream byte-stable across runs of identical input.
func UnconveyedValueLeafDeclarations(declared DeclaredSensitivity, sourced bool) []string {
	if !sourced || declared.ValuesAreDeclaredLeaves || len(declared.ValueLeaves) == 0 {
		return nil
	}
	coordinates := make([]string, 0, len(declared.ValueLeaves))
	for leaf := range declared.ValueLeaves {
		coordinates = append(coordinates, declarationRootValues+leaf)
	}
	sort.Strings(coordinates)
	return coordinates
}
