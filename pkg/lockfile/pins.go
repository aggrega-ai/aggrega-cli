// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package lockfile

import "github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"

// PluginPins derives the per-URI plugin pin maps a construction site threads into
// the artifact resolver: the content digest each plugin's resolved bytes verify
// against, the resolved version that addresses its versioned cache slot, and — for
// a row carrying a verified signature fact — the recorded digest of its cached
// signature-bundle blob. releasePath is the Release the record belongs to, as the
// reader named it: the lockfile beside it (PathFor) is named as where those facts
// are recorded and the Release as the one whose pull repairs them, so a refusal of
// one names both. An empty releasePath names neither.
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
func PluginPins(lf *Lockfile, releasePath string) pluginpath.PinMaps {
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
		if releasePath != "" {
			out.RecordedIn = PathFor(releasePath)
			out.Release = releasePath
		}
	}
	return out
}

// FindSourceArtifact returns the source-artifact row this record pins for the
// artifact a caller has just resolved, and whether one is present. A nil record
// pins nothing and reports absence rather than panicking, so a caller holding an
// optional record asks the question the same way as one holding a loaded record.
//
// The location terms are the whole of {kind, url, chart, moduleSubdir}: url is the
// authored identity URL, chart names the chart within a repo root, and moduleSubdir
// the directory within a git-sourced repository, so two charts of one repo root and
// two directories of one repository at one commit are distinct artifacts rather
// than one.
//
// # The join is on what the author wrote, and that is the whole point of the ref term
//
// A caller supplies both the authored reference and the authored version, because
// which of them identifies the artifact depends on whether the kind's resolver
// transforms it. A resolver that pins the authored version verbatim leaves the two
// the same string. A resolver that resolves a reference to something immutable — a
// git tag or branch to a commit — leaves the row pinned at a value the author never
// wrote, so a lookup keyed on the pinned version alone could never find it from the
// authored document.
//
// So the reference is tried first, against the reference the row records having
// resolved; where that finds nothing, the pinned version is matched as before. The
// order matters in one direction only: a row recorded before a reference was
// recorded for it is still found by its pinned version, so a lockfile written
// either way answers the same question.
//
// The lookup lives here rather than at each surface that needs it because this
// package owns what a recorded closure says. Two surfaces asking the same question
// of the same rows would be two readings of one record, free to disagree about
// which terms make an artifact the same artifact — and because the answers feed a
// verification and the fetch it guards, a disagreement between them would decide
// whether the verification runs at all.
func (lf *Lockfile) FindSourceArtifact(kind, url, chart, moduleSubdir, version, ref string) (*SourceArtifact, bool) {
	if lf == nil {
		return nil, false
	}
	// Author-written reference first. A row records the reference on each
	// back-pointer that authored one, so one reference reaching a row through any
	// of them is that row.
	if ref != "" {
		for i := range lf.SourceArtifacts {
			sa := &lf.SourceArtifacts[i]
			if sa.Kind != kind || sa.URL != url || sa.Chart != chart || sa.ModuleSubdir != moduleSubdir {
				continue
			}
			for _, rb := range sa.ReferencedBy {
				if rb.RequestedRef != "" && rb.RequestedRef == ref {
					return sa, true
				}
			}
		}
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
