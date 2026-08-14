// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package trace provides per-pass intermediate-state capture for the
// `aggrega build --trace` debug surface. When tracing is on, each
// pipeline pass writes a YAML-serialised snapshot of the state visible
// at that pass under `.trace/boundaries/<boundary>/<instance_id>/`
// (sibling to `dist/`, never under). The sibling layout keeps `dist/`
// purely deployable: no flag-conditional contents, no apply-tool
// walking surprises, no operator-authored boundary `directory:`
// collision risk against an Aggrega-managed dot-dir.
//
// The Pass enumeration names every snapshot slot the layout produces:
// provides evaluation through feature rendering (FeatureRecipe Resource
// Generation), plus PassEmission (post-render emission log) and PassLabels
// (provenance label injection log).
//
// PillarSnapshot returns the projection of a TemplateContext appropriate
// for a given Pass — only the pillars populated by that pass appear; later
// pillars are absent (not nil), matching the compiler's actual visibility
// at the failing point. This makes error messages honest: a source-resolution
// failure does not show an empty .workload, because .workload is not
// populated until workload synthesis has run.
//
// # Writers
//
// [Writer] has three implementations, chosen by the caller rather than by
// this package: a no-op writer when tracing is off, an asynchronous
// filesystem writer when tracing is on, and [MemoryWriter], which collects
// the same payloads into a map for a caller that does its own writing. Every
// capture site calls Write unconditionally and asks [Captures] whether
// anything will read the result, so no site branches on which implementation
// it is holding.
//
// # The capture path never blocks the render
//
// The filesystem writer accepts events on a bounded channel drained by one
// dedicated goroutine. Write marshals the payload and performs a non-blocking
// send: when the buffer is full the event is dropped and the call returns an
// error rather than the calling goroutine waiting on disk. Compile time is
// therefore independent of how fast the trace is written, and a slow disk
// costs trace completeness instead of build latency.
//
// A drop is a signal rather than a normal mode of operation. The buffer is
// sized to absorb a whole render's burst with headroom, so saturation means
// either that the consumer has stalled or that real demand has outgrown the
// buffer — both worth investigating rather than tuning away. [Stats] exposes
// the counters that make a drop observable.
//
// # One consumer, because ordering is the determinism contract
//
// The single consumer goroutine receives events in the order they were sent
// and is the only writer touching the output files, so two compiles of the
// same input produce byte-identical trace output. That cardinality is
// load-bearing rather than incidental: a second consumer would race on
// per-file writes, and reproducibility is precisely the property the trace is
// inspected for. Closing the writer drains the buffer and joins the consumer,
// so a Close that has returned means every accepted event is on disk.
package trace
