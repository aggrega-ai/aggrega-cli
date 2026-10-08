// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package sourcecache_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegmenttest"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcecache"
)

// TestTheTreeALayoutWritesIsNamedSoEveryPlatformAcceptsIt lays an archive out the
// way a build reads a cached artifact, and holds the directory the layout named
// after the archive's content digest — and the extracted directory above it — to
// the one validity predicate. A digest carries a colon, so a tree named by the
// digest as written is the shape a platform refuses.
func TestTheTreeALayoutWritesIsNamedSoEveryPlatformAcceptsIt(t *testing.T) {
	t.Parallel()
	slot := t.TempDir()
	archive := buildTGZ(t, map[string]string{"chart/Chart.yaml": "name: chart\n"})
	tree, err := sourcecache.RegenerateExtracted("", "", slot, archive)
	if err != nil {
		t.Fatalf("RegenerateExtracted: %v", err)
	}
	if want := sourcecache.ExtractedTree(slot, digestOf(archive)); tree != want {
		t.Fatalf("laid out at %s, ExtractedTree names %s", tree, want)
	}
	pathsegmenttest.RequireValidBeneath(t, slot, tree)
}
