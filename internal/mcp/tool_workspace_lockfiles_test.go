// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// TestTheReleaseListingNamesEachReturnedReleasesLockfile drives list-releases through the
// real tools/call dispatch and holds its lockfiles map to the records the answer carries.
//
// The rows are the three shapes of the answer: the whole listing in one page, a page from
// the middle of it, and the summary. The middle page is what separates a map describing
// the records this answer carries from one describing the whole workspace — the two agree
// on a single full page and on nothing else — and the summary must carry an empty object,
// because it carries no records.
//
// Each value is held against the compiler's rule applied to the returned path, in the same
// workspace-relative coordinate. The candidate walk lists only YAML-named Releases, where
// the rule and a derivation stripping any extension agree, so this table cannot tell those
// two apart; the map builder's own table in the serve package carries the name that does.
func TestTheReleaseListingNamesEachReturnedReleasesLockfile(t *testing.T) {
	root := t.TempDir()
	const release = "apiVersion: aggrega.ai/v1alpha1\nkind: Release\nmetadata:\n  name: probe\n  version: \"1.0.0\"\n"
	for _, rel := range []string{"app-release.yaml", "envs/prod.yaml", "staging.yml"} {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(release), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	s := &server{out: io.Discard, logw: io.Discard, startDir: root}

	rows := []struct {
		name         string
		args         map[string]any
		wantReturned []string
	}{
		{
			name:         "the whole listing in one page",
			args:         map[string]any{"response_format": "detailed"},
			wantReturned: []string{"app-release.yaml", "envs/prod.yaml", "staging.yml"},
		},
		{
			name:         "a page from the middle",
			args:         map[string]any{"response_format": "detailed", "limit": 1, "offset": 1},
			wantReturned: []string{"envs/prod.yaml"},
		},
		{
			name:         "the summary",
			args:         map[string]any{},
			wantReturned: []string{},
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			args := map[string]any{"workspace_root": root}
			for k, v := range row.args {
				args[k] = v
			}
			res := callDiscoveryTool(t, s, "list-releases", args)
			var got listReleasesResult
			decodeStructured(t, res, &got)

			if strings.Join(got.Releases, "\n") != strings.Join(row.wantReturned, "\n") {
				t.Fatalf("returned %v; want %v — the window is not the one this row is about", got.Releases, row.wantReturned)
			}
			if got.Lockfiles == nil {
				t.Fatal("lockfiles is absent or null; it is an object, empty when the answer carries no path")
			}

			var keys []string
			for key := range got.Lockfiles {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			want := append([]string(nil), got.Releases...)
			sort.Strings(want)
			if strings.Join(keys, "\n") != strings.Join(want, "\n") {
				t.Errorf("lockfiles keys %v; want exactly the paths this answer returned %v", keys, want)
			}

			for _, rel := range got.Releases {
				value := got.Lockfiles[rel]
				if filepath.IsAbs(value) {
					t.Errorf("lockfiles[%s] = %q is absolute; the listing reports every path relative to the root",
						rel, value)
				}
				if wantLock := lockfile.PathFor(rel); value != wantLock {
					t.Errorf("lockfiles[%s] = %q; the rule places it at %q", rel, value, wantLock)
				}
				if line := rel + " (lockfile " + value + ")"; !strings.Contains(res.Content[0].Text, line) {
					t.Errorf("the text rendering does not pair %s with its lockfile; want a line carrying %q in:\n%s",
						rel, line, res.Content[0].Text)
				}
			}
		})
	}
}
