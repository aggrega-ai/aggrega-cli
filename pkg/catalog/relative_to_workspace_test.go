// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// TestRelativeToWorkspaceSpellsRelativePathsWithSlashes pins the coordinate
// system an IR node's source file is recorded in: a path relative to the
// workspace is spelled with forward slashes whatever the host's separator, and
// an absolute path that cannot be made relative is returned in the host's own
// spelling.
//
// Every input is composed with the host's own path join, so on a platform whose
// separator is a backslash the inputs carry backslashes and only the conversion
// stands between them and the IR. Where the separator is a slash the two
// spellings coincide and the relative rows pass either way.
func TestRelativeToWorkspaceSpellsRelativePathsWithSlashes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	absoluteElsewhere := filepath.Join(t.TempDir(), "elsewhere", "aggregate.yaml")
	cases := []struct {
		name string
		root string
		path string
		want string
	}{
		{
			name: "a file inside the workspace",
			root: root,
			path: filepath.Join(root, "catalog", "apps", "aggregate.yaml"),
			want: "catalog/apps/aggregate.yaml",
		},
		{
			name: "a file beside the workspace climbs out of it",
			root: root,
			path: filepath.Join(filepath.Dir(root), "shared", "aggregate.yaml"),
			want: "../shared/aggregate.yaml",
		},
		{
			name: "a path that is already relative cannot be decomposed and is spelled as it stands",
			root: root,
			path: filepath.Join("catalog", "aggregate.yaml"),
			want: "catalog/aggregate.yaml",
		},
		{
			name: "no workspace with a relative path",
			root: "",
			path: filepath.Join("catalog", "aggregate.yaml"),
			want: "catalog/aggregate.yaml",
		},
		{
			name: "no workspace with an absolute path keeps the host's spelling",
			root: "",
			path: absoluteElsewhere,
			want: absoluteElsewhere,
		},
		{
			name: "no path",
			root: root,
			path: "",
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := catalog.RelativeToWorkspace(tc.root, tc.path)
			if got != tc.want {
				t.Errorf("RelativeToWorkspace(%q, %q) = %q, want %q", tc.root, tc.path, got, tc.want)
			}
			if !filepath.IsAbs(got) && strings.Contains(got, `\`) {
				t.Errorf("RelativeToWorkspace(%q, %q) = %q: a relative answer carries a backslash", tc.root, tc.path, got)
			}
		})
	}
}
