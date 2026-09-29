// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/provindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
)

// provenanceAt points the provenance recording at path, with a fresh per-process
// memo, for the rest of the test.
func provenanceAt(t *testing.T, path string) {
	t.Helper()
	savedPath, savedUpsert, savedMemo := provenanceIndexPath, provenanceUpsert, provenanceRecorded
	provenanceIndexPath = func() (string, bool) { return path, true }
	provenanceUpsert = provindex.Upsert
	provenanceRecorded = &sync.Map{}
	t.Cleanup(func() {
		provenanceIndexPath, provenanceUpsert, provenanceRecorded = savedPath, savedUpsert, savedMemo
	})
}

// provenanceEntries returns the provenance index at path in a form two runs can be
// compared in: FirstSeen is the one field that records when a run happened, so it
// is cleared.
func provenanceEntries(t *testing.T, path string) string {
	t.Helper()
	ix, err := provindex.Load(path)
	if err != nil {
		t.Fatalf("load the provenance index at %s: %v", path, err)
	}
	entries := ix.Entries()
	for i := range entries {
		entries[i].FirstSeen = time.Time{}
	}
	return fmt.Sprintf("%+v", entries)
}

// TestProvenanceAfterConcurrentResolutionsEqualsSerial proves the provenance index
// records the same identities whether a set of plugin resolutions runs one at a
// time or all at once. The set mixes identities that share guest bytes, so they
// meet in one digest's entry, with identities of bytes of their own, and resolves
// each identity more than once. The concurrent run is where the index's
// read-modify-write could lose one identity's record to another's.
func TestProvenanceAfterConcurrentResolutionsEqualsSerial(t *testing.T) {
	var resolutions []pluginpath.ResolvedPlugin
	for i := 0; i < 12; i++ {
		resolutions = append(resolutions, pluginpath.ResolvedPlugin{
			URI:     fmt.Sprintf("oci://hub.aggrega.ai/plugins/test/guest-%d", i),
			Version: "1.0.0",
			Bytes:   []byte(fmt.Sprintf("guest-bytes-%d", i%4)),
		})
	}
	const repeats = 3

	serialPath := filepath.Join(t.TempDir(), "provenance-index.json")
	provenanceAt(t, serialPath)
	for n := 0; n < repeats; n++ {
		for _, r := range resolutions {
			_ = PluginSpec("guest", r)
		}
	}
	want := provenanceEntries(t, serialPath)

	concurrentPath := filepath.Join(t.TempDir(), "provenance-index.json")
	provenanceAt(t, concurrentPath)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for n := 0; n < repeats; n++ {
		for _, r := range resolutions {
			wg.Add(1)
			go func(r pluginpath.ResolvedPlugin) {
				defer wg.Done()
				<-start
				_ = PluginSpec("guest", r)
			}(r)
		}
	}
	close(start)
	wg.Wait()

	ix, err := provindex.Load(serialPath)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(ix.Entries()); n != 4 {
		t.Fatalf("the serial run recorded %d digests; want 4, or the comparison proves nothing", n)
	}
	if got := provenanceEntries(t, concurrentPath); got != want {
		t.Fatalf("the provenance index after concurrent resolutions differs from the serial run's\n got: %s\nwant: %s", got, want)
	}
}
