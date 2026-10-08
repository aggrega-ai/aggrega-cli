// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestAFailedCacheWriteSaysWhatItWasDoingAndWhere drives a cache write into each
// of the two operations it performs — creating the slot's directory, then writing
// the file in it — and makes each fail with something standing in the way rather
// than with a permission, so every row holds for the superuser and on every
// platform. The failure is said as what was being done, with its path once, and
// then the operating system's reason, which is read off the error the system
// returned because its words are the platform's. The name of the call that failed
// and the package's own name never reach the sentence.
func TestAFailedCacheWriteSaysWhatItWasDoingAndWhere(t *testing.T) {
	t.Parallel()
	const (
		cacheDir = ".aggrega/cache"
		uri      = "core/datastore/mariadb"
		version  = "1.0.0"
	)
	for _, row := range []struct {
		name string
		// block lays what stands in the way, given the slot's directory.
		block func(t *testing.T, slot string)
		// doing and at are the act the sentence opens with and the path it names.
		doing string
		at    func(slot string) string
	}{
		{
			name: "a regular file stands where the slot's directory goes",
			block: func(t *testing.T, slot string) {
				if err := os.MkdirAll(filepath.Dir(slot), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(slot, []byte("in the way\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			doing: "create the cache directory",
			at:    func(slot string) string { return slot },
		},
		{
			name: "a directory stands where the slot's file goes",
			block: func(t *testing.T, slot string) {
				if err := os.MkdirAll(filepath.Join(slot, "blob"), 0o755); err != nil {
					t.Fatal(err)
				}
			},
			doing: "write the cache file",
			at:    func(slot string) string { return filepath.Join(slot, "blob") },
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			slot, err := oci.CachePath(root, cacheDir, uri, version)
			if err != nil {
				t.Fatalf("CachePath refused a well-formed reference and version: %v", err)
			}
			row.block(t, slot)

			werr := oci.WriteCached(root, cacheDir, uri, version, []byte("body\n"))
			if werr == nil {
				t.Fatalf("the write succeeded with something standing in its way; the row proves nothing")
			}
			var pathErr *fs.PathError
			if !errors.As(werr, &pathErr) {
				t.Fatalf("the operating system's path error is no longer reachable through the failure: %v", werr)
			}
			want := row.doing + " " + row.at(slot) + ": " + pathErr.Err.Error()
			if got := werr.Error(); got != want {
				t.Errorf("the failed write said\n  %s\nwant\n  %s", got, want)
			}
		})
	}
}
