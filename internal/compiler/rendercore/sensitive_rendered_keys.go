// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The two declaring blocks a marked key inherits its sensitivity from, used as
// the prefix of the coordinate carried as the marking's value.
//
// A bare name would be ambiguous: one map carries keys recovered from two
// different declaring blocks, and a slot and a values leaf may legitimately
// share a name, so a consumer reading a bare `password` could not tell which
// declaration it was pointed at. Both prefixes are the block's own authored
// spelling, so the coordinate reads as the path the author typed.
const (
	declarationRootValues = "values."
	declarationRootSlots  = "featuresSchema."
)

// DeclaredSensitivity carries the sensitivity DECLARATIONS one node's render
// dispatch needs in order to name a rendered key. It holds declarations only:
// nothing here is a rendered identity, and nothing here is derived from a
// template, a value or a name.
//
// The three fields are grouped rather than passed as separate arguments because
// ValueLeaves is meaningless without ValuesAreDeclaredLeaves, and a caller
// pairing a leaf set with the wrong gate would mark a key no declaration names.
//
// The zero value declares nothing, which marks nothing. That default is the
// safe direction for this axis and it is the opposite of the default that is
// safe for withholding: an unset field here loses a mark a reader could have
// had, where an unset field on a withholding set would print a datum an author
// asked to be withheld. Marking and withholding are separate concepts with
// opposite fail-safe directions, so neither one's inputs may be reused for the
// other.
type DeclaredSensitivity struct {
	// ValueLeaves are the `values` leaf paths the node's Component declares
	// sensitive, spelled as the flat-dotted path from the pillar root — the same
	// spelling the resolved values map a provider engine receives uses for its
	// keys, which is what makes the join exact rather than approximate.
	ValueLeaves map[string]bool

	// ValuesAreDeclaredLeaves reports that the resolved values map's keys ARE
	// the declared leaf paths. It is false where a Recipe declares a values
	// mapping, because the mapping is one whole-document template and its output
	// keys are the template's own invention: a declaration names none of them,
	// so no key of that map can be recovered from one and the whole join is off.
	// It carries the same name as the render state's own field for the same fact,
	// because it is that fact rather than a second one.
	ValuesAreDeclaredLeaves bool

	// Slots are the declared attachment slots the node's Component declares
	// sensitive, by slot name. A workload's attachment entry names one of these
	// through its `from` field, so the join runs declaration to declaration.
	//
	// This is the slot declaration itself and NOT the per-attachment roll-up the
	// graph carries: the roll-up is true whenever the attached entity declares
	// any sensitive leaf anywhere, which says nothing about the specific datum
	// one rendered key reads, and spreading it across individual keys would
	// assert something false about a public one.
	Slots map[string]bool
}

// SensitiveRenderedKeys joins a node's sensitivity declarations to the
// identities its render actually produces, returning each recovered rendered key
// mapped to the authored coordinate of the declaration it inherited that
// sensitivity from. It returns nil when nothing is recoverable, so a payload
// carrying no recovery carries no key at all.
//
// The recovery is EXACT or it does not happen. Two joins qualify, one per
// rendered shape, and each pairs a declaration with an identity that is already
// spelled the same way:
//
//   - The resolved values a sourced render receives are keyed by the declared
//     leaf paths, so a key equal to a declared-sensitive leaf path is that
//     leaf. Gated on ValuesAreDeclaredLeaves, because a values mapping's output
//     keys are invented by a template and a coincidental match to a declared
//     path would be a name collision rather than a recovery.
//   - A workload's container attachment entry names the declared slot it reads
//     through its `from` field, and the entry's own map key is its identity in
//     the rendered workload — the variable name where it delivers as an
//     environment variable, the entry naming the mount where it delivers as a
//     file. Either way the entry is what a guest addresses, so the map key is
//     the rendered identity and the slot declaration governs it.
//
// Everything else stays absent, and absence is a positive statement that no
// key's identity could be recovered — never that nothing on the payload carries
// sensitive material. Nothing here reads a key's own text, infers a meaning from
// a name, or inspects a value.
//
// The caller supplies exactly the data its own render shape carries, which is
// what keeps the two joins from overlapping and what makes a shape carrying
// neither — an artifacts placement, whose keys and file content are authored
// inside templates — yield nothing structurally rather than by a branch.
//
// The walk is deterministic: containers in author order, then init containers in
// their positional order, then each container's attachment entries in author
// order, with the first recovery of a key kept. Two containers may legitimately
// spell one attachment key against different slots, so a fixed traversal is what
// makes the result byte-identical across runs of identical input. The result is
// read as a lookup keyed by the rendered key, so its own iteration order carries
// no information and the wire codec sorts the keys it emits.
func SensitiveRenderedKeys(declared DeclaredSensitivity, values types.OrderedMap[any], w *types.Workload) map[string]string {
	var out map[string]string
	mark := func(renderedKey, declaration string) {
		if renderedKey == "" {
			return
		}
		if out == nil {
			out = map[string]string{}
		}
		if _, seen := out[renderedKey]; seen {
			return
		}
		out[renderedKey] = declaration
	}

	if declared.ValuesAreDeclaredLeaves && len(declared.ValueLeaves) > 0 {
		for key := range values.All() {
			if declared.ValueLeaves[key] {
				mark(key, declarationRootValues+key)
			}
		}
	}

	if w != nil && len(declared.Slots) > 0 {
		markContainer := func(c types.ContainerSpec) {
			for renderedKey, ref := range c.Attachments.All() {
				if declared.Slots[ref.From] {
					mark(renderedKey, declarationRootSlots+ref.From)
				}
			}
		}
		for _, c := range w.Containers.All() {
			markContainer(c)
		}
		for _, c := range w.InitContainers {
			markContainer(c)
		}
	}

	return out
}

// NodeProjection builds the wire node projection from the host node context and
// the data the dispatching render shape carries — the inverse of the guest's own
// context reconstruction. It carries the identity and provenance the render core
// stamps onto every emitted manifest, plus the node's attachments in author
// order: an attachment entry carries the derived sensitivity marking, which is
// what lets a guest render a target-native sensitivity primitive rather than
// guess from a value.
//
// It is the ONE place a node projection is built for a render dispatch, which is
// what makes the sensitivity marking impossible to omit from a dispatch: a shape
// added later reaches the guest through this call or not at all, and it declares
// what it carries by what it passes. values is the resolved values map a sourced
// render consumes and is empty on every other shape; w is the evaluated workload
// a synthesizing render consumes and is nil on every other shape. A shape
// carrying neither — an artifacts placement — therefore recovers no rendered key
// without a branch of its own having to say so.
//
// Type, resolved recipe, and uses edges stay unset, each for its own reason. The
// component type is opaque metadata rather than a behavior switch, so carrying it
// would only invite a guest to branch on it. The bound Recipe's identity already
// reaches the guest as the recipe name and version it stamps into provenance, so
// the resolved URI adds nothing it renders from. And an outbound edge is
// consumed before dispatch: the edge's provides are rehydrated into the node's
// own inject pipe, which the Recipe has already evaluated into the values and
// workload the guest receives, while a boundary assembler that needs the topology
// itself gets the edge list on its own input.
func NodeProjection(nodeCtx NodeRenderContext, values types.OrderedMap[any], w *types.Workload) IRNodeProjection {
	return IRNodeProjection{
		ID:                    nodeCtx.NodeID,
		Values:                values,
		Features:              nodeCtx.Attachments,
		SensitiveRenderedKeys: SensitiveRenderedKeys(nodeCtx.DeclaredSensitivity, values, w),
		BoundaryPath:          nodeCtx.BoundaryPath,
		SyncWave:              nodeCtx.SyncWave,
		RecipeName:            nodeCtx.RecipeName,
		RecipeVersion:         nodeCtx.RecipeVersion,
		LabelComponentID:      nodeCtx.LabelComponentID,
		OutputLabels:          nodeCtx.OutputLabels,
		Provenance: NodeProvenance{
			CompilerVersion: nodeCtx.Provenance.CompilerVersion,
			AggregateName:   nodeCtx.Provenance.AggregateName,
			Environment:     nodeCtx.Provenance.Environment,
			ProviderName:    nodeCtx.ProviderName,
			ProviderVersion: nodeCtx.ProviderVersion,
		},
	}
}
