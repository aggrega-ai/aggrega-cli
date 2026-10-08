// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build unix || windows

package wasm

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegmenttest"
)

// TestTheCompileLockIsNamedSoEveryPlatformAcceptsIt takes a guest's compile lock
// through the lock a load takes, for a content digest and for keys a digest's
// spelling does not constrain — capitals, a separator, a colon, a device name —
// and holds every name the locks created to the one validity predicate. Each
// lock keeps the shape the compiled-module listing excludes.
func TestTheCompileLockIsNamedSoEveryPlatformAcceptsIt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, key := range []string{
		digest.ContentDigest([]byte("a-guest")),
		"sha256:ABCDEF",
		"a/../b:c",
		"con",
	} {
		release := flockGuestCompile(dir, key)
		release()
		if name := guestCompileLockName(key); isCompileCacheLeaf(name) {
			t.Errorf("the lock %q for key %q is counted as a compiled module", name, key)
		}
	}
	if judged := pathsegmenttest.RequireTreeValid(t, dir); judged != 4 {
		t.Errorf("judged %d names, want one lock per key", judged)
	}
}
