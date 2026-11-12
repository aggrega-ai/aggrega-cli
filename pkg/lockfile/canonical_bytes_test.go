// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package lockfile_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// TestCanonicalBytesPreservingTimestampIsWhatTheWriteWrites holds the reporting
// half of the lockfile write to the writing half, in both states the timestamp
// rule distinguishes: a first write, which keeps the lockfile's own provenance
// stamp, and a re-write of an unchanged closure, which reuses the stamp already
// on disk.
//
// The second state is the one worth asserting. A reporting function that simply
// marshalled the value handed to it would agree with the write on a first
// resolution and disagree on every later one — by exactly one line, in a file
// whose whole point is that it does not churn.
func TestCanonicalBytesPreservingTimestampIsWhatTheWriteWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "release.lock")
	lf := &lockfile.Lockfile{
		Version:     1,
		Release:     "release.yaml",
		GeneratedAt: "2026-01-01T00:00:00Z",
		Entities: []lockfile.Entity{
			{Kind: "component", URI: "server", ResolvedVersion: "1.0.0", Digest: "sha256:aaa", ReferencedFrom: []string{"manifest"}},
		},
	}

	// A first resolution: no file to take a stamp from.
	reported, err := lockfile.CanonicalBytesPreservingTimestamp(path, lf)
	if err != nil {
		t.Fatalf("CanonicalBytesPreservingTimestamp: %v", err)
	}
	if err := lockfile.WriteLockfilePreservingTimestamp(path, lf); err != nil {
		t.Fatalf("WriteLockfilePreservingTimestamp: %v", err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read the written lockfile: %v", err)
	}
	if !bytes.Equal(reported, written) {
		t.Fatalf("the reported bytes are not the written bytes on a first resolution;\nreported:\n%s\nwritten:\n%s", reported, written)
	}

	// A later resolution of the same closure, carrying a different stamp. The
	// write reuses the stamp on disk, and so must the report.
	later := *lf
	later.GeneratedAt = "2026-06-30T12:00:00Z"
	reportedAgain, err := lockfile.CanonicalBytesPreservingTimestamp(path, &later)
	if err != nil {
		t.Fatalf("CanonicalBytesPreservingTimestamp on a re-resolution: %v", err)
	}
	if !bytes.Equal(reportedAgain, written) {
		t.Errorf("a report over an unchanged closure must reuse the stamp on disk, as the write does;\nreported:\n%s\non disk:\n%s", reportedAgain, written)
	}
	// The report describes the lockfile; it must not edit the value it describes.
	if later.GeneratedAt != "2026-06-30T12:00:00Z" {
		t.Errorf("the report rewrote the caller's provenance stamp to %q", later.GeneratedAt)
	}

	// And the file it read is still the file it read.
	if got, rerr := os.ReadFile(path); rerr != nil || !bytes.Equal(got, written) {
		t.Errorf("the report changed the lockfile on disk (read err %v);\nbefore:\n%s\nafter:\n%s", rerr, written, got)
	}
}
