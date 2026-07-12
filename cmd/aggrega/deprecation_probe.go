// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// deprecationView is the display-facing summary of the latest deprecation
// marker resolved for an artifact at query time. It is read live from the
// registry for advisory display in search and inspect and is NOT
// signature-verified — the signed, verified enforcement path runs at pull time
// and records the build-time gate. So a view surfaced here is a hint, not a
// trust decision.
type deprecationView struct {
	Severity    string
	Reason      string
	Replacement string
	IssuedOn    string
	// Tag is the version the winning marker was found on, so a per-repository
	// probe can name which release is deprecated.
	Tag string
}

// repoDeprecationLookup resolves the latest deprecation marker for a repository
// path, best-effort. It reports the view and whether one was found; any failure
// yields (zero, false) so a probe never fails its caller. A nil lookup means
// probing is disabled (offline, or no registry configured).
type repoDeprecationLookup func(repoPath string) (deprecationView, bool)

// newRepoDeprecationLookup builds the live repository-deprecation lookup for a
// registry host, or returns nil when probing is disabled — an offline workspace
// or an empty host. It is a package-level seam so search and inspect tests
// exercise the surfacing without a network. Each probe constructs a repository
// client through the shared credential path and reads referrers live.
var newRepoDeprecationLookup = func(cfg workspaceconfig.Config, host string) repoDeprecationLookup {
	if host == "" || cfg.Offline {
		return nil
	}
	return func(repoPath string) (deprecationView, bool) {
		repo, err := oci.NewRemoteRepository(host + "/" + repoPath)
		if err != nil {
			return deprecationView{}, false
		}
		return probeLatestDeprecation(context.Background(), repo)
	}
}

// probeLatestDeprecation resolves the newest deprecation marker across every tag
// of a repository target. For each tag it lists the deprecation referrers,
// selects that tag's authoritative marker by issuedOn, and keeps the newest
// across all tags — the same newest-issuedOn ordering the discovery pass uses,
// with the manifest digest as the deterministic tiebreak. Every step is
// best-effort: a tag-listing, referrer-listing, fetch, or decode failure skips
// that tag rather than failing the probe, so a display probe never breaks the
// caller. It reports the view and whether any marker was found.
func probeLatestDeprecation(ctx context.Context, target oras.ReadOnlyTarget) (deprecationView, bool) {
	tags, err := oci.ListTags(ctx, target)
	if err != nil {
		return deprecationView{}, false
	}
	var (
		best     deprecation.Marker
		bestTag  string
		bestTime time.Time
		bestHas  bool
		bestDig  string
		found    bool
	)
	for _, tag := range tags {
		markers, lerr := oci.ListReferrers(ctx, target, tag, deprecation.ArtifactType)
		if lerr != nil || len(markers) == 0 {
			continue
		}
		desc, marker, merr := latestDeprecationMarker(ctx, target, markers)
		if merr != nil {
			continue
		}
		t, has := marker.IssuedOnTime()
		if !found || laterMarker(has, t, desc.Digest.String(), bestHas, bestTime, bestDig) {
			best, bestTag, bestTime, bestHas, bestDig, found = marker, tag, t, has, desc.Digest.String(), true
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
		Tag:         bestTag,
	}, true
}

// lookupComponentDeprecation probes the workspace's configured registry for the
// latest deprecation marker on a catalog Component, best-effort. It returns
// (zero, false) when probing is disabled (offline or no registry) or when no
// marker is found. The repository path derives from the single OCI-location rule
// so it matches where the component publishes. It is a package-level seam so
// inspect tests exercise the surfacing without a network.
var lookupComponentDeprecation = func(cfg workspaceconfig.Config, componentName string) (deprecationView, bool) {
	lookup := newRepoDeprecationLookup(cfg, cfg.DefaultRegistry)
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

// formatDeprecationSuffix renders a one-line deprecation summary appended to a
// search result row: the severity (and the tag it was found on), the reason,
// the replacement when one is named, and the issue date.
func formatDeprecationSuffix(v deprecationView) string {
	var b strings.Builder
	b.WriteString("[deprecated: ")
	b.WriteString(v.Severity)
	if v.Tag != "" {
		b.WriteString(" @ ")
		b.WriteString(v.Tag)
	}
	b.WriteString("]")
	if v.Reason != "" {
		b.WriteString(" ")
		b.WriteString(v.Reason)
	}
	if v.Replacement != "" {
		b.WriteString(" (use ")
		b.WriteString(v.Replacement)
		b.WriteString(")")
	}
	if v.IssuedOn != "" {
		b.WriteString(" (since ")
		b.WriteString(v.IssuedOn)
		b.WriteString(")")
	}
	return b.String()
}
