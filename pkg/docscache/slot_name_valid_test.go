// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package docscache_test

import (
	"context"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegmenttest"
)

// TestEveryNameADocumentationBundleCacheProducesIsOneEveryPlatformAccepts caches a
// documentation bundle through the resolve a reader of documentation runs, under
// a reference whose host carries a port and whose path carries capitals, keeping
// the signature its pull verified, and holds every name beneath the
// documentation-bundle subtree — the slot, its files and the signature it keeps —
// to the one validity predicate; then reads a page back, so the read addresses the
// slot the write named.
func TestEveryNameADocumentationBundleCacheProducesIsOneEveryPlatformAccepts(t *testing.T) {
	t.Parallel()
	const ref = "oci://reg.example.com:5000/Docs/Aggrega-Docs"
	ctx := context.Background()
	store, pubPEM := signedBundleStore(t, buildBundle(t, map[string]string{componentPage: componentBody}))
	root := t.TempDir()
	if _, err := docscache.ResolveAndCache(ctx, store, ref, testVersion, root, keyedVerifier(store, pubPEM)); err != nil {
		t.Fatalf("ResolveAndCache: %v", err)
	}
	pathsegmenttest.RequireTreeValid(t, docscache.DocsBundlesRoot(root))
	if _, _, err := docscache.ReadPage(root, ref, testVersion, componentPage, admitAll); err != nil {
		t.Fatalf("ReadPage: %v", err)
	}
}
