// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"fmt"
	"io"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// deprecationView is the display-facing summary of the latest deprecation
// marker recorded for an artifact. It is read from the local registry index — the
// snapshot a registry update builds — where a marker is recorded only once its own
// signature has verified against the honored signer set. So a view surfaced here
// rests on a signed claim rather than an unauthenticated one, and the same
// recorded fact is what the resolution step reads when it refuses to select a
// withdrawn version.
//
// What it is NOT is current. The record says what the registry reported when the
// index was last built; a marker attached since then is not in it. ObservedAt
// carries that moment so a stale record is distinguishable from a fresh one, and
// HasObservedAt separates an index that carries no build time from one whose build
// time happens to be the zero time. A display that omitted the moment would invite
// the reader to treat the absence of a marker as evidence of non-withdrawal, which
// the design cannot support.
type deprecationView struct {
	Severity    string
	Reason      string
	Replacement string
	IssuedOn    string
	// Tag is the version the winning marker was recorded on, so a per-repository
	// advisory can name which release is deprecated.
	Tag string
	// ObservedAt is when the index snapshot this view was read from was built.
	ObservedAt    time.Time
	HasObservedAt bool
}

// repoDeprecationLookup resolves the latest deprecation marker for a repository
// path from the local index, best-effort. It reports the view and whether one
// was found; a repository absent from the index yields (zero, false) so a lookup
// never fails its caller. A nil lookup means the advisory is disabled (no local
// index, or no registry configured).
type repoDeprecationLookup func(repoPath string) (deprecationView, bool)

// newRepoDeprecationLookup builds the index-served repository-deprecation lookup
// for a registry host, or returns nil when the advisory is disabled — an empty
// host, or no local index built for the host. It reads the host's local
// discovery index once and serves each per-repository advisory from the recorded
// per-version markers, so it issues no network call and works in an offline
// workspace with a built index. It is a package-level seam so inspect tests
// exercise the surfacing without a cache.
var newRepoDeprecationLookup = func(rw *workspaceconfig.ResolvedWorkspace, host string) repoDeprecationLookup {
	if host == "" {
		return nil
	}
	loaded, err := registryindex.LoadHost(rw.Root(), rw.Config().ResolvedArtifactCacheDir(), host)
	if err != nil {
		// No local index for the host (not built), an unreadable one, or one the
		// read hold refuses because it leads through a link out of the root the
		// cache is held to — nothing is read through that link: the advisory is
		// best-effort, decides nothing, and simply shows nothing, the same silent
		// degrade the probe had when it could not reach the registry.
		return nil
	}
	byURI := make(map[string][]registryindex.VersionDeprecation, len(loaded.Records))
	for _, rec := range loaded.Records {
		if len(rec.Deprecations) > 0 {
			byURI[rec.URI] = rec.Deprecations
		}
	}
	return func(repoPath string) (deprecationView, bool) {
		deps, ok := byURI["oci://"+host+"/"+repoPath]
		if !ok {
			return deprecationView{}, false
		}
		v, found := latestDeprecationView(deps)
		if !found {
			return deprecationView{}, false
		}
		// The moment the snapshot was taken travels with the view, so the display
		// states what the record is as-of rather than implying it is current.
		v.ObservedAt, v.HasObservedAt = loaded.LastUpdated, loaded.HasTimestamp
		return v, true
	}
}

// latestDeprecationView selects the newest recorded deprecation across a
// repository's per-version markers and renders the display view. The newest
// issuedOn wins — the same ordering the recording pass applies within a version —
// with the version string as the deterministic tiebreak when two versions share
// an issuedOn (the higher version wins), so the view is stable regardless of the
// stored order. It reports the view and whether any marker was present.
//
// A record carrying no verified signature is not a candidate at all: the trust
// test is honoredRecord, the same one the selection screen applies, so a report
// and a refusal cannot come to disagree about whether a record may be acted on.
// EVERY severity survives that test. A display and a screen differ on severity by
// design — an advisory deprecation is exactly what a reader browsing an entity
// wants to be told and exactly what must not change which version a build takes —
// so mirroring the screen's severity filter here would hide the advisory
// severities and report only the strongest one.
func latestDeprecationView(deps []registryindex.VersionDeprecation) (deprecationView, bool) {
	var (
		best     deprecation.Marker
		bestVer  string
		bestTime time.Time
		bestHas  bool
		found    bool
	)
	for _, d := range deps {
		if !registryindex.Honored(d) {
			continue
		}
		t, has := d.Marker.IssuedOnTime()
		if !found || registryindex.LaterMarker(has, t, d.Version, bestHas, bestTime, bestVer) {
			best, bestVer, bestTime, bestHas, found = d.Marker, d.Version, t, has, true
		}
	}
	if !found {
		return deprecationView{}, false
	}
	return deprecationView{
		Severity:    string(best.Severity),
		Reason:      best.Reason,
		Replacement: best.Replacement,
		IssuedOn:    best.IssuedOn,
		Tag:         bestVer,
	}, true
}

// lookupEntityDeprecation resolves the latest recorded deprecation marker for a
// catalog entity of ANY kind from the local discovery index, best-effort. It
// returns (zero, false) when the advisory is disabled (no local index or no
// registry) or when no honored marker is recorded. It is a package-level seam so
// inspect tests exercise the surfacing without a cache.
//
// The three arguments are the entity's identity as the location rule takes it: the
// kind, the scope segment kinds that carry one contribute (a recipe's capability,
// a plugin's pluginType) and the rest leave empty, and the bare identity name. They
// are read from the RESOLVED entity rather than from the identifier a person typed,
// because catalog resolution accepts a version-pinned identifier: deriving the
// location from the typed string would produce a repository path carrying a version
// suffix, which no index holds. That miss is indistinguishable from "not withdrawn"
// — the surface would report nothing and look correct — so the derivation takes the
// entity's own identity, which carries no version.
//
// The path itself comes from the single OCI-location rule, the same one push
// publishes through and a registry-backed pull binds through, so the advisory reads
// the repository the entity actually publishes to for every kind.
//
// The registry is the workspace's default one, which is the whole of where a bare
// catalog identity resolves from: every entity-pull entrypoint binds a bare
// identity through the configured default registry, so the default registry's index
// is the index for the artifact this identity names. Reading a second registry's
// index would report a marker recorded against a different artifact that merely
// shares a repository path. The index is kept per host, so it is read by the
// default's host, and the repository is the one the location rule places under
// the default's repository path, where a pull fetches the entity.
var lookupEntityDeprecation = func(rw *workspaceconfig.ResolvedWorkspace, kind, provider, name string) (deprecationView, bool) {
	location := rw.Config().DefaultRegistryLocation()
	lookup := newRepoDeprecationLookup(rw, location.Host)
	if lookup == nil {
		return deprecationView{}, false
	}
	return lookup(location.Repository(catalog.OCILocation(kind, provider, name)))
}

// renderDeprecationBlock writes a multi-line deprecation notice for an inspected
// entity: the severity (and the version it was found on), the reason, the
// replacement when named, the issue date, and the moment the record was observed.
//
// The observation moment is not decoration. Two different times are in play and
// conflating them would mislead: the issue date is when the publisher issued the
// marker, while the observed moment is when this workspace last looked. Only the
// second tells the reader how far the record might have fallen behind, so it names
// the command that refreshes it.
func renderDeprecationBlock(out io.Writer, v deprecationView) {
	fmt.Fprintf(out, "Deprecation: %s", v.Severity)
	if v.Tag != "" {
		fmt.Fprintf(out, " (version %s)", v.Tag)
	}
	fmt.Fprintln(out)
	if v.Reason != "" {
		fmt.Fprintf(out, "  Reason: %s\n", v.Reason)
	}
	if v.Replacement != "" {
		fmt.Fprintf(out, "  Replacement: %s\n", v.Replacement)
	}
	if v.IssuedOn != "" {
		fmt.Fprintf(out, "  Issued: %s\n", v.IssuedOn)
	}
	if v.HasObservedAt {
		fmt.Fprintf(out, "  Recorded as of: %s (run `aggrega registry update` to refresh)\n",
			v.ObservedAt.UTC().Format(time.RFC3339))
	} else {
		fmt.Fprintln(out, "  Recorded from the local registry index, which carries no build time (run `aggrega registry update` to refresh)")
	}
}
