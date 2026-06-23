// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package jsonpatch applies RFC 6902 JSON Patch operations to an
// order-preserving document model.
//
// The engine operates over the same ordered representation the rest of
// the compiler round-trips JSON through: a JSON object is a
// [types.OrderedMap] (so object key order is preserved across every
// operation), a JSON array is a []any, a JSON number is a json.Number,
// and strings, booleans, and null are their plain Go values. Applying a
// patch never decodes a document through a map[string]interface{}, so
// object key order survives mutation and emitted output stays
// byte-identical across runs of identical input.
//
// The engine is apply-only. It implements the six RFC 6902 operations —
// add, remove, replace, move, copy, test — and RFC 6901 JSON Pointer
// resolution, but it does not invert a patch. Rollback is the caller's
// concern, handled by snapshotting the document before a step and
// restoring the snapshot on failure; an apply-only engine never needs
// to compute an inverse.
//
// The package is a provider-agnostic leaf: it imports only the ordered
// document model, never a Wasm-ABI or renderer type. A caller that
// holds ABI-shaped patch operations maps them onto [Op] at its own call
// site; the engine stays reusable by any patch producer.
package jsonpatch
