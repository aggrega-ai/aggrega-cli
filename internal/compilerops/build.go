// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	"context"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
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
	View                             *catalog.LazyLookup
	Resolved                         *ResolvedClosure
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
	return pipeline.RunPipeline(
		ctx,
		pipeline.Inputs{
			Release:                          r.Release,
			Manifest:                         r.Manifest,
			AggregateRawBytes:                r.AggBytes,
			AggregatePath:                    r.AggregatePath,
			WorkspaceConfig:                  in.WorkspaceConfig,
			Lockfile:                         in.Lockfile,
			WorkspaceRoot:                    in.WorkspaceRoot,
			MappingLibraries:                 view.MappingLibraries(),
			CompilerVersion:                  in.CompilerVersion,
			TolerateCatalogViewInconsistency: in.TolerateCatalogViewInconsistency,
		},
		view.AsCatalogLookup(),
		view.RecipeLookupWithManifest(r.Manifest),
		view.SubAggregateLoader(),
		opts,
	)
}
