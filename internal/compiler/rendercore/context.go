// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

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
	// Attachments is the node's post-cascade attachment set, keyed by the
	// instance name the author gave each one, exactly as the IR graph carries
	// it. It is the render surface's one source for an attachment's declared
	// facts: the per-node projection handed to a provider guest carries these
	// entries verbatim, including the derived sensitivity marking, so a guest
	// reads them without a host round-trip and the host derives nothing a
	// second time. Empty for a node with no attachments.
	Attachments types.OrderedMap[types.IRFeatureEntry]
	// DeclaredSensitivity carries the sensitivity declarations this node's render
	// joins against to name a rendered key. It is read only by the node
	// projection's own construction, which turns it into the per-node marking a
	// provider guest receives; nothing else consults it, and nothing derived from
	// it reaches an emitted byte. The zero value declares nothing and therefore
	// marks nothing, which is the safe direction here — see DeclaredSensitivity.
	DeclaredSensitivity DeclaredSensitivity
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
	// (recipe.spec.capability with any version pin stripped). When set it is the
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
	// OutputLabels are the recipe-author-owned labels stamped onto every object
	// this node's rendered output produces: the pairs written literally on the
	// recipe (recipe.spec.outputLabels) together with the ones it promotes from
	// this node's component labels by key (recipe.spec.outputComponentLabels),
	// already resolved to their values. They ride the same rail as the fixed
	// provenance labels: merged into the set [BuildAggregaProvenanceLabels]
	// returns. Load-time validation guarantees the keys are disjoint from the
	// compiler-owned provenance keyspace (no aggrega.ai/ or app.kubernetes.io/
	// key), so the merge never collides.
	//
	// The map belongs to this node alone. Two nodes bound to one recipe hold
	// separate maps, because a promoted value is this node's own component
	// identity and must not reach a neighbour's output.
	OutputLabels map[string]string
	Provenance   ProvenanceContext
	// YAMLIndent is the indentation width for artifacts rendered from this
	// node (clamped to [2,9], default 2). Forwarded from RendererInput.
	YAMLIndent int
	// WarningSink, when non-nil, receives the non-fatal advisories a provider
	// engine raises while rendering this node — the warn- and info-severity
	// diagnostics the guest returns in its render output, plus any guest-emitted
	// warnings. The orchestrator wires it to the build result's warnings stream;
	// engines nil-check it before calling. Warnings never affect emitted bytes,
	// so byte-identity of rendered files is independent of whether a sink is
	// attached. Host-only: it is not part of any wire projection sent to a guest.
	WarningSink func(string)
	// UnsupportedFieldSink, when non-nil, receives the unsupported-field advisories a
	// provider engine raises while rendering this node — the structured
	// warn-severity diagnostics reporting an authored field the target could
	// not honor. It is routed separately from WarningSink so a display surface can
	// suppress the unsupported-field class alone by its stable code while other render
	// warnings still surface, and so the structured diagnostics stay
	// machine-matchable downstream. Like WarningSink it never affects emitted
	// bytes and is host-only: not part of any wire projection sent to a guest.
	UnsupportedFieldSink func(renderctx.Diagnostic)
	// DerivedKeySink, when non-nil, receives the names a provider engine's guest
	// declares it derived while rendering this node, keyed by the guest's own key
	// space. A provider engine forwards whatever the guest declared, verbatim: the
	// host reads a space name and a key only as strings to compare for equality,
	// and derives no key of its own. The orchestrator wires it to the per-node
	// accumulator whose contents are checked for duplicates once every contributor
	// to an assembled output has rendered. A node may render across more than one
	// crossing — a source render, a workload synthesis, an artifacts placement — so
	// the sink is called once per crossing and the accumulator merges them.
	//
	// Like WarningSink it never affects emitted bytes: a duplicate halts the build
	// and no declaration changes what is written, so rendered output is
	// byte-identical whether or not a sink is attached. Host-only: not part of any
	// wire projection sent to a guest.
	DerivedKeySink func(types.OrderedMap[[]string])
}

// ProvenanceContext carries build identity for resource labels.
type ProvenanceContext struct {
	CompilerVersion  string
	AggregateName    string
	AggregateVersion string
	ReleaseName      string
	Environment      string
}
