// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

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
			kind:   entityKindForDetail(e.KindHint, e.ReferencedFrom),
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
	for _, l := range cl.ArtifactsLibraries {
		rows = append(rows, row{tier: pullEventTier(l.ResolutionPath), kind: "ArtifactsLibrary", uri: l.URI})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].uri != rows[j].uri {
			return rows[i].uri < rows[j].uri
		}
		return rows[i].ref < rows[j].ref
	})

	fmt.Fprintf(out, "resolved %d entities (this pull):\n", len(rows))
	tw := tabwriter.NewWriter(out, 2, 2, 2, ' ', 0)
	for _, r := range rows {
		if r.hasRef {
			fmt.Fprintf(tw, "  %s\t%s\t%s\t← %s\n", r.tier, r.kind, r.uri, r.ref)
		} else {
			fmt.Fprintf(tw, "  %s\t%s\t%s\n", r.tier, r.kind, r.uri)
		}
	}
	tw.Flush()
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

// pullEventTier maps the in-memory resolution tag to the word this column
// prints for the source that served the entity. The words come from the shared
// provenance vocabulary, so this column and the plugin-resolution diagnostics
// name the same source identically and cannot drift apart.
//
// An unrecognised tag passes through verbatim rather than being silently
// dropped: a tag with no word yet is a gap worth seeing in the output.
func pullEventTier(resolutionPath string) string {
	if source, ok := closure.ResolutionSourceFor(resolutionPath); ok {
		return string(source)
	}
	return resolutionPath
}

// entityKindForDetail derives the display kind for a resolved entity. The
// closure's KindHint is authoritative when present, but it is empty for the
// top-level aggregate (recorded with no hint), Components, and Features — so the
// kind is otherwise mapped from the entity's reference-site role, which is
// always present. Falls back to "Entity" when no rule matches.
func entityKindForDetail(kindHint string, referencedFrom []string) string {
	switch kindHint {
	case "recipe":
		return "Recipe"
	case "feature_recipe":
		return "FeatureRecipe"
	case "aggregate":
		return "Aggregate"
	}
	for _, ref := range referencedFrom {
		if k := kindFromReferenceSite(ref); k != "" {
			return k
		}
	}
	return "Entity"
}

// kindFromReferenceSite maps an authoring-site dotted path to the catalog kind
// it references. The Manifest mapping-value sites are matched first, because a
// mapping path (`manifest.spec.mappings.components.type.<type>`) denotes the
// Recipe/FeatureRecipe behind the type, not the Component/Feature itself.
//
// Instance-type sites end `.<collection>.<instance>.type`; the kind is the
// collection segment IMMEDIATELY before the instance name — not any earlier
// segment — so an attachment nested under a component
// (`…components.api.features.cache.type`) maps to Feature, not the enclosing
// Component. Every attachment family reaches this table through `features`,
// because that is the one attachment collection an authoring surface carries.
func kindFromReferenceSite(ref string) string {
	switch {
	case ref == "release.spec.aggregate":
		return "Aggregate"
	case ref == "release.spec.manifest" || ref == "manifest.spec.extends":
		return "Manifest"
	case strings.Contains(ref, "mappings.components.type."):
		return "Recipe"
	case strings.Contains(ref, "mappings.features.type."):
		return "FeatureRecipe"
	}
	if tokens := strings.Split(ref, "."); len(tokens) >= 3 && tokens[len(tokens)-1] == "type" {
		switch tokens[len(tokens)-3] {
		case "components":
			return "Component"
		case "features":
			return "Feature"
		case "aggregates":
			return "Aggregate"
		}
	}
	return ""
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
