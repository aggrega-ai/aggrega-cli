// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package rendercore

import (
	"encoding/json"
	"fmt"
	"reflect"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"go.yaml.in/yaml/v3"
)

// verifyPatchedBody checks that a surgically-edited document body realizes ops
// faithfully. It re-parses the edited body and asserts its decoded content
// equals the original body's decoded content with the same ops applied. The
// intended result is derived from the SAME ops the surgical edit executed, so
// the guard and the edit cannot disagree on what the patch was meant to do.
//
// The check is mechanical and content-agnostic: it reasons about parsed values,
// never YAML construct kinds. It therefore catches any edit that moved,
// absorbed, orphaned, or mis-valued content — a sibling swallowed into a
// neighbor's span, a fragment orphaned past the edit, a value landed at the
// wrong path — including a node-span miscalculation the byte-surgery might still
// carry. Because it decodes the result independently (never reusing the span
// math the edit used), it is not blinded by a shared bug in that math.
//
// It returns true when the edit is verified. On any mismatch — an unparseable
// result, a decode failure, or a decoded-content difference — it returns false,
// and the caller falls back to a full re-marshal.
func verifyPatchedBody(original, edited []byte, ops []frp.JSONPatchOp) bool {
	editedTree, err := YAMLBodyToJSON(edited)
	if err != nil {
		return false
	}
	expected, err := intendedTree(original, ops)
	if err != nil {
		return false
	}
	return jsonValueEqual(expected, editedTree)
}

// intendedTree is the intended result of applying ops to the original document
// body: the original decoded to a tree-view, with ops applied through the
// shared RFC-6902 applier. It is the single source of "what the patch was meant
// to produce", shared by the verify guard and the re-marshal fallback so the
// two cannot diverge on intent.
func intendedTree(original []byte, ops []frp.JSONPatchOp) (json.RawMessage, error) {
	origTree, err := YAMLBodyToJSON(original)
	if err != nil {
		return nil, err
	}
	return frp.StockApplier{}.Apply(origTree, ops)
}

// jsonValueEqual reports whether two JSON documents encode the same value,
// independent of object key order and of equivalent number spellings. Both
// inputs decode through the standard decoder (objects to maps, numbers to a
// single numeric type) and compare by deep value equality, so a correct patch
// whose surgical output differs from the applier's output only in key ordering
// does not trip the guard.
func jsonValueEqual(a, b json.RawMessage) bool {
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}

// remarshalPatchedBody is the guaranteed-correct fallback when a surgical edit
// fails verification. It applies ops to the original body's decoded tree and
// re-marshals the whole document as YAML at the configured indent. The result
// is correct by construction and deterministic — object order is preserved
// through the ordered decode, and the marshal is the codec's single indent-aware
// entry point — at the cost of the byte-surgical minimality that preserves
// comments, anchors, and original scalar formatting.
func remarshalPatchedBody(original []byte, ops []frp.JSONPatchOp, indent int) ([]byte, error) {
	expected, err := intendedTree(original, ops)
	if err != nil {
		return nil, err
	}
	// JSON is a subset of YAML: parse the intended tree into a node so scalar
	// types (numbers, booleans, strings) survive, and key order is carried from
	// the applier's order-preserving JSON. Force block layout so the fallback
	// reads as conventional YAML rather than inline flow collections.
	var node yaml.Node
	if err := yaml.Unmarshal(expected, &node); err != nil {
		return nil, err
	}
	blockifyNodeStyle(&node)
	return MarshalYAMLIndent(&node, indent)
}

// blockifyNodeStyle clears the flow-style flag on every mapping and sequence in
// the tree so a JSON-shaped intended tree re-marshals as block YAML rather than
// inline `{...}`/`[...]` collections. Scalar styles are left as parsed, so a
// number stays unquoted and a string keeps its quoting.
func blockifyNodeStyle(n *yaml.Node) {
	if n == nil {
		return
	}
	if n.Kind == yaml.MappingNode || n.Kind == yaml.SequenceNode {
		n.Style &^= yaml.FlowStyle
	}
	for _, c := range n.Content {
		blockifyNodeStyle(c)
	}
}

// PatchVerificationWarning is the user-facing advisory emitted when a surgical
// in-place patch of a unit does not verify against its intended result, so the
// codec re-rendered the whole unit as a guaranteed-correct fallback. The
// re-render preserves the patched values exactly; it may not preserve the
// comments and original formatting that the byte-surgical path keeps.
func PatchVerificationWarning(token string) string {
	return fmt.Sprintf(
		"in-place patch of %q could not be verified as byte-exact; the document was re-rendered in full so the patched result is correct. Comments and original formatting in this document may not be preserved.",
		token,
	)
}
