// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
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
// Release, and at which resolved versions; the node view says what the
// Release looks like. Neither is recoverable from the other: an entity can be
// reached without appearing as a node, and a node does not carry the version its
// type resolved to.
//
// The resolution is the shared one, so what this prints comes from a single
// computation rather than from a second one written beside it that agrees today.
// Nothing is rendered here beyond the listing: no provider guest is loaded and no
// artifact is produced, so the listing reports what the resolution reached and
// nothing the reconstruction of a recorded pin set would add to it.
//
// A record of pinned versions is read where the workspace has one and the view is
// seeded with it, so the versions reported are the ones this Release resolves
// to rather than whatever the catalog happens to hold today. A workspace with no
// such record resolves over the catalog as it stands.
func renderReleaseClosure(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, releasePath string, catalogDirs []string, opts inspectOptions) error {
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return err
	}
	wsRoot := rw.Root()
	wsCfg := rw.Config()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	dirs := rw.ResolveCatalogDirs(catalogDirs)

	view := catalog.NewLazyLookup(wsRoot, cacheDir, dirs, nil)
	lf, _, lfErr := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if lfErr == nil {
		view.SetPins(catalog.PinsFromLockfile(lf))
	} else {
		lf = nil
	}

	// The resolution below runs under the caller's context, so a reader who stops
	// the run stops the entity fetches the walk is making rather than only the
	// reply. The context arrives as an argument because the per-kind dispatch table
	// carries one on its shared function type.
	//
	// What a stop can reach here is bounded by the view above, and the bound is named
	// rather than left to be discovered: the view is built with no registry client,
	// so this listing resolves from the local catalog and the record of pins and
	// makes no fetch of its own. The context is still the caller's, because the
	// resolution is the shared one every surface runs and the surfaces that do supply
	// a client get the stop over the same code; on this one there is nothing outside
	// the process for it to end.
	resolved, cerr := compilerops.ResolveClosure(ctx, compilerops.ClosureInputs{
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
		// the author has to fix. Where no refusal explains it, the reference may
		// name an entity that is sitting in a directory this workspace does not
		// read, so the workspace's own account of where it reads travels too.
		return compilerops.AnnotateSkipContext(cerr, view.SkippedEntities(), rw.LocalCatalogAdviceNote())
	}

	renderClosureListing(opts, lf, resolved)
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
//
// lf is the record of pinned versions the resolution was seeded with, or nil where
// the workspace has none and equally where one is there and did not load. It is
// read for the withdrawal markers it recorded, which is a fact about the record
// rather than about the walk, so those are reported beside the closure. The read is
// nil-safe; what a nil means differs by cause, and only one of the two is free — a
// workspace with no record has no recorded marker either, while a record that did
// not load has markers this listing cannot report. The second is the degradation
// the resolution above already takes on an unreadable record rather than one this
// listing introduces.
func renderClosureListing(opts inspectOptions, lf *lockfile.Lockfile, resolved *compilerops.ResolvedClosure) {
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

	// The findings follow the closure they were found in rather than preceding it,
	// so the listing the caller asked for arrives first and is not mistaken for an
	// error report. This command reports on a Release rather than producing
	// anything from it, so a fault is described beside the closure rather than
	// replacing it with a refusal — the verdict is the reporting caller's, which is
	// exactly why the resolution carries these back as findings instead of failing
	// on them.
	renderClosureFindings(opts, lf, resolved)
}

// renderClosureFindings writes what is wrong with the closure just listed: the
// Manifest mappings whose target does not implement the mapped type, and the
// pinned references a withdrawal marker was recorded against.
//
// The two are separate sections because they are separate populations decided at
// separate moments — one by this resolution, one by the record it was seeded with —
// and a reader acting on either needs to know which. Each section is omitted where
// its population is empty, on the same footing as an axis the closure reached
// nothing on.
func renderClosureFindings(opts inspectOptions, lf *lockfile.Lockfile, resolved *compilerops.ResolvedClosure) {
	if len(resolved.ManifestConsistencyFindings) > 0 {
		fmt.Fprintln(opts.out, "\nMapping findings:")
		for _, f := range resolved.ManifestConsistencyFindings {
			fmt.Fprintf(opts.out, "  %s\n", f.Error())
		}
	}
	// Read through the shared report rather than off the record's rows, so the set
	// of references reported here is the set every reader of that report sees and
	// only the framing around it is this command's.
	if findings := lf.DeprecationFindings(); len(findings) > 0 {
		fmt.Fprintln(opts.out, "\nDeprecations:")
		for _, f := range findings {
			fmt.Fprintf(opts.out, "  %s\n", f.Notice())
		}
	}
}

// renderClosureLibraries writes the shared documents a Release draws on, one
// section per axis, omitting an axis the closure reached nothing on.
//
// It ranges over the closure's own declared axis set rather than naming the axes
// here. That set is the single enumeration of them, so an axis added to it is
// listed by construction — whereas a listing that named the axes itself would go
// on reporting the old set, with nothing failing to say an axis had stopped being
// reported. Each axis is labelled with the kind its members publish under, which
// the declaration carries, so the label is not a second spelling of it either.
//
// The order is the declared one, which is what makes the order one statement
// rather than one per listing, free to disagree with the others.
func renderClosureLibraries(opts inspectOptions, cl closure.Closure) {
	for _, axis := range closure.LibraryAxes() {
		libs := axis.Libraries(&cl)
		if len(libs) == 0 {
			continue
		}
		fmt.Fprintf(opts.out, "\n%s (%d):\n", axis.Kind, len(libs))
		for _, l := range libs {
			fmt.Fprintf(opts.out, "  %s\n", closureLine(opts, l.URI, l.ResolvedVersion))
		}
	}
}

// closureLine formats one reached entity: its reference, and the version it
// resolved to where it resolved to one.
//
// The version is the resolved value rather than whatever a document asked for — a
// reference naming no version resolves to one, and that resolved value is what a
// Release is actually built from. A reference that resolved to no version at
// all prints its reference alone rather than an empty pair of brackets.
func closureLine(opts inspectOptions, uri, version string) string {
	if version == "" {
		return opts.colorize.uri(uri)
	}
	return fmt.Sprintf("%s (%s)", opts.colorize.uri(uri), version)
}
