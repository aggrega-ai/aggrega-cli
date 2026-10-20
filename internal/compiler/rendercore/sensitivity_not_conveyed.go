// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"fmt"
	"sort"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
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
	return renderctx.Diagnostic{
		Severity:  abi.SeverityWarn,
		Code:      SensitivityNotConveyedCode,
		SourceRef: coordinate,
		Message: fmt.Sprintf(
			"%q is declared sensitive, but recipe %q maps its values through a valuesMapping whose output keys no declaration names, so the target receives no per-key sensitivity marking for it; where the mapping reads it, the mapping's whole output is marked as carrying a value declared sensitive instead; a recipe declaring no valuesMapping renders the declared leaf paths as its own keys, and the per-key marking travels with them",
			coordinate, recipe),
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
//     something to do.
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
