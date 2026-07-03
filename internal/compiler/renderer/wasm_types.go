// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
)

// The Provider render socket wire structs — RenderNodeInput, IRNodeProjection,
// CatalogProjection, SourceProjection, SourceFile, RenderNodeOutput,
// RenderedFile, AssembleBoundaryInput, RenderedNode, RenderEdge, and
// AssembleBoundaryOutput — are generated from the canonical provider render
// schema into providerrender_host_gen.go. This file carries the hand-written
// companions the generator does not emit: the type aliases that keep the shared
// cross-boundary types single-sourced in the renderctx leaf package.
//
// Render-time AOT-purity contract: a Provider guest renders deterministically
// from the RenderNodeInput payload and the read-only host functions alone. It
// performs no network access, no clock or random reads, and no filesystem or
// environment access — a sourced provider reconstructs its source in memory from
// the payload's SourceProjection rather than reading the chart from disk. Given
// identical input it produces identical serialized bytes per file; the host
// writes those bytes verbatim and runs no serializer, so the on-disk output is
// byte-identical to what the guest emitted. Determinism is the guest's
// responsibility: identical input must produce identical bytes.
//
// **Status: contract definition only — not yet executed.** These shapes ship
// the JSON ABI a Provider Wasm guest binds against; no production caller in
// this repository constructs or consumes the payload today. The runtime that
// invokes the guest and the reference Provider plugin that renders against this
// shape land in later work. The shape is stable surface so that work can bind
// against it now.

// RenderContext is the render-time context a Provider guest receives.
// It is a type alias for [renderctx.RenderContext]: the type lives in the
// leaf renderctx package so the feature-recipe-pipeline ABI surface can
// share it without importing the renderer, and the renderer re-exports it
// under its original name here for the in-package render call sites.
type RenderContext = renderctx.RenderContext

// RenderAnnotations carries the three annotation buckets a guest reads.
// It is a type alias for [renderctx.RenderAnnotations].
type RenderAnnotations = renderctx.RenderAnnotations

// BoundaryContext is the per-render boundary metadata a guest receives.
// It is a type alias for [renderctx.BoundaryContext].
type BoundaryContext = renderctx.BoundaryContext

// Diagnostic is the shared diagnostic a Wasm guest raises across the
// plugin ABI surfaces. It is a type alias for [renderctx.Diagnostic]: the
// type lives in the leaf renderctx package so the linter and
// feature-recipe-pipeline ABI surfaces can import this exact type without
// importing the renderer, and the renderer re-exports it under its
// original name here.
type Diagnostic = renderctx.Diagnostic

// DiagnosticLocation pins a [Diagnostic] to a source position. It is a
// type alias for [renderctx.DiagnosticLocation].
type DiagnosticLocation = renderctx.DiagnosticLocation
