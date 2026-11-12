// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package metadata_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/metadata"
)

// TestReadRefusesAVersionThatIsNotOnePathSegment drives a version that is not a
// single path segment through the reader. The version names the cache slot the
// metadata sits in, so a value carrying a separator or a parent step would name a
// location outside the cache. Every row starts from metadata the resolver writes
// and changes the version alone; the first row is the positive control, and it
// carries the full commit a git-repository source is pinned to. Each refusal must
// name the file and the key.
func TestReadRefusesAVersionThatIsNotOnePathSegment(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		version string
		refused bool
	}{
		{name: "the full commit a git-repository source is pinned to", version: "0123456789abcdef0123456789abcdef01234567"},
		{name: "a version that steps out of the cache", version: "../../../../x", refused: true},
		{name: "a version carrying a separator", version: "1.0.0/x", refused: true},
		{name: "a version carrying a backslash, a separator where the path is joined on Windows", version: `1.0.0\x`, refused: true},
		{name: "a bare parent step", version: "..", refused: true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "metadata.yaml")
			if err := metadata.Write(path, metadata.CacheMetadata{
				Kind:      "git-repository",
				URL:       "https://example.com/modules.git",
				Version:   tc.version,
				Digest:    "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789",
				Mediatype: "application/gzip",
				PulledAt:  time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC),
			}); err != nil {
				t.Fatalf("Write: %v", err)
			}
			got, err := metadata.Read(path)
			if !tc.refused {
				if err != nil {
					t.Fatalf("the reader refused metadata the resolver writes: %v", err)
				}
				if got.Version != tc.version {
					t.Errorf("the version read back as %q; want %q", got.Version, tc.version)
				}
				return
			}
			if err == nil {
				t.Fatalf("the reader admitted metadata with %s", tc.name)
			}
			if !errors.Is(err, metadata.ErrCacheMetadataInvalid) {
				t.Errorf("refused, but not as invalid cache metadata: %v", err)
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("the refusal does not name the file %s: %v", path, err)
			}
			if !strings.Contains(err.Error(), ": version: ") {
				t.Errorf("the refusal does not name the version key: %v", err)
			}
		})
	}
}
