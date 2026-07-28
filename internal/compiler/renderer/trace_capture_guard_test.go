// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// captureFile is the projection's one call site, relative to this package. The
// guard asserts an EXACT match against this one entry rather than "at least this
// one": a subset check would pass while a capture added later took its own route.
const captureFile = "phase_b_state.go"

// composedProjectionCall rebuilds the call text from fragments so this file can
// search for it without itself becoming a second call site. The seam is deliberate;
// concatenating here is what keeps the exact-match assertion below honest.
func composedProjectionCall() string { return "PillarSnapshot" + "(" }

// TestPillarSnapshotHasOneCallSite is the chokepoint guard for the display
// projection. It walks this package's non-test Go and requires the projection to be
// called from exactly one file, exactly once.
//
// The property it protects is not tidiness. The projection is handed a context
// whose non-key-addressable data has already been substituted — a rendered workload
// replaced by its display re-render — and the one call site is what builds that
// context. A second call site would receive the LIVE context and show a reader the
// document the build emitted where the display announced a re-render, which is the
// exact failure the announcement exists to prevent. It is also what stops a second
// parsed-document projection from acquiring its own parallel substitution path
// instead of routing through this one.
//
// Test files are out of scope: a unit test calls the projection directly on a
// constructed context on purpose, and requiring it to go through the state would
// blind it to the projection's own behaviour.
func TestPillarSnapshotHasOneCallSite(t *testing.T) {
	t.Parallel()
	needle := composedProjectionCall()
	counts := map[string]int{}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	scanned := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		scanned++
		body, readErr := os.ReadFile(filepath.Join(".", name))
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}
		if n := strings.Count(string(body), needle); n > 0 {
			counts[name] = n
		}
	}
	if scanned == 0 {
		t.Fatal("no non-test Go file was scanned; this guard is looking in the wrong place")
	}

	got := make([]string, 0, len(counts))
	for f := range counts {
		got = append(got, f)
	}
	sort.Strings(got)

	if len(got) != 1 || got[0] != captureFile {
		t.Fatalf("the display projection must be called from exactly one file.\n  want: [%s]\n  got:  %v\nEvery capture reaches it through PhaseBState.capture, which is what hands it an already-substituted context.", captureFile, got)
	}
	if n := counts[captureFile]; n != 1 {
		t.Errorf("%s must call the display projection exactly once; got %d calls", captureFile, n)
	}
}
