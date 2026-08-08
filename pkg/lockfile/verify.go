// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile

import (
	"errors"
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ErrLockfileDrift wraps every drift detection failure raised by
// Verify. Errors satisfying errors.Is(err, ErrLockfileDrift()) carry a
// wrapped message naming every offending URI surfaced across the
// closure axes (entities, schema libraries, mapping libraries,
// workload libraries, Wasm plugins). Each axis collects all of its drift entries before
// returning, so an operator with multiple drifts sees the full list
// in one diagnostic rather than one URI per iteration. The
// documented remediation is
// `aggrega pull --upgrade --release <release>`.
//
// Cross-tier digest semantics: ClosureLookup standardises on the
// content digest at every tier — Tier-3 (OCI pull) computes
// sha256(result.Bytes) directly after pkg/oci.Pull returns, ignoring
// pkg/oci.Pull's manifest-digest field; Tier-1 and Tier-2 hits hash
// the entity bytes the same way. Identical content vendored across
// tiers does not drift; drift surfaces only when the content
// actually changes (re-published tag, vendored re-tarred content,
// edited workspace catalog entity).
func ErrLockfileDrift() error { return errLockfileDrift }

var errLockfileDrift = cerrors.Register("lockfile/lockfile-drift", cerrors.Meta{
	Message: "lockfile: out of date; it no longer matches the current catalog",
})

// Verify checks every URI in the freshly-computed closure against
// the loaded lockfile's pinned (version, digest). Returns nil when
// every closure URI has a matching lockfile entry; returns an error
// wrapping ErrLockfileDrift when any axis surfaces drift — every
// drift entry across every axis is reported in one joined error so
// operators see the full picture.
//
// Walks six axes: Entities, SchemaLibraries, MappingLibraries,
// WorkloadLibraries, ArtifactsLibraries, Plugins. SourceArtifacts are not verified here — they pass
// through pkg/sourcecache.LookupAndExtract's existing digest gate at
// build-time read, which compares the cached blob's content digest
// against the lockfile-supplied digest the orchestrator routes
// through the sourcecache shim. Wiring the same comparison here
// would double-verify the same condition.
func Verify(lf *Lockfile, expected closure.Closure) error {
	var driftErrs []error
	driftErrs = append(driftErrs, collectDrift("entity",
		driftKeysOf(lf.Entities, entityKey), refKeys(expected.Entities))...)
	// The library axes report in declared order, each under its own operator-facing
	// label, so the labels here cannot disagree with the ones the rest of the
	// toolchain uses for the same axis.
	for _, sec := range librarySections() {
		driftErrs = append(driftErrs, collectDrift(sec.axis.Label,
			sec.keys(lf), libraryKeys(sec.axis.Libraries(&expected)))...)
	}
	driftErrs = append(driftErrs, collectDrift("wasm-plugin",
		driftKeysOf(lf.Plugins, pluginKey), refKeys(expected.Plugins))...)
	if len(driftErrs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrLockfileDrift(), errors.Join(driftErrs...))
}

// driftKey is the identity triple every axis is compared on: the reference that
// joins a lockfile row to a closure member, and the pinned pair that must match.
type driftKey struct {
	URI             string
	ResolvedVersion string
	Digest          string
}

func entityKey(e Entity) driftKey {
	return driftKey{URI: e.URI, ResolvedVersion: e.ResolvedVersion, Digest: e.Digest}
}

func pluginKey(p Plugin) driftKey {
	return driftKey{URI: p.URI, ResolvedVersion: p.ResolvedVersion, Digest: p.Digest}
}

func refKeys(refs []closure.ResolvedRef) []driftKey {
	return driftKeysOf(refs, func(r closure.ResolvedRef) driftKey {
		return driftKey{URI: r.URI, ResolvedVersion: r.ResolvedVersion, Digest: r.Digest}
	})
}

func libraryKeys(libs []closure.ResolvedLibrary) []driftKey {
	return driftKeysOf(libs, func(l closure.ResolvedLibrary) driftKey {
		return driftKey{URI: l.URI, ResolvedVersion: l.ResolvedVersion, Digest: l.Digest}
	})
}

// collectDrift reports every way one axis's lockfile rows disagree with the
// closure just computed: a reference the lockfile does not pin, a reference whose
// pinned pair moved, and a pinned reference the release no longer reaches.
//
// It is written once and given the axis's label, so every axis reports the same
// three conditions in the same words. A per-axis copy is how one axis comes to
// report a condition its siblings do not — and drift the operator is never told
// about is drift they cannot act on.
func collectDrift(label string, have, want []driftKey) []error {
	haveByURI := make(map[string]driftKey, len(have))
	for _, h := range have {
		haveByURI[h.URI] = h
	}
	seen := make(map[string]struct{}, len(want))
	var out []error
	for _, w := range want {
		seen[w.URI] = struct{}{}
		h, ok := haveByURI[w.URI]
		if !ok {
			out = append(out, fmt.Errorf("%s %q not in lockfile", label, w.URI))
			continue
		}
		if h.ResolvedVersion != w.ResolvedVersion || h.Digest != w.Digest {
			out = append(out, fmt.Errorf("%s %q changed: lockfile=(%s,%s) current=(%s,%s)",
				label, w.URI, h.ResolvedVersion, h.Digest, w.ResolvedVersion, w.Digest))
		}
	}
	for _, uri := range sortedExtras(haveByURI, seen) {
		out = append(out, fmt.Errorf("%s %q no longer used by the release", label, uri))
	}
	return out
}

func sortedExtras[T any](have map[string]T, seen map[string]struct{}) []string {
	var extras []string
	for uri := range have {
		if _, ok := seen[uri]; !ok {
			extras = append(extras, uri)
		}
	}
	sort.Strings(extras)
	return extras
}
