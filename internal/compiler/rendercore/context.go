// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

// IREdgeInfo is the per-edge view passed to a BoundaryAssembler. Decoupled
// from types.IREdge so the package's BoundaryAssembler input does not depend
// on pkg/types directly.
type IREdgeInfo struct {
	Source string
	Target string
}

// NodeRenderContext provides identity metadata for manifest generation.
type NodeRenderContext struct {
	NodeID       string
	Annotations  map[string]string // merged release annotations
	BoundaryPath string
	SyncWave     int // deployment wave from the containing boundary's syncWave (signed integer; default 0)
	// Source is what engines actually consume (via loader.Load or
	// equivalent): the resolved filesystem path for remote sources
	// (extracted directory under the source-artifact cache slot),
	// the verbatim path for local sources (Tier-1 filesystem paths,
	// Tier-2 workspace-relative paths), or nil for sourceless
	// Recipes. The orchestrator populates this before dispatch;
	// engines that receive a non-string value here treat it as an
	// orchestrator bug rather than authoring drift.
	Source any
	// OriginalSource carries the verbatim Recipe spec.source as
	// authored, regardless of whether Source was resolved through
	// the cache. Engines reference OriginalSource for human-readable
	// diagnostics ("chart load failed for oci://...") so error
	// messages match the operator's source code rather than the
	// opaque cache path. Engines NEVER call loader.Load on
	// OriginalSource — Source is the load-bearing path; the helper
	// sourceForDiagnostic centralises the diagnostic formatting.
	OriginalSource any
	RecipeName     string // recipe metadata.name; empty when no Recipe selected for this node
	RecipeVersion  string // recipe metadata.version; pairs with RecipeName to form the aggrega.ai/recipe-uri label
	// ProviderName is the bare provider engine kind rendering this node
	// (recipe.spec.provider with any version pin stripped). When set it is the
	// value of the aggrega.ai/provider label.
	ProviderName string
	// ProviderVersion is the resolved provider-rendering plugin version behind
	// ProviderName, or empty when the node renders through the in-tree engine
	// with no resolved plugin version. When set it is the value of the
	// aggrega.ai/provider-version label; empty omits that label.
	ProviderVersion string
	// LabelComponentID, when non-empty, overrides NodeID as the value of the
	// app.kubernetes.io/name and aggrega.ai/component-id labels. Used by FR-
	// emitted resources whose internal NodeID carries marker syntax
	// (e.g. <boundary-fr>:bridge@cluster) that needs sanitization for K8s
	// label-value rules. Empty means "use NodeID directly" — the default for
	// regular component nodes whose NodeIDs are already alphanumeric.
	LabelComponentID string
	Provenance       ProvenanceContext
	// YAMLIndent is the indentation width for artifacts rendered from this
	// node (clamped to [2,9], default 2). Forwarded from RendererInput.
	YAMLIndent int
	// WarningSink, when non-nil, receives the non-fatal advisories a provider
	// engine raises while rendering this node — the warning- and info-severity
	// diagnostics the guest returns in its render output, plus any guest-emitted
	// warnings. The orchestrator wires it to the build result's warnings stream;
	// engines nil-check it before calling. Warnings never affect emitted bytes,
	// so byte-identity of rendered files is independent of whether a sink is
	// attached. Host-only: it is not part of any wire projection sent to a guest.
	WarningSink func(string)
}

// ProvenanceContext carries build identity for resource labels.
type ProvenanceContext struct {
	CompilerVersion  string
	AggregateName    string
	AggregateVersion string
	ReleaseName      string
	Environment      string
}
