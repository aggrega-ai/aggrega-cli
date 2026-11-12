// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package codecconformance is the shared determinism + correctness contract
// every provider codec must pass. A codec carves a provider's emitted output
// into addressable units and reassembles it after a pipeline step edits some
// of them; whether it is the core's stock YAML codec or a provider override,
// it must hold the same three property families this package exercises:
//
//   - DETERMINISM — the same input produces byte-identical output across many
//     runs, so a guest codec cannot leak map-iteration order into the
//     rendered artifacts.
//   - ROUND-TRIP + PATCH-ISOLATION — reassembling an unpatched segmentation
//     reproduces the body byte-for-byte, and patching one unit leaves every
//     other unit byte-identical.
//   - OP-APPLY CONTRACT — RFC-6902 application over a unit's tree-view behaves
//     to spec, a PATCH against an opaque (no-tree) unit cannot be applied, and
//     a PATCH against a token the codec never produced is rejected.
//
// Run is the entry point: a table-driven harness any test calls with a
// concrete [frp.Codec] and a corpus of [Case] values. The package imports
// only the codec/applier contract (featurerecipe_pipeline) and the standard
// library, so both the renderer's stock-codec tests and a provider package's
// override tests import it without a dependency cycle.
//
// The applier used for the op-apply family is the one stock applier every
// codec borrows ([frp.StockApplier]): the harness holds a custom codec's
// apply to the same RFC-6902 bar the core's apply engine is held to.
package codecconformance
