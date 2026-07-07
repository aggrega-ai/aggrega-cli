// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package rendercore holds the loader-free primitives of the render pipeline:
// the provider-render socket wire types, the per-node render and provenance
// context structs, the deterministic YAML codec and its multi-document
// segmenter, and the pure placement, provenance, and value-flattening helpers.
//
// The package imports no plugin loader and no host runtime, so provider render
// guests and the per-provider render subpackages depend on it without linking
// the compiler's execution machinery. The render orchestration package builds
// on top of these primitives and adds the loader-bound dispatch.
package rendercore
