// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryops

import (
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegmenttest"
)

// TestEveryNameAnIndexWriteProducesIsOneEveryPlatformAccepts writes a host's
// search index through the writer a registry update uses, for a host carrying a
// port and repositories carrying capitals, a colon and a device name, and holds
// every directory and file name beneath the index subtree to the one validity
// predicate — the host's directory, each repository's record and the freshness
// file beside the directory.
func TestEveryNameAnIndexWriteProducesIsOneEveryPlatformAccepts(t *testing.T) {
	t.Parallel()
	const cacheDir = ".aggrega/cache"
	ws := t.TempDir()
	idx := HostIndex{
		Host: "localhost:5000",
		Records: []registryindex.Record{
			{URI: "oci://localhost:5000/component/core/api", Name: "component/core/api", Version: "1.0.0", Kind: "Component"},
			{URI: "oci://localhost:5000/Team/App:x", Name: "Team/App:x", Version: "1.0.0", Kind: "Component"},
			{URI: "oci://localhost:5000/con", Name: "con", Version: "1.0.0", Kind: "Component"},
		},
	}
	if err := WriteHostIndex(ws, cacheDir, idx, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)); err != nil {
		t.Fatalf("WriteHostIndex: %v", err)
	}
	if judged := pathsegmenttest.RequireTreeValid(t, oci.IndexRoot(ws, cacheDir)); judged != len(idx.Records)+2 {
		t.Errorf("judged %d names, want the host's directory, its freshness file and %d records", judged, len(idx.Records))
	}
}
