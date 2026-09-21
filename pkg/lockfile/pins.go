// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile

import "github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"

// PluginPins derives the per-URI plugin pin maps a construction site threads into
// the artifact resolver: the content digest each plugin's resolved bytes verify
// against, the resolved version that addresses its versioned cache slot, and — for
// a row carrying a verified signature fact — the recorded digest of its cached
// signature-bundle blob.
//
// The derivation lives here rather than at the surface that consumes it because
// this package owns what a recorded closure says, and a surface reading those rows
// for itself would be a second reading of the same record, free to disagree with
// this one about which rows contribute and which fields address the slot.
//
// A nil record, or one that pins no plugin, yields the zero value. That is the
// honest result for "this record pins nothing yet" and it is deliberately NOT the
// same fact as "this site has no record at all" — which this function cannot
// express, because it is the calling site's to state rather than this one's to
// infer. The two are told apart where the site writes its pin source, not here.
func PluginPins(lf *Lockfile) pluginpath.PinMaps {
	if lf == nil || len(lf.Plugins) == 0 {
		return pluginpath.PinMaps{}
	}
	pins := make(map[string]string, len(lf.Plugins))
	versions := make(map[string]string, len(lf.Plugins))
	bundleDigests := make(map[string]string, len(lf.Plugins))
	for _, wp := range lf.Plugins {
		pins[wp.URI] = wp.Digest
		versions[wp.URI] = wp.ResolvedVersion
		if wp.Signature != nil && wp.Signature.BundleDigest != "" {
			bundleDigests[wp.URI] = wp.Signature.BundleDigest
		}
	}
	out := pluginpath.PinMaps{Pins: pins, PinVersions: versions}
	// Left nil rather than empty when no row carries one: the bundle check is
	// disabled by an absent map, and an empty one would claim this record was
	// consulted about signature bundles and found none for every plugin.
	if len(bundleDigests) > 0 {
		out.BundleDigests = bundleDigests
	}
	return out
}

// FindSourceArtifact returns the source-artifact row this record pins for the
// identity {kind, url, chart, moduleSubdir, version}, and whether one is present.
// A nil record pins nothing and reports absence rather than panicking, so a
// caller holding an optional record asks the question the same way as one holding
// a loaded record.
//
// The identity is the whole of the five terms: url is the authored identity URL,
// chart names the chart within a repo root, and moduleSubdir the directory within
// a git-sourced repository, so two charts of one repo root and two directories of
// one repository at one commit are distinct artifacts rather than one. version is
// matched against the row's pinned version, which for a kind whose resolver
// transforms the authored version — a git reference resolved to a commit — is the
// resolved value rather than the authored one.
//
// The lookup lives here rather than at each surface that needs it because this
// package owns what a recorded closure says. Two surfaces asking the same question
// of the same rows would be two readings of one record, free to disagree about
// which terms make an artifact the same artifact — and because the answers feed a
// verification and the fetch it guards, a disagreement between them would decide
// whether the verification runs at all.
func (lf *Lockfile) FindSourceArtifact(kind, url, chart, moduleSubdir, version string) (*SourceArtifact, bool) {
	if lf == nil {
		return nil, false
	}
	for i := range lf.SourceArtifacts {
		sa := &lf.SourceArtifacts[i]
		if sa.Kind == kind && sa.URL == url && sa.Chart == chart &&
			sa.ModuleSubdir == moduleSubdir && sa.PinnedVersion == version {
			return sa, true
		}
	}
	return nil, false
}

// PinsByURI returns a map from canonical entity URI to the content
// digest the lockfile pins for it, across entities, schema libraries,
// mapping libraries, workload libraries, and wasm plugins — every kind
// the closure walker resolves through the Tier-2 cache. Source
// artifacts are excluded: they are descended on their own path and
// carry their own read-time digest gate, not the closure walker's
// Tier-2 reuse.
//
// The result feeds the re-pull reuse-check (catalog.ReuseVerification):
// a cached slot whose recomputed digest no longer matches its pin here
// is re-fetched rather than re-pinned from drifted content.
func (lf *Lockfile) PinsByURI() map[string]string {
	pins := make(map[string]string, len(lf.Entities)+len(lf.Plugins))
	for _, e := range lf.Entities {
		pins[e.URI] = e.Digest
	}
	// Every declared library axis contributes, read through the section binding
	// rather than one loop per axis: an axis this projection skipped would have its
	// cached slot re-pinned from whatever content was on disk, with no pin to check
	// it against.
	for _, sec := range librarySections() {
		for _, k := range sec.keys(lf) {
			pins[k.URI] = k.Digest
		}
	}
	for _, p := range lf.Plugins {
		pins[p.URI] = p.Digest
	}
	return pins
}
