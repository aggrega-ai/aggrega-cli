// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package parsercore holds the pure text-to-tree conversions the embedded
// starter parser guests run. A parser turns a foreign source text into a
// canonical structured tree (JSON) so any consumer can read the tree without
// carrying its own language parser. Each conversion is a pure bytes-to-tree
// function with no filesystem, network, or ambient state, so the same source
// bytes always yield a byte-identical tree.
//
// The functions live here, outside the guest main packages, so they compile and
// run on the host toolchain and are exercised directly in tests — the guest main
// packages are built only for GOOS=wasip1 and hold nothing but the ABI decode,
// the call into this package, and the ABI encode.
//
// Byte-identical output: every conversion builds a Go value (a map keyed by
// string, a slice, or a scalar) and encodes it with encoding/json, which sorts
// map keys, so the emitted tree is deterministic regardless of the source
// language's own iteration order. Numbers decode into json.Number so a large
// integer keeps its exact digits rather than degrading through float64.
package parsercore

import (
	"bytes"
	"encoding/json"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
)

// nullTree is the canonical tree for a source that failed to parse: a JSON null.
// ParseOutput.Tree is a required wire field, so a diagnostic-only result still
// carries a syntactically valid tree rather than an empty one.
var nullTree = json.RawMessage("null")

// errorOutput returns a ParseOutput carrying only an error diagnostic and a null
// tree. It is the shape every conversion returns when the source is malformed:
// the parse reports the failure as a diagnostic rather than crashing the guest.
func errorOutput(message string) abi.ParseOutput {
	return abi.ParseOutput{
		Tree:        cloneRaw(nullTree),
		Diagnostics: []abi.ParseDiagnostic{{Severity: "error", Message: message}},
	}
}

// treeOutput encodes a parsed Go value into the canonical JSON tree. encoding/json
// sorts map keys and renders json.Number verbatim, so the tree is byte-identical
// across runs of the same input. An encoding failure degrades to an error
// diagnostic rather than a partial tree.
func treeOutput(value any) abi.ParseOutput {
	raw, err := marshalDeterministic(value)
	if err != nil {
		return errorOutput("encode parsed tree: " + err.Error())
	}
	return abi.ParseOutput{Tree: raw}
}

// marshalDeterministic encodes value with HTML escaping disabled and no trailing
// newline, so a value containing "<", ">", or "&" is not rewritten to < and
// the output is a bare JSON document. Key order is encoding/json's sorted order.
func marshalDeterministic(value any) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return nil, err
	}
	return json.RawMessage(bytes.TrimRight(buf.Bytes(), "\n")), nil
}

// cloneRaw returns an independent copy of a json.RawMessage so a returned tree
// never aliases the shared nullTree backing array.
func cloneRaw(r json.RawMessage) json.RawMessage {
	out := make(json.RawMessage, len(r))
	copy(out, r)
	return out
}
