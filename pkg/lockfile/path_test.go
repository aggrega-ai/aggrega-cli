// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile

import "testing"

func TestPathFor(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		releasePath string
		want        string
	}{
		{"yaml extension", "/workspace/release.yaml", "/workspace/release.lock"},
		{"yml extension", "/workspace/release.yml", "/workspace/release.lock"},
		{"no extension", "/workspace/release", "/workspace/release.lock"},
		{"other extension", "/workspace/release.txt", "/workspace/release.txt.lock"},
		{"nested path", "/a/b/c/release.yaml", "/a/b/c/release.lock"},
		{"relative path", "release.yaml", "release.lock"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := PathFor(tc.releasePath)
			if got != tc.want {
				t.Errorf("PathFor(%q): got %q, want %q", tc.releasePath, got, tc.want)
			}
		})
	}
}
