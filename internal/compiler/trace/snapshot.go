// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

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
// the consumer's display even when the consumer declares nothing.
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
//   - Pass 1 (provides): values, infra, context (basic identity), metadata.
//   - Pass 2 (inject): above + context.inject (partial — uses-edge inject
//     resolved into per-consumer slots).
//   - Phase A: above + context.inject (hydrated — including FR injectMapping
//     rewrites).
//   - Phase B.0: above + locals + source.
//   - Phase B.1: above (locals + source) — valuesMapping output is in-flight,
//     not yet on the context as values.
//   - Phase B.2: above; .workload in progress.
//   - Phase B.3-B.5: above + workload.
//   - Phase B.6: for component-scope FRs, same as Phase B.3 plus the
//     FR-scoped values/infra/locals overlay; for boundary-scope FRs,
//     drop .context.* and surface .boundary.* (path, components,
//     internal/cross-boundary uses-edges, labels, annotations,
//     feature_attachments) plus the FR-attached BoundaryFeature's
//     .infra. .values is empty for boundary-scope FRs.
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

	// Pass 1 (provides) is the baseline — return early.
	if pass == PassProvides || pass == PassPillarsInitial {
		return out
	}

	// Pass 2 onward: the inject pipe is populated.
	if pass == PassInject || isPhaseAOrLater(pass) {
		if ctx.Context.Inject.Env.Len() > 0 || ctx.Context.Inject.File.Len() > 0 || ctx.Context.Inject.Arg.Len() > 0 {
			out["context.inject"] = keys.InjectPipe(ctx.Context.Inject)
		}
	}

	// Phase A and later: uses edges visible.
	if isPhaseAOrLater(pass) && ctx.Context.Uses.Len() > 0 {
		out["context.uses"] = redact.Uses(ctx.Context.Uses, keys.UsesProvides)
	}

	// Phase B.0+: locals are populated.
	if isPhaseB0OrLater(pass) && ctx.Locals.Len() > 0 {
		out["locals"] = redact.Locals(ctx.Locals, keys.Locals)
	}

	// Phase B.0+: .source is populated (after Recipe.Spec.Source template eval).
	if isPhaseB0OrLater(pass) && ctx.Source != nil {
		out["source"] = redact.Source(ctx.Source, keys.Source)
	}

	// Phase B.3-B.5+: .workload is populated. Phase B.2 has it in progress;
	// not surfaced until B.3 to avoid showing an in-flight state.
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

	// Phase B.6 boundary-scope branch: drop .context.*, surface .boundary.*.
	// The component-scope branch matches Phase B.3 (handled above).
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

// isPhaseAOrLater reports whether the given pass is at or after Phase A.
func isPhaseAOrLater(p Pass) bool {
	switch p {
	case PassPhaseA, PassPhaseB0, PassPhaseB0Locals, PassPhaseB1, PassPhaseB2,
		PassPhaseB3, PassPhaseB4, PassPhaseB6, PassEmission, PassLabels:
		return true
	}
	return false
}

// isPhaseB0OrLater reports whether the given pass is at or after Phase B.0.
func isPhaseB0OrLater(p Pass) bool {
	switch p {
	case PassPhaseB0, PassPhaseB0Locals, PassPhaseB1, PassPhaseB2,
		PassPhaseB3, PassPhaseB4, PassPhaseB6, PassEmission, PassLabels:
		return true
	}
	return false
}

// isPhaseB3OrLater reports whether the given pass is at or after Phase B.3.
func isPhaseB3OrLater(p Pass) bool {
	switch p {
	case PassPhaseB3, PassPhaseB4, PassPhaseB6, PassEmission, PassLabels:
		return true
	}
	return false
}
