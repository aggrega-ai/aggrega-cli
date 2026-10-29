// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package stepwalk is the host's runtime for a Recipe or FeatureRecipe
// `pipeline:`. [Walk] evaluates each step's `with` (and its optional `when`
// gate), dispatches each step's Wasm guest through the invoker its caller binds,
// checks what the guest returned at the plugin socket boundary, applies the
// returned patches to the named documents, restores the document set by snapshot
// on a failed step, and accumulates the side effects. The renderer drives [Walk]
// for every pipeline at render time.
//
// It is a package of its own, apart from the pipeline-step contract it speaks,
// because the two have different readers. The contract — the document set, the
// step input and output, the codec types — is linked into provider guests as well
// as into the host, while the walk runs only in the host and reads the host's
// plugin loader to check a guest's answer. Keeping the walk here keeps that loader,
// and the WebAssembly runtime beneath it, out of every guest that links the
// contract.
package stepwalk
