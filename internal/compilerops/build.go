// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compilerops

import (
	"context"
	"io"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
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
	//
	// It is also handed to the pipeline as the Release's own path, which names the
	// Release's file in every finding about what the Release wrote and in the
	// source an annotation written there is traced to in the graph.
	ReleasePath                      string
	WorkspaceConfig                  workspaceconfig.Config
	WorkspaceRoot                    string
	Lockfile                         *lockfile.Lockfile
	CompilerVersion                  string
	TolerateCatalogViewInconsistency bool

	// composedTrust is the record of plugin identities beside the lockfile as the
	// caller composed it and has not yet written it, or nil, which reads the record
	// from disk. The preview hands on the one it was supplied, so a plugin attached at
	// a verdict role is held at its load to the signature this run records, as the
	// check of recorded signatures before the render held it.
	composedTrust *pluginstrust.Trust

	// resolutionAdvisoriesShown is handed to the pipeline as
	// pipeline.Inputs.ResolutionAdvisoriesShown: the preview that builds this set
	// it where its caller's operation has already shown the resolution's
	// advisories, so the build's resolution records them without showing them.
	resolutionAdvisoriesShown bool

	// display is handed to the pipeline as pipeline.Inputs.Display: where the
	// run displays what it raises as it raises it. The preview that builds this
	// sets it from its own inputs (WithDisplay); nil shows nothing.
	display io.Writer
}

// BuildToBuffer runs the full compiler pipeline for the resolved Release and
// returns the run's outcome in memory — the IR graph and, unless opts.SkipRender
// is set, the rendered artifact buffers. It performs no filesystem writes; a
// caller that persists artifacts writes the returned buffers itself.
//
// The outcome is the pipeline's own, handed on unchanged, so a run that stopped
// arrives here carrying the advisories the phases ahead of the halt raised and a
// caller reads them through the same accessors whichever surface it is on.
//
// The three lookups the pipeline needs — the Component/Feature catalog lookup,
// the Manifest-aware recipe lookup, and the sub-aggregate loader — all come
// from the request view, so no eager whole-catalog load is required and a
// broken out-of-closure entity never fails the build. The catalog lookup is
// scoped to the version each instance compiles at
// (ResolvedClosure.InstanceVersionLookup), and an instance whose constraints
// admit no version halts the build before the pipeline runs.
func BuildToBuffer(ctx context.Context, in BuildInputs, opts pipeline.Options) (pipeline.Outcome, error) {
	view := in.View
	r := in.Resolved
	// Each library is read at the version this Release's closure resolved, where
	// the closure was resolved with its library axes (ResolvedClosure.Libraries).
	libraries := r.Libraries(view)
	catLookup := view.AsCatalogLookupWithSchemas(ctx, libraries.Schemas)

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
		Release:           r.Release,
		Manifest:          r.Manifest,
		AggregateRawBytes: r.AggBytes,
		AggregatePath:     catalog.RelativeToWorkspace(in.WorkspaceRoot, r.AggregatePath),
		// The path as this core holds it. The pipeline renders it into the same
		// workspace coordinate system against WorkspaceRoot below, which is the one
		// place the Release's path is rendered whichever surface ran the build.
		ReleasePath: in.ReleasePath,
		// A pointer into this call's own inputs value, so the composition below
		// re-points it at the copy it rewrites and the caller's configuration is
		// never written through.
		WorkspaceConfig: &in.WorkspaceConfig,
		Lockfile:        in.Lockfile,
		// A build consumes a recorded closure, so the recorded identities govern
		// what its plugins resolve to. That stays true of a build whose record
		// pins no plugin yet: the claim is that pins govern here, not that any
		// exist, which is what keeps an unrecorded guest worth reporting on this
		// path.
		PluginPins:                       pluginpath.RecordedPins(lockfile.PluginPins(in.Lockfile, in.ReleasePath)),
		WorkspaceRoot:                    in.WorkspaceRoot,
		MappingLibraries:                 libraries.Mappings,
		WorkloadLibraries:                libraries.Workloads,
		ArtifactLibraries:                libraries.Artifacts,
		RecordedPins:                     view.RecordedPins(),
		CompilerVersion:                  in.CompilerVersion,
		TolerateCatalogViewInconsistency: in.TolerateCatalogViewInconsistency,
		ResolutionAdvisoriesShown:        in.resolutionAdvisoriesShown,
		Display:                          in.display,
		// The closure resolution that produced Resolved read every closure entity
		// through this view, so its advisories over them are complete here.
		CatalogAdvisories: view.SchemaAdvisories,
	}
	// The governance attachment list and the digests recorded as trusted for the
	// verdict attachments arrive through the one composition every build surface
	// calls — this one serves both the daemon and the editor's build tool, and the
	// command-line build calls the same helper. Composing them here rather than at
	// each surface is what keeps a surface from acquiring one without the other.
	if err := applyGovernanceComposition(&inputs, view, in.ReleasePath, in.composedTrust); err != nil {
		// A composition failure happens before the pipeline runs, so there is no
		// run to report findings from and the zero outcome is the truthful answer.
		return pipeline.Outcome{}, err
	}

	// Every instance is compiled at the version its constraints admit, through the
	// one settlement a validation of the same Release reads too. A conflict between
	// them halts here, before the pipeline runs, for the reason a composition
	// failure does.
	catLookup, err := r.InstanceVersionLookup(catLookup, view)
	if err != nil {
		return pipeline.Outcome{}, err
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
