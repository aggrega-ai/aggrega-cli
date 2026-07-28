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
// Three roots in the table below are displayed verbatim, and for two distinct
// reasons. .workload is a rendered document whose fields carry no link back to
// the declaration they came from, so a datum interpolated into it is no longer
// identifiable by key at all. .context.uses and .boundary are CROSS-NODE
// projections — a uses edge carries its target's resolved provides, and the
// boundary view carries the same for every edge in its subtree — and the set
// passed here describes one node's own declarations, so it does not reach
// another node's.
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
			out["context.inject"] = redact.InjectPipe(ctx.Context.Inject, keys.Inject)
		}
	}

	// Phase A and later: uses edges visible.
	if isPhaseAOrLater(pass) && ctx.Context.Uses.Len() > 0 {
		out["context.uses"] = ctx.Context.Uses
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
