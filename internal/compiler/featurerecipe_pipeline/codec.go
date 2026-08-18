// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package featurerecipe_pipeline

import (
	"encoding/json"
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/jsonpatch"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// Structured reports whether the unit carries an RFC-6902-addressable tree
// view and is therefore a PATCH target.
//
// A unit with a tree is structured. A unit with no tree is opaque: it is still
// handed to the guest, tagged with its media type and bytes so the guest's view
// of the output is complete, and it is a SELECT and EMIT target — but it is not
// an RFC-6902 PATCH target, and a PATCH addressed at one halts loudly rather
// than being silently dropped. What counts as structured is the [Codec]'s call
// — the codec produced a tree — not a fixed parser-accepted test.
func (u Unit) Structured() bool { return u.Tree != nil }

// PatchedUnit is one unit a pipeline step changed, handed to a Codec's
// Reassemble so it can splice the change back into the original bytes by
// token. It carries the unit's token, the patched tree-view, and the ops
// that produced the change — the codec edits the addressed byte-spans by op
// kind (a replace edits the value's span; an add inserts a new member at the
// established child indent; a remove deletes the member's span), so the
// edit is surgical: every byte the ops did not touch survives verbatim.
//
// Every field is a serializable payload: PatchedUnit is Wasm-ABI-shaped, so
// the Codec interface is the future Wasm export contract unchanged. The
// RFC-6901 paths the ops carry are a stable address vocabulary the guest
// already speaks, not an in-process type leaking across the boundary.
type PatchedUnit struct {
	Token string          `json:"token"`
	Tree  json.RawMessage `json:"tree"`
	Ops   []JSONPatchOp   `json:"ops,omitempty"`
}

// Codec is the provider capability that carves a provider's emitted output
// into addressable units and reassembles it after a pipeline step edits
// some of them. It is two halves of ONE paired, byte-faithful contract:
//
//   - Segment splits one named body into ordered units (the host's input
//     contract for a pipeline guest).
//   - Reassemble rebuilds the body, splicing patched units back surgically;
//     every untouched byte survives verbatim.
//
// The pairing is the contract: Reassemble of an unpatched segmentation
// reproduces the body byte-for-byte (round-trip), and reassembling with one
// unit patched leaves every other byte identical (patch-isolation). The two
// methods are bundled into one interface deliberately — they share the
// byte-faithful invariant, and splitting them would invite a custom
// segmenter mixed with a foreign reassembler that silently corrupts bytes.
//
// The core ships a stock YAML codec a provider borrows by default; a
// provider that knows its own output shape overrides the codec to define its
// own units. Either way the guest's op vocabulary is the unchanged canonical
// RFC-6902 — the codec changes how output is carved and spliced, never what
// a guest may ask for.
//
// Both method signatures are Wasm-ABI-shaped: every parameter and result is
// a serializable payload (string, []byte, []Unit / []PatchedUnit of
// serializable fields). The Go interface is therefore the future Wasm
// export contract; the stock codec stays in-core, and only a provider's
// override logic ports to Wasm later.
type Codec interface {
	// Segment carves body (named name) into ordered addressable units. A
	// body the codec cannot tree at all surfaces a typed error the caller
	// turns into a not-structured halt; a body that carves into a mix of
	// structured and opaque units returns those units (opaque units carry
	// MediaType and Bytes but no Tree).
	Segment(name string, body []byte) ([]Unit, error)

	// Reassemble rebuilds body (named name, segmented into segs) with the
	// patched units spliced in by token. A token absent from patched is
	// re-joined from its original byte-range verbatim; a patched token has
	// its addressed spans edited surgically. The result is byte-faithful:
	// untouched bytes are reproduced exactly.
	//
	// The returned warnings are advisory, non-fatal diagnostics the caller
	// raises to the operator — for example, a patched unit whose surgical edit
	// could not be verified against its intended result and was re-rendered in
	// full as a safe fallback. An advisory warning never means the reassembly
	// failed: the returned body is always a correct realization of the patches;
	// only a non-nil error signals a failure the caller must surface as a halt.
	Reassemble(name string, body []byte, segs []Unit, patched []PatchedUnit) (out []byte, warnings []string, err error)
}

// Applier applies RFC-6902 operations to one unit's tree-view and returns
// the patched tree. It is format-agnostic — one stock implementation serves
// every codec because RFC-6902 over a JSON object does not depend on the
// originating format.
//
// This hook is an intentional future-use extension point. It is exposed as
// an overridable seam for future formats whose operation semantics genuinely
// differ from RFC-6902-over-JSON, but no provider overrides it today: the
// stock applier serves every codec. It MUST NOT be removed for being unused
// — it is deliberately retained, not dead code. A provider that needs
// different op semantics supplies its own Applier without touching the codec
// split.
//
// The signature is Wasm-ABI-shaped: tree and the result are json.RawMessage
// and the ops are the canonical wire JSONPatchOp, so the interface is the
// future Wasm export contract unchanged.
type Applier interface {
	Apply(tree json.RawMessage, ops []JSONPatchOp) (json.RawMessage, error)
}

// StockApplier is the one format-agnostic RFC-6902 applier every codec
// borrows. It applies operations over types.OrderedMap so object key order
// is preserved and the re-marshalled tree stays byte-identical across runs
// — the determinism contract the rest of the pipeline upholds. It is the
// single home of the wire-op → engine-op mapping: a JSONPatchOp becomes a
// jsonpatch.Op here and only here, keeping the apply engine free of the ABI
// import (see the Op godoc in package jsonpatch).
type StockApplier struct{}

// Apply applies ops to tree in order and returns the patched tree. The tree
// is decoded into an order-preserving OrderedMap, each op is mapped onto the
// engine's local Op and applied, and the result is re-marshalled. On any
// failing op the original error propagates wrapped with the offending op's
// position; tree is not partially returned.
func (StockApplier) Apply(tree json.RawMessage, ops []JSONPatchOp) (json.RawMessage, error) {
	doc := types.NewOrderedMap[any]()
	if err := json.Unmarshal(tree, doc); err != nil {
		return nil, fmt.Errorf("apply: parse tree: %w", err)
	}
	engineOps := make([]jsonpatch.Op, 0, len(ops))
	for _, op := range ops {
		// The one place a wire JSONPatchOp becomes an engine jsonpatch.Op.
		engineOps = append(engineOps, jsonpatch.Op{Op: op.Op, Path: op.Path, Value: op.Value, From: op.From})
	}
	if err := jsonpatch.Apply(doc, engineOps); err != nil {
		return nil, err
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("apply: re-marshal tree: %w", err)
	}
	return out, nil
}
