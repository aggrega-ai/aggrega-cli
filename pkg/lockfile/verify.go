// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile

import (
	"errors"
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// ErrLockfileDrift wraps every drift detection failure raised by
// Verify. Errors satisfying errors.Is(err, ErrLockfileDrift) carry a
// wrapped message naming every offending URI surfaced across the
// closure axes (entities, schema libraries, mapping libraries,
// workload libraries, Wasm plugins). Each axis collects all of its drift entries before
// returning, so an operator with multiple drifts sees the full list
// in one diagnostic rather than one URI per iteration. The
// documented remediation is `aggrega pull --upgrade <release>`.
//
// Cross-tier digest semantics: ClosureLookup standardises on the
// content digest at every tier — Tier-3 (OCI pull) computes
// sha256(result.Bytes) directly after pkg/oci.Pull returns, ignoring
// pkg/oci.Pull's manifest-digest field; Tier-1 and Tier-2 hits hash
// the entity bytes the same way. Identical content vendored across
// tiers does not drift; drift surfaces only when the content
// actually changes (re-published tag, vendored re-tarred content,
// edited workspace catalog entity).
var ErrLockfileDrift = errors.New("lockfile: out of date; it no longer matches the current catalog")

// Verify checks every URI in the freshly-computed closure against
// the loaded lockfile's pinned (version, digest). Returns nil when
// every closure URI has a matching lockfile entry; returns an error
// wrapping ErrLockfileDrift when any axis surfaces drift — every
// drift entry across every axis is reported in one joined error so
// operators see the full picture.
//
// Walks five axes: Entities, SchemaLibraries, MappingLibraries,
// WorkloadLibraries, Plugins. SourceArtifacts are not verified here — they pass
// through pkg/sourcecache.LookupAndExtract's existing digest gate at
// build-time read, which compares the cached blob's content digest
// against the lockfile-supplied digest the orchestrator routes
// through the sourcecache shim. Wiring the same comparison here
// would double-verify the same condition.
func Verify(lf *Lockfile, expected closure.Closure) error {
	var driftErrs []error
	driftErrs = append(driftErrs, collectEntityDrift(lf.Entities, expected.Entities)...)
	driftErrs = append(driftErrs, collectSchemaLibraryDrift(lf.SchemaLibraries, expected.SchemaLibraries)...)
	driftErrs = append(driftErrs, collectMappingLibraryDrift(lf.MappingLibraries, expected.MappingLibraries)...)
	driftErrs = append(driftErrs, collectWorkloadLibraryDrift(lf.WorkloadLibraries, expected.WorkloadLibraries)...)
	driftErrs = append(driftErrs, collectPluginDrift(lf.Plugins, expected.Plugins)...)
	if len(driftErrs) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrLockfileDrift, errors.Join(driftErrs...))
}

func collectEntityDrift(have []Entity, want []closure.ResolvedRef) []error {
	haveByURI := make(map[string]Entity, len(have))
	for _, e := range have {
		haveByURI[e.URI] = e
	}
	seen := make(map[string]struct{}, len(want))
	var out []error
	for _, w := range want {
		seen[w.URI] = struct{}{}
		h, ok := haveByURI[w.URI]
		if !ok {
			out = append(out, fmt.Errorf("entity %q not in lockfile", w.URI))
			continue
		}
		if h.ResolvedVersion != w.ResolvedVersion || h.Digest != w.Digest {
			out = append(out, fmt.Errorf("entity %q changed: lockfile=(%s,%s) current=(%s,%s)",
				w.URI, h.ResolvedVersion, h.Digest, w.ResolvedVersion, w.Digest))
		}
	}
	for _, uri := range sortedExtras(haveByURI, seen) {
		out = append(out, fmt.Errorf("entity %q no longer used by the release", uri))
	}
	return out
}

func collectSchemaLibraryDrift(have []SchemaLibrary, want []closure.ResolvedLibrary) []error {
	haveByURI := make(map[string]SchemaLibrary, len(have))
	for _, e := range have {
		haveByURI[e.URI] = e
	}
	seen := make(map[string]struct{}, len(want))
	var out []error
	for _, w := range want {
		seen[w.URI] = struct{}{}
		h, ok := haveByURI[w.URI]
		if !ok {
			out = append(out, fmt.Errorf("schema-library %q not in lockfile", w.URI))
			continue
		}
		if h.ResolvedVersion != w.ResolvedVersion || h.Digest != w.Digest {
			out = append(out, fmt.Errorf("schema-library %q changed: lockfile=(%s,%s) current=(%s,%s)",
				w.URI, h.ResolvedVersion, h.Digest, w.ResolvedVersion, w.Digest))
		}
	}
	for _, uri := range sortedExtras(haveByURI, seen) {
		out = append(out, fmt.Errorf("schema-library %q no longer used by the release", uri))
	}
	return out
}

func collectMappingLibraryDrift(have []MappingLibrary, want []closure.ResolvedLibrary) []error {
	haveByURI := make(map[string]MappingLibrary, len(have))
	for _, e := range have {
		haveByURI[e.URI] = e
	}
	seen := make(map[string]struct{}, len(want))
	var out []error
	for _, w := range want {
		seen[w.URI] = struct{}{}
		h, ok := haveByURI[w.URI]
		if !ok {
			out = append(out, fmt.Errorf("mapping-library %q not in lockfile", w.URI))
			continue
		}
		if h.ResolvedVersion != w.ResolvedVersion || h.Digest != w.Digest {
			out = append(out, fmt.Errorf("mapping-library %q changed: lockfile=(%s,%s) current=(%s,%s)",
				w.URI, h.ResolvedVersion, h.Digest, w.ResolvedVersion, w.Digest))
		}
	}
	for _, uri := range sortedExtras(haveByURI, seen) {
		out = append(out, fmt.Errorf("mapping-library %q no longer used by the release", uri))
	}
	return out
}

func collectWorkloadLibraryDrift(have []WorkloadLibrary, want []closure.ResolvedLibrary) []error {
	haveByURI := make(map[string]WorkloadLibrary, len(have))
	for _, e := range have {
		haveByURI[e.URI] = e
	}
	seen := make(map[string]struct{}, len(want))
	var out []error
	for _, w := range want {
		seen[w.URI] = struct{}{}
		h, ok := haveByURI[w.URI]
		if !ok {
			out = append(out, fmt.Errorf("workload-library %q not in lockfile", w.URI))
			continue
		}
		if h.ResolvedVersion != w.ResolvedVersion || h.Digest != w.Digest {
			out = append(out, fmt.Errorf("workload-library %q changed: lockfile=(%s,%s) current=(%s,%s)",
				w.URI, h.ResolvedVersion, h.Digest, w.ResolvedVersion, w.Digest))
		}
	}
	for _, uri := range sortedExtras(haveByURI, seen) {
		out = append(out, fmt.Errorf("workload-library %q no longer used by the release", uri))
	}
	return out
}

func collectPluginDrift(have []Plugin, want []closure.ResolvedRef) []error {
	haveByURI := make(map[string]Plugin, len(have))
	for _, e := range have {
		haveByURI[e.URI] = e
	}
	seen := make(map[string]struct{}, len(want))
	var out []error
	for _, w := range want {
		seen[w.URI] = struct{}{}
		h, ok := haveByURI[w.URI]
		if !ok {
			out = append(out, fmt.Errorf("wasm-plugin %q not in lockfile", w.URI))
			continue
		}
		if h.ResolvedVersion != w.ResolvedVersion || h.Digest != w.Digest {
			out = append(out, fmt.Errorf("wasm-plugin %q changed: lockfile=(%s,%s) current=(%s,%s)",
				w.URI, h.ResolvedVersion, h.Digest, w.ResolvedVersion, w.Digest))
		}
	}
	for _, uri := range sortedExtras(haveByURI, seen) {
		out = append(out, fmt.Errorf("wasm-plugin %q no longer used by the release", uri))
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
