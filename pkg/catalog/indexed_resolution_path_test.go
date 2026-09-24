// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
)

// TestIndexedResolutionPathWithoutARecord pins the default the eager catalog and
// every other construction rely on: a catalog carrying no record of cache-folded
// entries answers every entry as catalog-resolved, which is what an index built
// from catalog directories alone is.
func TestIndexedResolutionPathWithoutARecord(t *testing.T) {
	t.Parallel()
	var c Catalog
	if got := c.indexedResolutionPath("Plugin", "pipeline-step/any:1.0.0"); got != closure.ResolutionPathCatalog {
		t.Errorf("nil record: got %q; want %q", got, closure.ResolutionPathCatalog)
	}
	c.fromCacheSlot = map[string]struct{}{entityBytesKey("Plugin", "pipeline-step/any:1.0.0"): {}}
	if got := c.indexedResolutionPath("Plugin", "pipeline-step/any:1.0.0"); got != closure.ResolutionPathCache {
		t.Errorf("recorded entry: got %q; want %q", got, closure.ResolutionPathCache)
	}
	if got := c.indexedResolutionPath("Recipe", "pipeline-step/any:1.0.0"); got != closure.ResolutionPathCatalog {
		t.Errorf("same key under another kind: got %q; want %q — the record is per kind", got, closure.ResolutionPathCatalog)
	}
}
