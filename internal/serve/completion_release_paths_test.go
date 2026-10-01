// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestReleasePathCompletionsAreSlashSeparated pins the spelling of the paths a
// Release's `spec.aggregate:` and `spec.manifest:` completions offer: relative
// to the workspace root, without the file extension, and separated by forward
// slashes whatever the host's separator.
//
// A chosen completion is written into the Release, which travels to other
// machines, so a path spelled with the host's backslash would name a file that a
// platform reading the backslash as part of the name cannot find. The entities
// sit two directories deep so the offered path has separators to spell. On a
// platform whose separator is a slash the two spellings coincide, so this passes
// there either way; it fails on one whose separator is a backslash as soon as the
// path is no longer converted.
func TestReleasePathCompletionsAreSlashSeparated(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	files := map[string]string{
		filepath.Join(root, "catalog", "apps", "shop.yaml"): "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\n" +
			"metadata:\n  name: shop\n",
		filepath.Join(root, "releases", "manifests", "prod.yml"): "apiVersion: aggrega.ai/v1alpha1\nkind: Manifest\n" +
			"metadata:\n  name: prod\n",
	}
	for p, body := range files {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	s := &Server{workspaceRoot: root}

	cases := []struct {
		kind string
		want []string
	}{
		{kind: "Aggregate", want: []string{"catalog/apps/shop"}},
		{kind: "Manifest", want: []string{"releases/manifests/prod"}},
	}
	for _, tc := range cases {
		got := s.enumerateWorkspaceRelativePaths(context.Background(), tc.kind)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s completions = %q, want %q", tc.kind, got, tc.want)
		}
		for _, p := range got {
			if strings.Contains(p, `\`) {
				t.Errorf("%s completion %q carries a backslash; a path written into a Release is slash-separated", tc.kind, p)
			}
		}
	}
}
