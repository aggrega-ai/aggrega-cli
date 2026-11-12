// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// TestPullOfflineLockfileMatchesOnlineByteForByte proves that an offline
// workspace is a refusal to touch the network, not a reduced result: against a
// catalog that satisfies every reference locally, `aggrega pull` with
// `offline: true` writes a lockfile byte-identical to the one the same inputs
// produce with `offline: false`. Offline resolution is complete or it halts;
// there is no third outcome in which it quietly pins less.
//
// The comparison is over the bytes each pull actually wrote. Comparing the
// parsed lockfiles instead would pass while the serialization diverged, and the
// serialized bytes are the artifact a reviewer diffs and a deployment pipeline
// consumes.
//
// Two sections describe the pull rather than the closure and are the only
// content permitted to differ here: generated_at, which records when the pull ran
// and so differs between any two pulls by construction, and the toolchain stamp,
// which records the compiler and core-plugin versions that produced the file.
// Both are waived through lockfile.ParityWaived, the one home for that tolerance,
// so this comparison and every other lockfile comparison waive the same set
// rather than each maintaining its own list. Every other differing line fails the
// test and is named in the failure, so nothing else is normalized away.
func TestPullOfflineLockfileMatchesOnlineByteForByte(t *testing.T) {
	pull := func(configBody string) []byte {
		t.Helper()
		// Each pull gets its own root, so the second resolves from the same
		// authored inputs rather than reconciling against the first one's
		// lockfile. The lockfile records the Release's base name, not its
		// directory, so two roots stay comparable.
		root := t.TempDir()
		t.Chdir(root)
		releasePath := authoringFixture(t, root)
		writeWorkspaceConfig(t, root, configBody)

		if err := pullRun(context.Background(), pullConfig{
			ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
			ReleasePath: releasePath,
			CatalogDirs: []string{filepath.Join(root, "catalog")},
		}); err != nil {
			t.Fatalf("pullRun with %q: %v", strings.TrimSpace(configBody), err)
		}
		data, err := os.ReadFile(filepath.Join(root, "release.lock"))
		if err != nil {
			t.Fatalf("read lockfile written with %q: %v", strings.TrimSpace(configBody), err)
		}
		return data
	}

	online := pull("offline: false\n")
	// The offline pull reaching this line at all is half the property: it
	// resolved the whole closure with no registry tier available, rather than
	// halting or skipping an entry.
	offline := pull("offline: true\n")

	// The waiver removes a whole section per waived key, so it is only as narrow
	// as the number of sections it can match: a lockfile carrying a waived key
	// twice at column 0 would have both removed and the tolerance would widen
	// with nothing failing. A pull writes each waived section exactly once, so
	// asserting that count first is what keeps the widening loud. This runs
	// against every declared waived key rather than a key named here, so a section
	// added to the waiver is covered without this test being remembered.
	for _, key := range lockfile.ParityWaivedSections() {
		if n := lockfile.ParityWaivedSectionCount(online, key); n != 1 {
			t.Fatalf("online lockfile opens the waived section %q %d times; want exactly 1, "+
				"otherwise the parity tolerance is wider than it reads", key, n)
		}
		if n := lockfile.ParityWaivedSectionCount(offline, key); n != 1 {
			t.Fatalf("offline lockfile opens the waived section %q %d times; want exactly 1, "+
				"otherwise the parity tolerance is wider than it reads", key, n)
		}
	}

	onlineCompared := lockfile.ParityWaived(online)
	offlineCompared := lockfile.ParityWaived(offline)
	if bytes.Equal(onlineCompared, offlineCompared) {
		return
	}

	onlineLines := strings.Split(string(onlineCompared), "\n")
	offlineLines := strings.Split(string(offlineCompared), "\n")
	if len(onlineLines) != len(offlineLines) {
		t.Fatalf("offline lockfile has %d compared lines, online has %d — the two pulls pinned different content (waived: %s)\n--- online ---\n%s\n--- offline ---\n%s",
			len(offlineLines), len(onlineLines), strings.Join(lockfile.ParityWaivedSections(), ", "), online, offline)
	}
	for i := range onlineLines {
		if onlineLines[i] == offlineLines[i] {
			continue
		}
		t.Errorf("line %d differs between an offline and an online pull of identical inputs (waived sections already removed):\n  online:  %s\n  offline: %s",
			i+1, onlineLines[i], offlineLines[i])
	}
}
