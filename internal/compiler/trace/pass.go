// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package trace

// Pass names a snapshot slot in the per-node trace layout. Each Pass maps to
// one numbered file or directory under
// .trace/boundaries/<boundary>/<instance_id>/ (sibling to dist/).
//
// Some slots are written for every node, because the stages they capture run
// whatever shape the Recipe has; the rest appear only when the Recipe declares
// the input kind that stage reads. That makes the slot inventory itself
// informative — which slots a node produced shows which input kinds its Recipe
// actually exercised, independent of what the catalog entry claims it does.
//
// Adding a stage means adding its Pass here, adding the Write call at the
// stage that produces the state, and describing the new slot in the published
// build-command reference. The layout is a surface authors read directly, so a
// slot that appears with nothing describing it is output they cannot interpret.
type Pass string

// The pass identifiers, one per slot in the per-node layout.
const (
	// PassPillarsInitial captures the per-node values, infra, context (basic
	// identity), and metadata pillars before any sub-pass has run. Slot:
	// 01-pillars-initial.yaml.
	PassPillarsInitial Pass = "01-pillars-initial"

	// PassProvides captures the result of Pass 1 — every node's resolved
	// provides set. Slot: 02-pass1-provides.yaml.
	PassProvides Pass = "02-pass1-provides"

	// PassInject captures the post-Pass 2 inject pipe (uses-edge inject
	// resolved into per-consumer .context.inject.{env,file,arg,all}). Slot:
	// 03-pass2-inject.yaml.
	PassInject Pass = "03-pass2-inject"

	// PassPhaseA captures Phase A injectMapping output (per-consumer inject
	// pipe rewrites authored by attached FeatureRecipes' injectMapping
	// blocks). Slot: 04-phaseA-inject.yaml.
	PassPhaseA Pass = "04-phaseA-inject"

	// PassPhaseB0 captures the Phase B.0 source resolution (Recipe.Spec.Source
	// after Go-template evaluation). Slot: 05-phaseB-source.yaml.
	PassPhaseB0 Pass = "05-phaseB-source"

	// PassPhaseB0Locals captures Phase B.0's locals evaluation. Slot:
	// 06-phaseB-locals.yaml.
	PassPhaseB0Locals Pass = "06-phaseB-locals"

	// PassPhaseB1 captures Phase B.1 valuesMapping output. Slot:
	// 07-phaseB-valuesMapping.yaml.
	PassPhaseB1 Pass = "07-phaseB-valuesMapping"

	// PassPhaseB2 captures Phase B.2 parsed Workload. Slot:
	// 08-phaseB-workload.yaml.
	PassPhaseB2 Pass = "08-phaseB-workload"

	// PassPhaseB3 captures Phase B.3 artifacts (per-artifact). Files land
	// under 09-phaseB-artifacts/<filename>.yaml.
	PassPhaseB3 Pass = "09-phaseB-artifacts"

	// PassPhaseB4 captures Phase B.4 engine-synthesised manifests
	// (per-emitted-file). Files land under 10-engine-synthesis/<filename>.yaml.
	PassPhaseB4 Pass = "10-engine-synthesis"

	// PassPhaseB6 captures Phase B.6 FeatureRecipe Resource Generation
	// (per-attached FR; for boundary-scope FRs, deduplicated by
	// (boundary_path, FR-instance)).
	PassPhaseB6 Pass = "09b-phaseB6-boundary-fr"

	// PassEmission captures emission-time collision check results plus the
	// final per-node file list. Slot: 11-engine-emission.log.
	PassEmission Pass = "11-engine-emission"

	// PassLabels captures provenance label injection per emitted file.
	// Slot: 12-labels-injected.log.
	PassLabels Pass = "12-labels-injected"
)

// String returns the Pass enum's string representation (the slot name
// without the .yaml/.log extension).
func (p Pass) String() string {
	return string(p)
}
