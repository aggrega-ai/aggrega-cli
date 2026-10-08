// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pathscope_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
)

// TestSamePlace holds SamePlace to the file system's own answer: one place
// however it is spelled, two places however alike their spellings, and no place
// at all for a path that is not there.
func TestSamePlace(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, d := range []string{"a", "b", filepath.Join("a", "sub")} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "a", "file.yaml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	linked := os.Symlink(filepath.Join(root, "a"), filepath.Join(root, "a-link")) == nil

	for _, tc := range []struct {
		name     string
		a, b     string // relative to root
		want     bool
		needLink bool
	}{
		{name: "one directory spelled twice alike", a: "a", b: "a", want: true},
		{name: "a trailing separator", a: "a", b: "a" + string(filepath.Separator), want: true},
		{name: "an interior climb back to it", a: "a", b: filepath.Join("b", "..", "a"), want: true},
		{name: "a link and the directory it leads to", a: "a-link", b: "a", want: true, needLink: true},
		{name: "one file", a: filepath.Join("a", "file.yaml"), b: filepath.Join("a-link", "file.yaml"), want: true, needLink: true},
		{name: "two directories", a: "a", b: "b", want: false},
		{name: "a directory and one beneath it", a: "a", b: filepath.Join("a", "sub"), want: false},
		{name: "a path that is not there, even against itself", a: "missing", b: "missing", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.needLink && !linked {
				t.Skip("this file system cannot hold a symbolic link")
			}
			// The join cleans away a trailing separator, so a row spelling one keeps it.
			b := filepath.Join(root, tc.b)
			if tc.b != "" && os.IsPathSeparator(tc.b[len(tc.b)-1]) {
				b += string(filepath.Separator)
			}
			if got := pathscope.SamePlace(filepath.Join(root, tc.a), b); got != tc.want {
				t.Errorf("SamePlace(%s, %s) = %v; want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
