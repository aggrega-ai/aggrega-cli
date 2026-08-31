// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"context"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// BuildInputs carries the explicit state BuildToBuffer needs. View is the
// request-scoped catalog lookup; every recipe, sub-aggregate, and
// mapping-library resolution the build performs comes from it, so the whole
// build resolves against one consistent pin snapshot. Resolved is the closure
// ResolveClosure produced for the same request. Lockfile, WorkspaceRoot, and
// WorkspaceConfig's resolved cache directory enable build-time source-artifact
// resolution: when Lockfile is non-nil and WorkspaceRoot is set, a remote
// Recipe (or FeatureRecipe) source resolves to its pinned, content-addressed
// cache entry; otherwise each source is forwarded verbatim.
//
// TolerateCatalogViewInconsistency opts the build out of the guard-layer halt
// for a type that resolved as a catalog reference yet is momentarily absent
// from the view (a drifted cache slot skipped on read). Set true for resilient
// inspection — the graph still builds with the entity skipped, and the
// condition surfaces as a diagnostic; left false, the divergence halts loudly.
type BuildInputs struct {
	View     *catalog.LazyLookup
	Resolved *ResolvedClosure
	// ReleasePath is the path of the Release being built. It locates the record of
	// the plugin identities trusted for this Release's verdict attachments, which
	// sits beside that Release. It is required, and BuildToBuffer refuses a build
	// without it rather than defaulting: the record's absence and its
	// unlocatability are different facts, and a default would report the second as
	// the first — a workspace that has recorded gates would build with none of them
	// held to their recorded identity, and nothing would say so.
	ReleasePath                      string
	WorkspaceConfig                  workspaceconfig.Config
	WorkspaceRoot                    string
	Lockfile                         *lockfile.Lockfile
	CompilerVersion                  string
	TolerateCatalogViewInconsistency bool
}

// BuildToBuffer runs the full compiler pipeline for the resolved Release and
// returns the result in memory — the IR graph and, unless opts.SkipRender is
// set, the rendered artifact buffers. It performs no filesystem writes; a
// caller that persists artifacts writes the returned buffers itself.
//
// The three lookups the pipeline needs — the Component/Feature catalog lookup,
// the Manifest-aware recipe lookup, and the sub-aggregate loader — all come
// from the request view, so no eager whole-catalog load is required and a
// broken out-of-closure entity never fails the build.
func BuildToBuffer(ctx context.Context, in BuildInputs, opts pipeline.Options) (*pipeline.Result, error) {
	view := in.View
	r := in.Resolved
	catLookup := view.AsCatalogLookup()

	// Reconstruct provider resolution from the LOCKFILE, never the workspace
	// config, so rendered output is invariant to a plugins.providers change made
	// after the pull (adopted only on the next pull). Provider resolution reads the
	// pinned bindings — a federated catalog with more than one plugin serving a
	// capability is disambiguated by the binding, so an ambiguous target resolves
	// rather than halting. catLookup is a value struct, so the rewrite touches only
	// this call's pipeline inputs and never the shared view.
	if in.Lockfile != nil {
		bindings := lockfile.ProviderBindings(in.Lockfile)
		catLookup.ProviderPlugin = func(ref string) (types.Plugin, bool, error) {
			return view.ProviderPluginForTarget(ref, bindings)
		}
	}

	// The Aggregate's source path is rendered into the workspace coordinate
	// system HERE rather than at each caller, because it is stamped onto every
	// root-tier IR node as its SourceFile and the IR wire shape is required to be
	// byte-identical across machines whose workspaces live at different absolute
	// paths. A caller forwarding the resolved path verbatim would stamp absolute
	// provenance, which is machine-specific by construction; converting at the one
	// site every render passes through is what makes that unrepresentable. The
	// helper returns an already-relative path unchanged, so a caller that had
	// converted already is unaffected.
	inputs := pipeline.Inputs{
		Release:                          r.Release,
		Manifest:                         r.Manifest,
		AggregateRawBytes:                r.AggBytes,
		AggregatePath:                    catalog.RelativeToWorkspace(in.WorkspaceRoot, r.AggregatePath),
		WorkspaceConfig:                  in.WorkspaceConfig,
		Lockfile:                         in.Lockfile,
		WorkspaceRoot:                    in.WorkspaceRoot,
		MappingLibraries:                 view.MappingLibraries(),
		WorkloadLibraries:                view.WorkloadLibraries(),
		ArtifactsLibraries:               view.ArtifactsLibraries(),
		CompilerVersion:                  in.CompilerVersion,
		TolerateCatalogViewInconsistency: in.TolerateCatalogViewInconsistency,
	}
	// The governance attachment list and the digests recorded as trusted for the
	// verdict attachments arrive through the one composition every build surface
	// calls — this one serves both the daemon and the editor's build tool, and the
	// command-line build calls the same helper. Composing them here rather than at
	// each surface is what keeps a surface from acquiring one without the other.
	if err := ApplyGovernanceComposition(&inputs, view, in.ReleasePath); err != nil {
		return nil, err
	}

	return pipeline.RunPipeline(
		ctx,
		inputs,
		catLookup,
		view.RecipeLookupWithManifest(r.Manifest),
		view.SubAggregateLoader(),
		opts,
	)
}
