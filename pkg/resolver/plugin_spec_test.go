// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/provindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
)

// provenanceProbe installs a scratch provenance index under the test's own temp
// directory and a counting write sink in front of the real Upsert, then resets
// the process-level record memo. It returns a pointer whose value is the number
// of times the underlying write actually ran, so a test can assert how often the
// memo let a record through. Everything is restored on cleanup, and the memo is
// reset again so a later test starts clean — the memo and the seam variables are
// package state shared across the whole binary.
func provenanceProbe(t *testing.T) (path string, writes *int) {
	t.Helper()

	path = filepath.Join(t.TempDir(), "provenance-index.json")
	var count int

	savedPath := provenanceIndexPath
	savedUpsert := provenanceUpsert
	savedMemo := provenanceRecorded

	provenanceIndexPath = func() (string, bool) { return path, true }
	provenanceUpsert = func(p, rawDigest string, id provindex.Identity, size int64, now time.Time) error {
		count++
		return provindex.Upsert(p, rawDigest, id, size, now)
	}
	provenanceRecorded = &sync.Map{}

	t.Cleanup(func() {
		provenanceIndexPath = savedPath
		provenanceUpsert = savedUpsert
		provenanceRecorded = savedMemo
	})

	return path, &count
}

// TestRecordProvenanceMemoizesIdentity proves the render-hot-path guard: many
// resolutions of the SAME {URI, version} record exactly once per process. Only
// the first crossing computes the digest and takes the index lock; every later
// crossing short-circuits before either.
func TestRecordProvenanceMemoizesIdentity(t *testing.T) {
	path, writes := provenanceProbe(t)

	resolved := pluginpath.ResolvedPlugin{
		URI:     "oci://hub.aggrega.ai/plugins/core/helm",
		Version: "1.0.0",
		Bytes:   []byte("guest-bytes"),
	}
	for i := 0; i < 5; i++ {
		recordProvenance(resolved)
	}

	if *writes != 1 {
		t.Fatalf("want exactly 1 index write for a repeated identity, got %d", *writes)
	}

	ix, err := provindex.Load(path)
	if err != nil {
		t.Fatalf("load index: %v", err)
	}
	entries := ix.Entries()
	if len(entries) != 1 {
		t.Fatalf("want 1 digest entry, got %d: %+v", len(entries), entries)
	}
	if len(entries[0].Identities) != 1 {
		t.Fatalf("want 1 recorded identity, got %d: %+v", len(entries[0].Identities), entries[0].Identities)
	}
}

// TestRecordProvenanceSharedDigestSurvivesMemo is the strange case under the
// memo: two DISTINCT URIs that resolve to byte-identical guests (one digest)
// must BOTH be recorded, because the memo keys on the full identity, not on the
// digest. The resulting single digest entry carries an identity set of two.
func TestRecordProvenanceSharedDigestSurvivesMemo(t *testing.T) {
	path, writes := provenanceProbe(t)

	shared := []byte("identical-guest-bytes")
	idA := provindex.Identity{URI: "oci://hub.aggrega.ai/plugins/core/helm", Version: "1.0.0"}
	idB := provindex.Identity{URI: "oci://hub.aggrega.ai/plugins/team/helm-fork", Version: "2.0.0"}

	// Record each identity twice; the memo collapses the repeats but must let the
	// two distinct identities through.
	recordProvenance(pluginpath.ResolvedPlugin{URI: idA.URI, Version: idA.Version, Bytes: shared})
	recordProvenance(pluginpath.ResolvedPlugin{URI: idA.URI, Version: idA.Version, Bytes: shared})
	recordProvenance(pluginpath.ResolvedPlugin{URI: idB.URI, Version: idB.Version, Bytes: shared})
	recordProvenance(pluginpath.ResolvedPlugin{URI: idB.URI, Version: idB.Version, Bytes: shared})

	if *writes != 2 {
		t.Fatalf("want 2 index writes (one per distinct identity), got %d", *writes)
	}

	ix, err := provindex.Load(path)
	if err != nil {
		t.Fatalf("load index: %v", err)
	}
	entries := ix.Entries()
	if len(entries) != 1 {
		t.Fatalf("want 1 shared-digest entry, got %d: %+v", len(entries), entries)
	}
	got := entries[0].Identities
	if len(got) != 2 {
		t.Fatalf("want identity set of 2 (shared by 2), got %d: %+v", len(got), got)
	}
	seen := map[provindex.Identity]bool{}
	for _, id := range got {
		seen[id] = true
	}
	if !seen[idA] || !seen[idB] {
		t.Fatalf("identity set dropped a source under the memo: %+v", got)
	}
}

// TestRecordProvenanceMemoKeysOnVersion proves the memo key includes the
// version: the same URI at two versions records twice, so a version bump is not
// masked by a prior record of the earlier version.
func TestRecordProvenanceMemoKeysOnVersion(t *testing.T) {
	_, writes := provenanceProbe(t)

	uri := "oci://hub.aggrega.ai/plugins/core/helm"
	recordProvenance(pluginpath.ResolvedPlugin{URI: uri, Version: "1.0.0", Bytes: []byte("v1")})
	recordProvenance(pluginpath.ResolvedPlugin{URI: uri, Version: "2.0.0", Bytes: []byte("v2")})
	recordProvenance(pluginpath.ResolvedPlugin{URI: uri, Version: "1.0.0", Bytes: []byte("v1")})

	if *writes != 2 {
		t.Fatalf("want 2 writes (one per {URI, version}), got %d", *writes)
	}
}

// TestPluginSpecFunnelsThroughMemo proves the memo sits on the one funnel every
// socket loads through: repeated PluginSpec calls for the same identity record
// once, so the render path pays the digest-and-lock cost at most once.
func TestPluginSpecFunnelsThroughMemo(t *testing.T) {
	_, writes := provenanceProbe(t)

	resolved := pluginpath.ResolvedPlugin{
		URI:     "oci://hub.aggrega.ai/plugins/core/kustomize",
		Version: "3.1.0",
		Bytes:   []byte("guest-bytes"),
	}
	for i := 0; i < 4; i++ {
		_ = PluginSpec("kustomize", resolved)
	}

	if *writes != 1 {
		t.Fatalf("want exactly 1 index write across repeated PluginSpec calls, got %d", *writes)
	}
}
