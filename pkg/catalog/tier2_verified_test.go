// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestTier2ReadVerified pins the verifying Tier-2 cache read: it mirrors the
// source-artifact cache read (pkg/sourcecache.LookupAndExtract) — read the slot,
// recompute the content digest, compare to the lockfile pin — so cache-content
// integrity lives at the cache boundary rather than in caller discipline. A
// build-time reader passing its lockfile pin cannot get back unverified bytes;
// a missing slot steers to `aggrega pull`; an empty pin is a programming error
// (there is no skip mode).
func TestTier2ReadVerified(t *testing.T) {
	t.Parallel()

	ws := t.TempDir()
	const cacheDir = ".aggrega/cache"
	const uri = "hub.aggrega.ai/schemalibrary/core/DbSizing:1.0.0"
	body := []byte("apiVersion: aggrega.ai/v1alpha1\n" +
		"kind: SchemaLibrary\nmetadata:\n  name: DbSizing\n  version: 1.0.0\n" +
		"spec:\n  schemas:\n    Storage: {}\n")

	if err := oci.WriteCached(ws, cacheDir, uri, "1.0.0", body); err != nil {
		t.Fatalf("WriteCached: %v", err)
	}
	pin := contentDigest(body)

	t.Run("digest match returns the bytes", func(t *testing.T) {
		got, err := tier2ReadVerified(ws, cacheDir, uri, pin)
		if err != nil {
			t.Fatalf("verified read (match): %v", err)
		}
		if string(got) != string(body) {
			t.Errorf("bytes = %q; want %q", got, body)
		}
	})

	t.Run("digest mismatch is ErrFetchIntegrity", func(t *testing.T) {
		_, err := tier2ReadVerified(ws, cacheDir, uri, "sha256:0000000000000000000000000000000000000000000000000000000000000000")
		if !errors.Is(err, oci.ErrFetchIntegrity) {
			t.Errorf("error = %v; want ErrFetchIntegrity", err)
		}
	})

	t.Run("missing cache slot is ErrFetchNotFound", func(t *testing.T) {
		_, err := tier2ReadVerified(ws, cacheDir, "hub.aggrega.ai/schemalibrary/core/Absent:1.0.0", pin)
		if !errors.Is(err, oci.ErrFetchNotFound) {
			t.Errorf("error = %v; want ErrFetchNotFound", err)
		}
	})

	t.Run("empty pin is a programming error, not a skip", func(t *testing.T) {
		if _, err := tier2ReadVerified(ws, cacheDir, uri, ""); err == nil {
			t.Error("empty expected digest must error (no skip mode); got nil")
		}
	})
}
