// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"sort"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// TestPinsFromLockfileCoversSameURIsAsPinsByURI enforces that the two lockfile
// pin projections stay over the identical URI set. PinsFromLockfile (version-aware
// Pin structs for the reuse/bare-URI-fetch path) and Lockfile.PinsByURI
// (URI->digest for the re-pull drift check) both claim to cover every kind the
// closure walker resolves through the Tier-2 cache. They are separate builders,
// so a new lockfile section wired into one and missed in the other diverges
// silently — a bare-URI pin present for the drift check but absent for the
// versioned cache-slot fetch (or the reverse). This guard fails that divergence.
func TestPinsFromLockfileCoversSameURIsAsPinsByURI(t *testing.T) {
	lf := &lockfile.Lockfile{
		Entities: []lockfile.Entity{
			{URI: "core/mariadb:1.0.0", ResolvedVersion: "1.0.0", Digest: "sha256:aaa"},
		},
		SchemaLibraries: []lockfile.SchemaLibrary{
			{URI: "core/DbSizing:1.0.0", ResolvedVersion: "1.0.0", Digest: "sha256:bbb"},
		},
		MappingLibraries: []lockfile.MappingLibrary{
			{URI: "core/BaseMappings:1.0.0", ResolvedVersion: "1.0.0", Digest: "sha256:ccc"},
		},
		WorkloadLibraries: []lockfile.WorkloadLibrary{
			{URI: "core/BaseWorkloads:1.0.0", ResolvedVersion: "1.0.0", Digest: "sha256:ddd"},
		},
		ArtifactLibraries: []lockfile.ArtifactLibrary{
			{URI: "core/BaseArtifacts:1.0.0", ResolvedVersion: "1.0.0", Digest: "sha256:eee"},
		},
		Plugins: []lockfile.Plugin{
			{URI: "core/policy:1.0.0", ResolvedVersion: "1.0.0", Digest: "sha256:fff"},
		},
		SourceArtifacts: []lockfile.SourceArtifact{
			{Kind: "helm-oci-chart", URL: "oci://example/chart", Digest: "sha256:ggg"},
		},
	}

	fromLockfile := PinsFromLockfile(lf)
	byURI := lf.PinsByURI()

	keys := func(m map[string]struct{}) []string {
		out := make([]string, 0, len(m))
		for k := range m {
			out = append(out, k)
		}
		sort.Strings(out)
		return out
	}
	fromSet := map[string]struct{}{}
	for k := range fromLockfile {
		fromSet[k] = struct{}{}
	}
	byURISet := map[string]struct{}{}
	for k := range byURI {
		byURISet[k] = struct{}{}
	}

	for k := range fromSet {
		if _, ok := byURISet[k]; !ok {
			t.Errorf("URI %q pinned by PinsFromLockfile but missing from PinsByURI (drift check)", k)
		}
	}
	for k := range byURISet {
		if _, ok := fromSet[k]; !ok {
			t.Errorf("URI %q pinned by PinsByURI but missing from PinsFromLockfile (versioned fetch)", k)
		}
	}

	// Every closure-walked section must be represented; source artifacts excluded.
	// Named rather than counted: a count agrees when both sides are short the same
	// row, and a bare number goes stale the moment an axis is added while still
	// reading as deliberate. This list and the fixture above move together.
	wantURIs := []string{
		"core/BaseArtifacts:1.0.0",
		"core/BaseMappings:1.0.0",
		"core/BaseWorkloads:1.0.0",
		"core/DbSizing:1.0.0",
		"core/mariadb:1.0.0",
		"core/policy:1.0.0",
	}
	for _, want := range wantURIs {
		if _, ok := fromSet[want]; !ok {
			t.Errorf("PinsFromLockfile does not cover %q — one closure-walked kind is unpinned", want)
		}
	}
	for _, got := range keys(fromSet) {
		if !containsPinURI(wantURIs, got) {
			t.Errorf("PinsFromLockfile covers unexpected URI %q; source artifacts are excluded and every other kind is named above", got)
		}
	}
}

func containsPinURI(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
