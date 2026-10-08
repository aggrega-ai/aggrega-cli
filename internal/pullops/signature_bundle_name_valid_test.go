// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegmenttest"
)

// TestTheSignatureBundleAPullCachesIsNamedSoEveryPlatformAcceptsIt caches a
// verified signature bundle through the write a pull uses, and holds every name
// beneath the cache directory to the one validity predicate. The bundle is named
// after its digest, which carries a colon.
func TestTheSignatureBundleAPullCachesIsNamedSoEveryPlatformAcceptsIt(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	bundle := []byte(`{"mediaType":"application/vnd.dev.sigstore.bundle.v0.3+json"}`)
	if err := cacheBundleBlob(ws, bundleCacheDir, digest.ContentDigest(bundle), bundle); err != nil {
		t.Fatalf("cacheBundleBlob: %v", err)
	}
	if judged := pathsegmenttest.RequireTreeValid(t, filepath.Join(ws, bundleCacheDir)); judged != 2 {
		t.Errorf("judged %d names, want the signatures directory and the bundle", judged)
	}
}
