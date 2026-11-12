// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pluginstrust

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// TestLoadRefusesAVersionOrDigestThatIsNotOne drives one malformed value per
// constrained field class of the trust record through the loader. Every row starts
// from a record the resolution writes and changes one field; the first row is the
// positive control, and it carries a version in the form a build between release
// tags reports, which a pattern stricter than the writers would refuse.
//
// A version is held to a single path segment and a digest to the one spelling
// every comparison uses. Each refusal must name the file and the key.
func TestLoadRefusesAVersionOrDigestThatIsNotOne(t *testing.T) {
	t.Parallel()
	recorded := func() *Trust {
		return &Trust{
			Version: CurrentVersion,
			Plugins: []TrustedPlugin{{
				Plugin:          "governance/aggrega-security-baseline",
				ResolvedVersion: "0.7.3-68-g860f014b-dirty",
				Digest:          testDigest,
			}},
		}
	}
	cases := []struct {
		name   string
		mutate func(tr *Trust)
		key    string // empty: the row is the positive control
	}{
		{name: "a record the resolution writes", mutate: func(*Trust) {}},
		{
			name:   "a version that steps out of the cache",
			mutate: func(tr *Trust) { tr.Plugins[0].ResolvedVersion = "../../../../x" },
			key:    "plugins[0].resolved_version",
		},
		{
			name:   "a version carrying a separator",
			mutate: func(tr *Trust) { tr.Plugins[0].ResolvedVersion = "1.0.0/x" },
			key:    "plugins[0].resolved_version",
		},
		{
			name:   "a version carrying a backslash, a separator where the path is joined on Windows",
			mutate: func(tr *Trust) { tr.Plugins[0].ResolvedVersion = `1.0.0\x` },
			key:    "plugins[0].resolved_version",
		},
		{
			name:   "a digest shorter than a content digest",
			mutate: func(tr *Trust) { tr.Plugins[0].Digest = "sha256:abc" },
			key:    "plugins[0].digest",
		},
		{
			name:   "a digest that is a bare hex string",
			mutate: func(tr *Trust) { tr.Plugins[0].Digest = testSHA256 },
			key:    "plugins[0].digest",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tr := recorded()
			tc.mutate(tr)
			path := write(t, tr)
			got, err := Load(path)
			if tc.key == "" {
				if err != nil {
					t.Fatalf("the loader refused a record the resolution writes: %v", err)
				}
				if got.Plugins[0].ResolvedVersion != "0.7.3-68-g860f014b-dirty" {
					t.Errorf("the version read back as %q", got.Plugins[0].ResolvedVersion)
				}
				return
			}
			if err == nil {
				t.Fatalf("the loader admitted a record with %s: %+v", tc.name, got)
			}
			if !errors.Is(err, envelope.ErrEntityEnvelope()) {
				t.Errorf("refused, but not as a schema violation: %v", err)
			}
			if !strings.Contains(err.Error(), path) {
				t.Errorf("the refusal does not name the file %s: %v", path, err)
			}
			if !strings.Contains(err.Error(), tc.key) {
				t.Errorf("the refusal does not name the key %s: %v", tc.key, err)
			}
		})
	}
}
