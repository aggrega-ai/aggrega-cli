// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// printPullDetail renders the transient pull-event diagnostic for
// `aggrega pull --detail`: per resolved entity (and Wasm plugin), what THIS
// pull run did — fetched fresh from a registry, reused a cache slot, found in a
// local catalog, served from inside the compiler binary, or external. It reads
// the closure's in-memory resolution tag, which is deliberately not persisted in
// the lockfile (a committed lockfile is byte-identical across machines that
// resolved the same closure from different local state). The report is therefore
// unrecoverable after the pull: a freshly-pulled entity is cached, so the same
// lookup reads the cache on the next run.
//
// Scope is the pull EVENT, not post-pull state: it reports the source, the kind,
// the identity, and the always-present referenced-from disambiguator — not
// minimum-version-selection decisions or consumer→library chains, which are
// `aggrega inspect`'s remit (and why the library axes, whose only disambiguator
// is the consumer chain, are not listed here). Output is stdout-only; rows are
// deterministically sorted.
func printPullDetail(out io.Writer, cl closure.Closure) {
	type row struct {
		tier, kind, uri, ref string
		// hasRef is false for library rows: a library's only reference is its
		// consumer chain (inspect territory), so its line omits the referenced-from
		// column to stay pull-event-scoped.
		hasRef bool
	}
	var rows []row
	for _, e := range cl.Entities {
		rows = append(rows, row{
			tier:   pullEventTier(e.ResolutionPath),
			kind:   entityKindForDetail(e),
			uri:    e.URI,
			ref:    joinReferencedFrom(e.ReferencedFrom),
			hasRef: true,
		})
	}
	for _, w := range cl.Plugins {
		rows = append(rows, row{
			tier:   pullEventTier(w.ResolutionPath),
			kind:   "Plugin",
			uri:    w.URI,
			ref:    joinReferencedFrom(w.ReferencedFrom),
			hasRef: true,
		})
	}
	for _, l := range cl.SchemaLibraries {
		rows = append(rows, row{tier: pullEventTier(l.ResolutionPath), kind: "SchemaLibrary", uri: l.URI})
	}
	for _, l := range cl.MappingLibraries {
		rows = append(rows, row{tier: pullEventTier(l.ResolutionPath), kind: "MappingLibrary", uri: l.URI})
	}
	for _, l := range cl.WorkloadLibraries {
		rows = append(rows, row{tier: pullEventTier(l.ResolutionPath), kind: "WorkloadLibrary", uri: l.URI})
	}
	for _, l := range cl.ArtifactLibraries {
		rows = append(rows, row{tier: pullEventTier(l.ResolutionPath), kind: "ArtifactLibrary", uri: l.URI})
	}
	// Two entities of different kinds may share a reference and a reference site —
	// a Component and the Recipe of the same name a Manifest mapping binds to it —
	// so the kind breaks the last tie and the order stays fixed.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].uri != rows[j].uri {
			return rows[i].uri < rows[j].uri
		}
		if rows[i].ref != rows[j].ref {
			return rows[i].ref < rows[j].ref
		}
		return rows[i].kind < rows[j].kind
	})

	// The report is composed whole and written once. A tabwriter writes cell by
	// cell, and the report is written while a progress checklist may be drawn on
	// the terminal; a write that ends part way along a line would be drawn over by
	// the checklist's next redraw.
	var report bytes.Buffer
	fmt.Fprintf(&report, "resolved %s (this pull):\n", countOfNoun(len(rows), "entity", "entities"))
	tw := tabwriter.NewWriter(&report, 2, 2, 2, ' ', 0)
	for _, r := range rows {
		if r.hasRef {
			fmt.Fprintf(tw, "  %s\t%s\t%s\t← %s\n", r.tier, r.kind, r.uri, r.ref)
		} else {
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", r.tier, r.kind, r.uri)
		}
	}
	tw.Flush()
	_, _ = out.Write(report.Bytes())
}

// printHeldBelowNewest renders, for `aggrega pull --detail`, one line per
// reference the pull settled below the newest version held: the reference, the
// version it is selected at, the newest version held, and the constraints that do
// not admit the newest, each as the validation detail for a held-back property
// names it. The rows arrive in the settlement's visit order and are printed in it.
// Like the rest of the report it is composed whole and written once.
func printHeldBelowNewest(out io.Writer, held []catalog.HeldReference) {
	var report bytes.Buffer
	for _, h := range held {
		var holding []string
		for _, n := range h.Notes() {
			holding = append(holding, n.Caption+" "+n.Value)
		}
		fmt.Fprintf(&report, "  %s %s is selected at %s, below %s, the newest held: held by %s\n",
			closure.DocumentKind(h.Kind), h.Reference, h.Selected, h.Newest, strings.Join(holding, ", "))
	}
	_, _ = out.Write(report.Bytes())
}

// printLockfileToolchain renders the toolchain stamp an existing lockfile
// records: the compiler version that wrote the file, then one row per core
// plugin the compiler carried, in plugin-coordinate order.
//
// It is a separate printer from printPullDetail rather than a section of it,
// because the two report different subjects. printPullDetail reports the pull
// EVENT — what this run did, read from transient in-memory state — while this
// reports post-pull STATE read back out of the lockfile on disk. Folding the
// stamp into that printer would contradict the scope its own documentation
// states.
//
// It keeps that printer's two properties: output goes only to the supplied
// writer (stdout in production, never the diagnostic stream), and rows are
// deterministically ordered. Order comes from the caller's slice, which
// WriteLockfile canonicalises by plugin coordinate, so the printed order is the
// recorded order and this function introduces no ordering of its own.
//
// A nil stamp prints nothing at all. A lockfile written before the stamp existed
// carries none, and reporting its absence would be a notice about a field that is
// deliberately not enforced — the stamp produces no halt, no warning and no
// verdict, so it produces no complaint about being missing either.
//
// "reported, not verified" is on the surface deliberately. This runs directly
// after the line stating that the lockfile matches the current dependency
// resolution, and a reader who saw a toolchain reported there could otherwise
// reasonably conclude the toolchain had been checked too. Nothing compares it:
// the toolchain is not part of the pinned closure.
func printLockfileToolchain(out io.Writer, stamp *lockfile.ToolchainStamp) {
	if stamp == nil {
		return
	}
	fmt.Fprintf(out, "toolchain recorded by the pull that wrote it (reported, not verified): compiler %s\n",
		stamp.CompilerVersion)
	if len(stamp.CorePlugins) == 0 {
		return
	}
	tw := tabwriter.NewWriter(out, 2, 2, 2, ' ', 0)
	for _, p := range stamp.CorePlugins {
		fmt.Fprintf(tw, "  %s\t%s\n", p.Plugin, p.Version)
	}
	tw.Flush()
}

// pullEventTier is the text this column prints for the source that served the
// entity, read from the in-memory resolution tag.
//
// It is the shared composition and nothing of its own: the structured answers
// reporting the same resolution read that composition too, and its words are the
// shared provenance vocabulary the plugin-resolution diagnostics also read, so none
// of them can name a source differently. That includes the fallback: an
// unrecognised tag passes through verbatim rather than being silently dropped,
// because a tag with no word yet is a gap worth seeing.
func pullEventTier(resolutionPath string) string {
	return closure.ReportedResolutionSource(resolutionPath)
}

// entityKindForDetail is the kind a resolved entity's row prints: the kind the
// walk collected it as, named as an author writes it (closure.DocumentKind), which
// is the kind every other report of a resolution names it by. "Entity" stands in
// only for an entity recorded with no kind.
func entityKindForDetail(e closure.ResolvedRef) string {
	if kind := closure.DocumentKind(e.Kind()); kind != "" {
		return kind
	}
	return "Entity"
}

// joinReferencedFrom renders the referenced-from set deterministically. The
// closure already sorts the slice; this joins it for the single-line display
// and shows a placeholder when (unexpectedly) empty.
func joinReferencedFrom(referencedFrom []string) string {
	if len(referencedFrom) == 0 {
		return "(no reference site)"
	}
	sorted := append([]string(nil), referencedFrom...)
	sort.Strings(sorted)
	return strings.Join(sorted, ", ")
}
