// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile

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
