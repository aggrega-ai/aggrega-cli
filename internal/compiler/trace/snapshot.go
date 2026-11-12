// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package trace

import (
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// PillarSnapshot returns the projection of a TemplateContext appropriate
// for the given Pass. Only pillars populated by that pass appear in the
// returned map; later pillars are absent (not nil), matching the
// compiler's actual visibility at the failing pass.
//
// keys names the data the projection must withhold — both what an author
// declared sensitive and what a template computed from such a declaration. It is
// computed by the caller — which holds the node's attachments, the catalog and
// the bound Recipe's authored template text — and passed in; this package derives
// no sensitivity of its own, reads no catalog and parses no template. The
// parameter is required rather than optional so a new capture site cannot
// silently produce an unredacted snapshot: the only way to opt out is to pass an
// empty set, which is a visible choice. An empty set leaves the projection
// byte-identical to one taken before the axis existed.
//
// Two roots withhold differently from the rest, and the difference is worth
// naming because it changes what a reader sees.
//
// .context.uses is a CROSS-NODE projection: a uses edge publishes its target's
// resolved provides into this node's display, so those values are governed by the
// target Component's declarations rather than this node's. The set passed here
// carries them per alias, so a value the target declared sensitive is withheld on
// the consumer's display even when the consumer declares nothing. The attachments
// written on a connection are displayed under its edge too, and each is governed
// by its own Feature's declarations; the set carries those per alias and slot,
// and the one applier for the uses map withholds both.
//
// .workload is not addressable by key. It is a parsed document: a datum a
// template interpolated into it lands in a field no author declared, and nothing
// links that field back to the declaration it came from. So the projection is
// governed by one display decision the set carries rather than by keys within it,
// and this package applies that decision without making it — the document it
// shows arrives on the context already, produced by whoever re-rendered it.
//
// Substituted is the ordinary case: a re-render of the same template against a
// context whose declared-sensitive data reads as the placeholder, so the document
// keeps its shape and only the substituted datum's position differs. It is
// announced in a sibling entry, because a reader told nothing would take a
// re-render for the document the build emitted. Unavailable is the fallback for a
// re-render that could not be produced, and shows the withholding statement — an
// absent projection would be a hole the reader cannot see.
//
// .boundary is displayed verbatim. It is the boundary-scope view a FeatureRecipe
// with that execution scope renders against, and it carries every subtree edge's
// target provides; the set passed here is one component node's, which does not
// span a boundary subtree. No capture site reaches this branch today, because the
// boundary-scope render builds its own context and runs without a capture writer.
//
// The mapping table:
//
//   - provides evaluation: values, infra, context (basic identity), metadata.
//   - inject hydration: above + context.inject (partial — uses-edge inject
//     resolved into per-consumer slots).
//   - inject mapping: above + context.inject (hydrated — including FR
//     injectMapping rewrites).
//   - source resolution: above + locals + source.
//   - values mapping: above (locals + source) — valuesMapping output is
//     in-flight, not yet on the context as values.
//   - workload synthesis: above; .workload in progress.
//   - artifact evaluation and provider render: above + workload.
//   - feature rendering: for component-scope FRs, the host context as artifact
//     evaluation shows it (each FR renders against an uncaptured copy); for
//     boundary-scope FRs, drop .context.* and surface .boundary.* (path,
//     components, internal/cross-boundary uses-edges, labels, annotations,
//     feature_attachments) plus the FR-attached BoundaryFeature's .infra.
//     .values is empty for boundary-scope FRs.
//
// nil ctx returns an empty map.
func PillarSnapshot(ctx *types.TemplateContext, pass Pass, keys redact.Keys) map[string]any {
	if ctx == nil {
		return map[string]any{}
	}
	out := map[string]any{}

	// Baseline: present at every pass.
	if ctx.Values.Len() > 0 {
		out["values"] = redact.Pillar(ctx.Values, keys.Values)
	}
	if ctx.Infra.Len() > 0 {
		out["infra"] = redact.Pillar(ctx.Infra, keys.Infra)
	}
	if ctx.Context.InstanceID != "" {
		out["context.instance_id"] = ctx.Context.InstanceID
	}
	if ctx.Metadata != nil {
		out["metadata"] = ctx.Metadata
	}

	// Provides evaluation is the baseline — return early.
	if pass == PassProvides || pass == PassPillarsInitial {
		return out
	}

	// Inject hydration onward: the inject pipe is populated.
	if pass == PassInject || isPhaseAOrLater(pass) {
		if deliversAnyKey(ctx.Context.Inject) {
			out["context.inject"] = keys.InjectPipe(ctx.Context.Inject)
		}
	}

	// Inject mapping and later: uses edges visible.
	if isPhaseAOrLater(pass) && ctx.Context.Uses.Len() > 0 {
		out["context.uses"] = keys.Uses(ctx.Context.Uses)
	}

	// Source resolution onward: locals are populated.
	if isPhaseB0OrLater(pass) && ctx.Locals.Len() > 0 {
		out["locals"] = redact.Locals(ctx.Locals, keys.Locals)
	}

	// Source resolution onward: .source is populated (after Recipe.Spec.Source
	// template eval).
	if isPhaseB0OrLater(pass) && ctx.Source != nil {
		out["source"] = redact.Source(ctx.Source, keys.Source)
	}

	// Artifact evaluation onward: .workload is populated. Workload synthesis
	// has it in progress; it is not surfaced until artifact evaluation, to
	// avoid showing an in-flight state.
	if isPhaseB3OrLater(pass) && ctx.Workload != nil {
		out["workload"] = ctx.Workload
		switch keys.Workload {
		case redact.WorkloadSubstituted:
			// The document on the context is the re-render, so the projection's
			// own work is the announcement. It rides a sibling entry rather than
			// wrapping the document, because a reader inspecting one field of a
			// rendered workload must find it at the same path either way.
			out["workload_note"] = redact.SubstitutedProjection()
		case redact.WorkloadUnavailable:
			// The whole document, replaced by a statement that says so. Keeping
			// the key with a visible statement is what distinguishes "withheld"
			// from "this Recipe declares no workload", which are different facts
			// and would otherwise display identically.
			out["workload"] = redact.WithheldProjection()
		}
	}

	// Feature rendering's boundary-scope branch: drop .context.*, surface
	// .boundary.*. The component-scope branch matches artifact evaluation
	// (handled above).
	if pass == PassPhaseB6 && ctx.Boundary != nil {
		// Boundary-scope: drop the per-component .context.* and surface
		// .boundary.*.
		delete(out, "context.instance_id")
		delete(out, "context.inject")
		delete(out, "context.uses")
		out["boundary"] = ctx.Boundary
	}

	return out
}

// deliversAnyKey reports whether a delivery pipe carries at least one key under
// any mechanism, a custom one included. Templates read a custom mechanism's keys
// through its own name and through the merged view, so a pipe carrying nothing
// but a custom mechanism still delivers, and its block is shown like any other.
// A mechanism present with no keys delivers nothing and does not count.
func deliversAnyKey(pipe types.InjectPipe) bool {
	if pipe.Env.Len() > 0 || pipe.File.Len() > 0 || pipe.Arg.Len() > 0 || pipe.All.Len() > 0 {
		return true
	}
	for _, deliveries := range pipe.Additional.All() {
		if deliveries.Len() > 0 {
			return true
		}
	}
	return false
}

// isPhaseAOrLater reports whether the given pass is at or after inject
// mapping.
func isPhaseAOrLater(p Pass) bool {
	switch p {
	case PassPhaseA, PassPhaseB0, PassPhaseB0Locals, PassPhaseB1, PassPhaseB2,
		PassPhaseB3, PassPhaseB4, PassPhaseB6, PassEmission, PassLabels:
		return true
	}
	return false
}

// isPhaseB0OrLater reports whether the given pass is at or after source
// resolution.
func isPhaseB0OrLater(p Pass) bool {
	switch p {
	case PassPhaseB0, PassPhaseB0Locals, PassPhaseB1, PassPhaseB2,
		PassPhaseB3, PassPhaseB4, PassPhaseB6, PassEmission, PassLabels:
		return true
	}
	return false
}

// isPhaseB3OrLater reports whether the given pass is at or after artifact
// evaluation.
func isPhaseB3OrLater(p Pass) bool {
	switch p {
	case PassPhaseB3, PassPhaseB4, PassPhaseB6, PassEmission, PassLabels:
		return true
	}
	return false
}
