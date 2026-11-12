// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package recipepin_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// pinnedSources are the pins an entity author's pin step records, one per pin
// class, in the spellings the step writes: a git-repository source pinned to the
// full commit, a chart source pinned to its version with the manifest digest it
// was fetched against, and a content digest on every digest field. The writer
// orders the source rows by kind, so the git row is the first one in the file.
func pinnedSources() recipepin.Pins {
	return recipepin.Pins{
		SourceDigests: []types.SourceDigest{
			{
				Kind:    "git-repository",
				URL:     "https://example.com/modules.git",
				Version: "0123456789abcdef0123456789abcdef01234567",
				Digest:  digest.ContentDigest([]byte("git-archive")),
			},
			{
				Kind:           "helm-oci-chart",
				URL:            "oci://registry.example.com/charts/api",
				Version:        "1.0.0",
				Digest:         digest.ContentDigest([]byte("chart-archive")),
				ManifestDigest: digest.ContentDigest([]byte("chart-manifest")),
			},
		},
		ArtifactDigests:    []types.ArtifactDigest{{Name: "extra.yaml", Digest: digest.ContentDigest([]byte("extra"))}},
		LocalSourceDigests: []types.LocalSourceDigest{{Path: "chart/Chart.yaml", Digest: digest.ContentDigest([]byte("chart-file"))}},
	}
}

// TestReadLockRefusesAKindVersionOrDigestThatIsNotOne drives one malformed value
// per constrained field class of the entity lock through the reader. Every row
// starts from the pins the pin step writes and changes one field, and the first
// row is the positive control showing the unchanged pins read back.
//
// The source kind is held to the set of source kinds the lockfile and the cache
// records carry, because a build reads the kind to choose how to read the source.
// A version is held to a single path segment because it names the cache slot the
// build reads the source back from. A digest is held to the one spelling every
// comparison uses. Each refusal must name the lock file and the key.
func TestReadLockRefusesAKindVersionOrDigestThatIsNotOne(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		mutate func(p *recipepin.Pins)
		key    string // empty: the row is the positive control
	}{
		{name: "the pins the pin step writes", mutate: func(*recipepin.Pins) {}},
		{
			name:   "a source kind outside the set",
			mutate: func(p *recipepin.Pins) { p.SourceDigests[1].Kind = "helm-https-charts" },
			key:    "sourceDigests[1].kind",
		},
		{
			name:   "a source version that steps out of the cache",
			mutate: func(p *recipepin.Pins) { p.SourceDigests[1].Version = "../../../../x" },
			key:    "sourceDigests[1].version",
		},
		{
			name:   "a source version carrying a backslash, a separator where the path is joined on Windows",
			mutate: func(p *recipepin.Pins) { p.SourceDigests[0].Version = `1.0.0\x` },
			key:    "sourceDigests[0].version",
		},
		{
			name:   "a source version that is a bare parent step",
			mutate: func(p *recipepin.Pins) { p.SourceDigests[1].Version = ".." },
			key:    "sourceDigests[1].version",
		},
		{
			name:   "a source digest shorter than a content digest",
			mutate: func(p *recipepin.Pins) { p.SourceDigests[1].Digest = "sha256:abc" },
			key:    "sourceDigests[1].digest",
		},
		{
			name: "a manifest digest in uppercase hex",
			mutate: func(p *recipepin.Pins) {
				p.SourceDigests[1].ManifestDigest = strings.ToUpper(p.SourceDigests[1].ManifestDigest)
			},
			key: "sourceDigests[1].manifestDigest",
		},
		{
			name:   "an imported-file digest that is not a content digest",
			mutate: func(p *recipepin.Pins) { p.ArtifactDigests[0].Digest = "sha256:../../x" },
			key:    "artifactDigests[0].digest",
		},
		{
			name:   "a local-source file digest that is not a content digest",
			mutate: func(p *recipepin.Pins) { p.LocalSourceDigests[0].Digest = "md5:0123456789abcdef" },
			key:    "localSourceDigests[0].digest",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			entity := writeRecipe(t, bareRecipe)
			pins := pinnedSources()
			tc.mutate(&pins)
			if _, err := recipepin.WriteLock(entity, pins); err != nil {
				t.Fatalf("WriteLock: %v", err)
			}
			lock, present, err := recipepin.ReadLock(entity)
			if tc.key == "" {
				if err != nil || !present {
					t.Fatalf("the reader refused the pins the pin step writes: present=%v err=%v", present, err)
				}
				if got := lock.SourceDigests[0].Version; got != "0123456789abcdef0123456789abcdef01234567" {
					t.Errorf("the git-repository pin read back as %q; want the full commit", got)
				}
				return
			}
			if err == nil {
				t.Fatalf("the reader admitted a lock with %s: %+v", tc.name, lock)
			}
			if !errors.Is(err, envelope.ErrEntityEnvelope()) {
				t.Errorf("refused, but not as a schema violation: %v", err)
			}
			if path := recipepin.LockPath(entity); !strings.Contains(err.Error(), path) {
				t.Errorf("the refusal does not name the lock file %s: %v", path, err)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Errorf("the refusal does not name the key %s: %v", tc.key, err)
			}
		})
	}
}
