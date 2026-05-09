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
// Pass 1 (provides) through Phase B.6 (FeatureRecipe Resource Generation),
// plus PassEmission (post-render emission log) and PassLabels (provenance
// label injection log).
//
// PillarSnapshot returns the projection of a TemplateContext appropriate
// for a given Pass — only the pillars populated by that pass appear; later
// pillars are absent (not nil), matching the compiler's actual visibility
// at the failing point. This makes error messages honest: a Phase B.0
// failure does not show an empty .workload, because .workload is not
// populated yet at Phase B.0.
//
// The Writer interface has two implementations: a no-op writer used when
// tracing is off, and an asynchronous filesystem writer used when tracing
// is on. The fs writer uses a bounded channel + dedicated goroutine so
// trace I/O does not dominate compile time.
package trace
