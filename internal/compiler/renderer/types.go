// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/trace"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// RendererInput holds all data needed for Step 5 rendering.
type RendererInput struct {
	Graph         *types.IRGraph
	FeatureOrder  ir.FeatureOrder
	ResolveResult *resolver.ResolveResult
	CatalogLookup resolver.CatalogLookup

	// MappingLibraries is the resolved MappingLibrary set (local catalog plus
	// lockfile-pinned libraries bridged in at load), keyed by bare name. It is
	// the source for resolving a Recipe / FeatureRecipe valuesMapping's bare-name
	// `$import: <Library>#/mappings/<entry>` at render. Passing the typed set
	// (not a catalog handle) keeps the renderer decoupled from pkg/catalog. nil
	// when no MappingLibraries are present.
	MappingLibraries map[string]types.MappingLibrary

	// WorkloadLibraries is the local-catalog WorkloadLibrary set, keyed by bare
	// name. It is the source for resolving a Recipe workload's bare-name
	// `$import: <Library>#/workloads/<entry>` at render. The set is sourced from
	// the local catalog only; a remote workload import is not bridged. nil when
	// no WorkloadLibraries are present.
	WorkloadLibraries map[string]types.WorkloadLibrary

	// ArtifactsLibraries is the local-catalog ArtifactsLibrary set, keyed by bare
	// name. It is the source for resolving a Recipe / FeatureRecipe artifacts
	// entry's bare-name `artifacts.<name>: { $import: <Library>#/artifacts/<entry> }`
	// at render. nil when no ArtifactsLibraries are present.
	ArtifactsLibraries map[string]types.ArtifactsLibrary

	// Lockfile, WorkspaceRoot, and CacheDir together enable orchestrator-
	// side source-artifact resolution. When all three are set, the
	// orchestrator looks up each Recipe's spec.source against
	// Lockfile.SourceArtifacts, verifies the cached archive's full
	// digest, and populates NodeRenderContext.Source with the extracted
	// path — so a remote source resolves to its cache entry and renders.
	// When any of the three is unset, the orchestrator forwards
	// recipe.Spec.Source verbatim: the sourceless, local-path, and
	// no-lockfile code paths. The CLI build and the serve daemon both
	// populate all three — from the loaded lockfile, the workspace root,
	// and the resolved cache directory.
	Lockfile      *lockfile.Lockfile
	WorkspaceRoot string
	CacheDir      string

	// Trace, when non-nil, captures per-pass intermediate state under
	// .trace/ (sibling to dist/) for the --trace debug surface. nil
	// disables tracing (the orchestrator passes a no-op writer when this
	// field is unset).
	Trace trace.Writer

	// YAMLIndent is the indentation width for every rendered YAML artifact.
	// The orchestrator forwards it onto each NodeRenderContext and
	// BoundaryInfo so the marshal sites apply one configured width. Clamped
	// to [2,9] (default 2) at the marshal call; the zero value renders at 2.
	YAMLIndent int

	// PluginPathLookup resolves a pipeline step's plugin URI to its verified
	// .wasm bytes, binding the per-node pipeline executor to the shared
	// plugin-resolution cascade (embed → colocated → local → cache → registry,
	// digest-pin-verified). nil disables pipeline execution: each node's
	// pipeline is validated but not run, and the renderer surfaces a
	// validate-only warning — the non-regression path for callers that do not
	// wire plugin loading.
	PluginPathLookup pluginpath.Lookup

	// ProviderVersion, when non-nil, reports the resolved provider-rendering
	// plugin version behind each node's provider binding — the value of the
	// aggrega.ai/provider-version label and the ordering key for boundary-
	// assembler selection across a mixed-version dispatch group. nil reports
	// every binding as version-less: nodes render through the in-tree engine and
	// carry no provider-version label. The build wiring sets it from the same
	// hybrid dispatch that resolves the rendering engine, so the reported version
	// and the selected engine agree.
	ProviderVersion ProviderVersionResolver

	// NodeProgress, when non-nil, is invoked once per node as the per-node render
	// loop completes each node: done is the 1-based count of nodes rendered so
	// far, total is the graph's node count, and nodeID names the node just
	// finished. It is a diagnostic observation point for a progress surface and
	// must not write to stdout — the rendered file map stays byte-deterministic
	// regardless of whether the hook is set. nil (the common case) skips the
	// call. Mirrors the Trace writer: an opt-in per-node hook, no-op when unset.
	NodeProgress func(done, total int, nodeID string)
}

// NodeProvides holds the resolved provides for all nodes (Pass 1 output).
// Key: nodeID, Value: provides dictionary (key → resolved value string).
type NodeProvides map[string]types.OrderedMap[string]

// ProvidesContext is the restricted context available during provides evaluation.
// Only self-contained data: instance_id, outputs, values, infra.
type ProvidesContext struct {
	Context ProvidesContextBlock  `json:"context"`
	Values  types.OrderedMap[any] `json:"values"`
	Infra   types.OrderedMap[any] `json:"infra"`
	// Locals carries EVALUATED locals, and is populated only on the attachment
	// FeatureRecipe path, which resolves that entity's own locals against this
	// same scope before evaluating its provides against it — so an attachment
	// FeatureRecipe's provides may read .locals and receives resolved values.
	// Do not remove this field as unused: a base Recipe's provides pass leaves
	// it zero because a base Recipe's locals are resolved after provides, and
	// the base pass's self-containment check declares .locals a narrowing of
	// this type for exactly that reason.
	Locals types.OrderedMap[string] `json:"locals"`
	// AttachmentName is the attachment instance name, populated for every
	// attachment FeatureRecipe's provides evaluation so its provides templates
	// may name the instance they are resolving for. Empty for a base Recipe's
	// provides, whose self-containment check declares this root a narrowing of
	// this type (a base provides referencing the attachment name still halts).
	//
	// The template key mirrors the field name: it is the published spelling
	// catalog FeatureRecipes read in their `provides` templates, and the two
	// scopes that expose the attachment name — this provides scope and the
	// render scope — publish it under the same key so a template author reads
	// one name at both.
	AttachmentName string `json:"attachment_name,omitempty"`
}

// ProvidesContextBlock holds only the instance_id for provides evaluation.
type ProvidesContextBlock struct {
	InstanceID string `json:"instance_id"`
}

// PathTemplateContext is the render context a Release boundary's `directory:` and
// `subDirectory:` templates are evaluated against. It is the single home of the
// variable set those templates may read: the names come from these json tags, and
// each surface states only which of them it refuses.
//
// No field carries omitempty, and that is load-bearing rather than an oversight.
// The surface renders under missingkey=error, so an omitted key halts naming the
// key that vanished instead of the reference the author actually wrote. Capability
// and RecipeName are legitimately empty when a `directory:` template is evaluated
// for a whole boundary — there is no single component and so no bound recipe or
// provider — and an empty string is what those templates render today. Dropping
// the keys would turn a rendered empty segment into a halt.
type PathTemplateContext struct {
	// Capability is the per-component provider name the component's recipe binds
	// to. Empty during the boundary-level `directory:` walk, which runs once per
	// boundary rather than once per component: per-provider segregation is the
	// `subDirectory:` concern.
	Capability string `json:"capability"`
	// BoundaryName is the boundary path the template is evaluated for.
	BoundaryName string `json:"boundary_name"`
	// Parameters is the Release-global parameter map. It stays an open map because
	// parameters are Release-authored: there is no declared key set to derive one
	// from, and under missingkey=error an unknown parameter key already halts.
	Parameters map[string]any `json:"parameters"`
	// ComponentID names the component being placed. Meaningful only on the
	// `subDirectory:` surface; the `directory:` surface refuses the reference
	// because a boundary directory is evaluated once for the boundary.
	ComponentID string               `json:"component_id"`
	Manifest    PathTemplateManifest `json:"manifest"`
	// ParentBoundary carries the immediate parent boundary's evaluated directory,
	// name, parameters and cascaded annotations. It stays an open map so that a
	// root-level boundary — which has no parent and therefore contributes an empty
	// map — halts on the KEY the template reached for, naming .directory rather
	// than naming the parent pillar itself.
	ParentBoundary map[string]any `json:"parent_boundary"`
}

// PathTemplateManifest is the Manifest-derived surface a path template may read.
type PathTemplateManifest struct {
	// RecipeName is the bound recipe's name for the component being placed. Empty
	// during the boundary-level `directory:` walk, where no component is bound.
	RecipeName string `json:"recipe_name"`
}
