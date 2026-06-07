// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/trace"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// RendererInput holds all data needed for Step 5 rendering.
type RendererInput struct {
	Graph          *types.IRGraph
	FeatureOrder   ir.FeatureOrder
	ResolveResult  *resolver.ResolveResult
	CatalogLookup  resolver.CatalogLookup
	ProviderLookup ProviderLookup // Optional: enables baseSchema validation in sourceless mode

	// MappingLibraries is the resolved MappingLibrary set (local catalog plus
	// lockfile-pinned libraries bridged in at load), keyed by bare name. It is
	// the source for resolving a Recipe / FeatureRecipe valuesMapping's bare-name
	// `$import: <Library>#/mappings/<entry>` at render. Passing the typed set
	// (not a catalog handle) keeps the renderer decoupled from pkg/catalog. nil
	// when no MappingLibraries are present.
	MappingLibraries map[string]types.MappingLibrary

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
}

// NodeProvides holds the resolved provides for all nodes (Pass 1 output).
// Key: nodeID, Value: provides dictionary (key → resolved value string).
type NodeProvides map[string]types.OrderedMap[string]

// ProvidesContext is the restricted context available during provides evaluation.
// Only self-contained data: instance_id, outputs, values, infra.
type ProvidesContext struct {
	Context ProvidesContextBlock     `json:"context"`
	Values  types.OrderedMap[any]    `json:"values"`
	Infra   types.OrderedMap[any]    `json:"infra"`
	Locals  types.OrderedMap[string] `json:"locals"`
}

// ProvidesContextBlock holds only the instance_id for provides evaluation.
type ProvidesContextBlock struct {
	InstanceID string `json:"instance_id"`
}
