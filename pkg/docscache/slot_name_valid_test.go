// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package docscache_test

import (
	"context"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegmenttest"
)

// TestEveryNameADocumentationBundleCacheProducesIsOneEveryPlatformAccepts caches a
// documentation bundle through the resolve a reader of documentation runs, under
// a reference whose host carries a port and whose path carries capitals, and
// holds every name beneath the documentation-bundle subtree to the one validity
// predicate — then reads a page back, so the read addresses the slot the write
// named.
func TestEveryNameADocumentationBundleCacheProducesIsOneEveryPlatformAccepts(t *testing.T) {
	t.Parallel()
	const cacheDir = ".aggrega/cache"
	const ref = "oci://reg.example.com:5000/Docs/Aggrega-Docs"
	ctx := context.Background()
	store := memory.New()
	if _, err := oci.Push(ctx, store, testVersion, oci.MediaTypeDocs,
		buildBundle(t, map[string]string{componentPage: componentBody}), nil); err != nil {
		t.Fatalf("Push: %v", err)
	}
	ws := t.TempDir()
	if _, err := docscache.ResolveAndCache(ctx, store, ref, testVersion, ws, cacheDir, nil); err != nil {
		t.Fatalf("ResolveAndCache: %v", err)
	}
	pathsegmenttest.RequireTreeValid(t, docscache.DocsBundlesRoot(ws, cacheDir))
	if _, _, err := docscache.ReadPage(ws, cacheDir, ref, testVersion, componentPage); err != nil {
		t.Fatalf("ReadPage: %v", err)
	}
}
