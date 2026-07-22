// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"io"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// deprecationView is the display-facing summary of the latest deprecation
// marker recorded for an artifact. It is read from the local discovery index
// (the snapshot a registry update builds) for advisory display in inspect, and
// is NOT signature-verified — the signed, verified enforcement path runs at pull
// time and records the build-time gate. So a view surfaced here is a hint, not a
// trust decision.
type deprecationView struct {
	Severity    string
	Reason      string
	Replacement string
	IssuedOn    string
	// Tag is the version the winning marker was recorded on, so a per-repository
	// advisory can name which release is deprecated.
	Tag string
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
	loaded, err := loadHostIndex(rw.Root(), rw.Config().ResolvedArtifactCacheDir(), host)
	if err != nil {
		// No local index for the host (not built) or an unreadable one: the
		// advisory is best-effort and simply shows nothing, the same silent
		// degrade the probe had when it could not reach the registry.
		return nil
	}
	byURI := make(map[string][]VersionDeprecation, len(loaded.Records))
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
		return latestDeprecationView(deps)
	}
}

// latestDeprecationView selects the newest recorded deprecation across a
// repository's per-version markers and renders the display view. The newest
// issuedOn wins — the same ordering the recording pass applies within a version —
// with the version string as the deterministic tiebreak when two versions share
// an issuedOn (the higher version wins), so the view is stable regardless of the
// stored order. It reports the view and whether any marker was present.
func latestDeprecationView(deps []VersionDeprecation) (deprecationView, bool) {
	var (
		best     deprecation.Marker
		bestVer  string
		bestTime time.Time
		bestHas  bool
		found    bool
	)
	for _, d := range deps {
		t, has := d.Marker.IssuedOnTime()
		if !found || laterMarker(has, t, d.Version, bestHas, bestTime, bestVer) {
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

// lookupComponentDeprecation resolves the latest recorded deprecation marker for
// a catalog Component from the local discovery index, best-effort. It returns
// (zero, false) when the advisory is disabled (no local index or no registry) or
// when no marker is recorded. The repository path derives from the single
// OCI-location rule so it matches where the component publishes. It is a
// package-level seam so inspect tests exercise the surfacing without a cache.
var lookupComponentDeprecation = func(rw *workspaceconfig.ResolvedWorkspace, componentName string) (deprecationView, bool) {
	lookup := newRepoDeprecationLookup(rw, rw.Config().DefaultRegistry)
	if lookup == nil {
		return deprecationView{}, false
	}
	return lookup(catalog.OCILocation("Component", "", componentName))
}

// renderDeprecationBlock writes a multi-line deprecation notice for an inspected
// entity: the severity (and the version it was found on), the reason, the
// replacement when named, and the issue date.
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
}
