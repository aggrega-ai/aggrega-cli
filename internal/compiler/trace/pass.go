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

	// PassProvides captures the result of provides evaluation — every node's
	// resolved provides set. Slot: 02-provides-evaluation.yaml.
	PassProvides Pass = "02-provides-evaluation"

	// PassInject captures the inject pipe after inject hydration (uses-edge
	// inject resolved into per-consumer .context.inject.{env,file,arg,all}).
	// Slot: 03-inject-hydration.yaml.
	PassInject Pass = "03-inject-hydration"

	// PassPhaseA captures inject mapping output (per-consumer inject pipe
	// rewrites authored by attached FeatureRecipes' injectMapping blocks).
	// Slot: 04-inject-mapping.yaml.
	PassPhaseA Pass = "04-inject-mapping"

	// PassPhaseB0 captures source resolution (Recipe.Spec.Source after
	// Go-template evaluation). Slot: 05-source-resolution.yaml.
	PassPhaseB0 Pass = "05-source-resolution"

	// PassPhaseB0Locals captures the locals evaluation source resolution
	// performs before it evaluates the source template. Slot:
	// 06-source-locals.yaml.
	PassPhaseB0Locals Pass = "06-source-locals"

	// PassPhaseB1 captures values mapping output. Slot:
	// 07-values-mapping.yaml.
	PassPhaseB1 Pass = "07-values-mapping"

	// PassPhaseB2 captures the Workload parsed by workload synthesis. Slot:
	// 08-workload-synthesis.yaml.
	PassPhaseB2 Pass = "08-workload-synthesis"

	// PassPhaseB3 captures artifact evaluation (per-artifact). Files land
	// under 09-artifact-evaluation/<filename>.yaml.
	PassPhaseB3 Pass = "09-artifact-evaluation"

	// PassPhaseB4 captures the manifests the provider render synthesized
	// (per-emitted-file). Files land under 10-engine-synthesis/<filename>.yaml.
	PassPhaseB4 Pass = "10-engine-synthesis"

	// PassPhaseB6 captures feature rendering — FeatureRecipe Resource
	// Generation (per-attached FR; for boundary-scope FRs, deduplicated by
	// (boundary_path, FR-instance)). Slot:
	// 09b-feature-rendering-boundary.yaml.
	PassPhaseB6 Pass = "09b-feature-rendering-boundary"

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
