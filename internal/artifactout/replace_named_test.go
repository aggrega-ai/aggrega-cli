// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package artifactout

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReplaceNamedReplacesOnlyTheSetsOwnNames pins what a write into a directory
// a caller named may remove: a file of the set's naming that the new set does not
// write is gone, a file of another name and a subdirectory of the set's naming are
// left as they were, and the new set is written.
func TestReplaceNamedReplacesOnlyTheSetsOwnNames(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "out")
	owned := func(name string) bool { return strings.HasPrefix(name, "set.") }

	seed := map[string]string{
		"set.old.json":         "stale",
		"set.kept.json":        "previous",
		"notes.txt":            "the author's own",
		"set.subdir/inner.txt": "inside a directory of the set's naming",
	}
	for rel, body := range seed {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := ReplaceNamed(root, dir, map[string][]byte{"set.kept.json": []byte("current")}, owned); err != nil {
		t.Fatalf("ReplaceNamed: %v", err)
	}

	tests := []struct {
		rel  string
		want string // empty: the file must be absent
	}{
		{"set.old.json", ""},
		{"set.kept.json", "current"},
		{"notes.txt", "the author's own"},
		{"set.subdir/inner.txt", "inside a directory of the set's naming"},
	}
	for _, tc := range tests {
		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(tc.rel)))
		switch {
		case tc.want == "" && !os.IsNotExist(err):
			t.Errorf("%s: want it removed, read %q (err %v)", tc.rel, got, err)
		case tc.want != "" && (err != nil || string(got) != tc.want):
			t.Errorf("%s: want %q, read %q (err %v)", tc.rel, tc.want, got, err)
		}
	}
}

// TestReplaceNamedCreatesAMissingDirectory pins that the first write into a
// directory that does not exist yet creates it, an empty set included.
func TestReplaceNamedCreatesAMissingDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "new", "dir")
	if err := ReplaceNamed(root, dir, nil, func(string) bool { return true }); err != nil {
		t.Fatalf("ReplaceNamed: %v", err)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Fatalf("the directory was not created: %v", err)
	}
}
