// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

const (
	// flagClosure is the name of the flag that selects a Release's dependency
	// closure. It is a constant because the flag registration, the help text and
	// the refusals a non-Release subject produces all name it, and a flag spelled
	// in four places is spelled differently in one of them eventually.
	flagClosure = "closure"

	// helpClosure is the flag's one-line help, shared by the registration and by
	// the generated command reference.
	helpClosure = "Report the Release's dependency closure — every catalog entity it reaches, at the version each " +
		"resolved to — in place of the resolved-node view. Applies to a Release, which is the only kind that " +
		"resolves a closure; any other kind refuses the request rather than silently dropping it"
)

// renderReleaseClosure resolves the dependency closure of the Release at
// releasePath and writes the listing.
//
// It answers a different question from the resolved-node view this command
// renders by default. The closure says what was pulled in to build the
// deployment, and at which resolved versions; the node view says what the
// deployment looks like. Neither is recoverable from the other: an entity can be
// reached without appearing as a node, and a node does not carry the version its
// type resolved to.
//
// The resolution is the shared one every surface that reports a closure runs, so
// what this prints and what a session or a tool call reports for the same Release
// come from one computation rather than from three that agree today. Nothing is
// rendered here beyond the listing: no provider guest is loaded and no artifact is
// produced.
//
// A record of pinned versions is read where the workspace has one and the view is
// seeded with it, so the versions reported are the ones this deployment resolves
// to rather than whatever the catalog happens to hold today. A workspace with no
// such record resolves over the catalog as it stands.
func renderReleaseClosure(rw *workspaceconfig.ResolvedWorkspace, releasePath string, catalogDirs []string, opts inspectOptions) error {
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}
	wsRoot := rw.Root()
	wsCfg := rw.Config()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	dirs := workspaceconfig.ResolveCatalogDirs(catalogDirs, wsCfg, wsRoot)

	view := catalog.NewLazyLookup(wsRoot, cacheDir, dirs, nil)
	lf, _, lfErr := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if lfErr == nil {
		view.SetPins(catalog.PinsFromLockfile(lf))
	} else {
		lf = nil
	}

	resolved, cerr := compilerops.ResolveClosure(compilerops.ClosureInputs{
		View:            view,
		ReleasePath:     releasePath,
		WorkspaceRoot:   wsRoot,
		WorkspaceConfig: wsCfg,
		Lockfile:        lf,
		Augment:         true,
	})
	if cerr != nil {
		// A file the catalog read refused makes its entity unresolvable, and the
		// failure on its own can only name the reference, never the file behind it.
		// Replaying the refusals the view recorded turns that into the constraint
		// the author has to fix.
		return withSkipContext(cerr, view.SkippedEntities())
	}

	renderClosureListing(opts, releasePath, resolved)
	return nil
}

// renderClosureListing writes the closure a Release resolved, at the verbosity
// the level flags selected.
//
// The three levels carry the same meaning here they carry for every other kind:
// identifiers only, a structured overview, and every field the answer holds.
//
// The entity order is the resolution's own and is not re-sorted. It is already a
// function of the Release's content rather than of anything ambient, so imposing
// a second order here would be a second statement of the same thing, free to
// disagree with the first.
func renderClosureListing(opts inspectOptions, releasePath string, resolved *compilerops.ResolvedClosure) {
	if opts.level == levelList {
		for _, e := range resolved.Closure.Entities {
			fmt.Fprintf(opts.out, "%s\n", e.URI)
		}
		return
	}

	renderHeader(opts, "Release", identifierVersion(resolved.Release.Metadata.Name, resolved.Release.Metadata.Version))
	fmt.Fprintf(opts.out, "  Aggregate: %s\n", resolved.Aggregate.Metadata.Name)
	fmt.Fprintf(opts.out, "  Manifest: %s\n", resolved.Manifest.Metadata.Name)
	if env := resolved.Release.Metadata.Environment; env != "" {
		fmt.Fprintf(opts.out, "  Environment: %s\n", env)
	}

	fmt.Fprintf(opts.out, "\nEntities (%d):\n", len(resolved.Closure.Entities))
	if len(resolved.Closure.Entities) == 0 {
		fmt.Fprintln(opts.out, "  (none)")
	}
	for _, e := range resolved.Closure.Entities {
		line := closureLine(opts, e.URI, e.ResolvedVersion)
		// The kind is carried at the fullest level only, and only where the
		// reference site recorded one. Some positions in a document say what kind
		// they point at and others do not, so it is reported where it is known and
		// omitted where it is not, rather than guessed from the shape of the name.
		if opts.level == levelDetail {
			if kind := compilerops.EntityKindForHint(e.KindHint); kind != "" {
				line += "  [" + kind + "]"
			}
		}
		fmt.Fprintf(opts.out, "  %s\n", line)
	}

	renderClosureLibraries(opts, resolved.Closure)

	// The mapping findings follow the closure they were found in rather than
	// preceding it, so the listing the caller asked for arrives first and is not
	// mistaken for an error report. This command reports on a Release rather than
	// producing anything from it, so a fault is described beside the closure
	// rather than replacing it with a refusal — the same reading every other
	// surface that reports a closure applies to the same findings.
	if len(resolved.ManifestConsistencyFindings) > 0 {
		fmt.Fprintln(opts.out, "\nMapping findings:")
		for _, f := range resolved.ManifestConsistencyFindings {
			fmt.Fprintf(opts.out, "  %s\n", f.Error())
		}
	}
}

// renderClosureLibraries writes the shared documents a Release draws on, one
// section per axis, omitting an axis the closure reached nothing on.
//
// The axes are written in the order the resolution declares them, which is the
// order every other surface reports them in.
func renderClosureLibraries(opts inspectOptions, cl closure.Closure) {
	axes := []struct {
		label string
		libs  []closure.ResolvedLibrary
	}{
		{"MappingLibrary", cl.MappingLibraries},
		{"SchemaLibrary", cl.SchemaLibraries},
		{"WorkloadLibrary", cl.WorkloadLibraries},
		{"ArtifactsLibrary", cl.ArtifactsLibraries},
	}
	for _, axis := range axes {
		if len(axis.libs) == 0 {
			continue
		}
		fmt.Fprintf(opts.out, "\n%s (%d):\n", axis.label, len(axis.libs))
		for _, l := range axis.libs {
			fmt.Fprintf(opts.out, "  %s\n", closureLine(opts, l.URI, l.ResolvedVersion))
		}
	}
}

// closureLine formats one reached entity: its reference, and the version it
// resolved to where it resolved to one.
//
// The version is the resolved value rather than whatever a document asked for — a
// reference naming no version resolves to one, and that resolved value is what a
// deployment is actually built from. A reference that resolved to no version at
// all prints its reference alone rather than an empty pair of brackets.
func closureLine(opts inspectOptions, uri, version string) string {
	if version == "" {
		return opts.colorize.uri(uri)
	}
	return fmt.Sprintf("%s (%s)", opts.colorize.uri(uri), version)
}
