// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
// generated_at records when the pull ran, so it differs between any two pulls
// by construction and is the one line permitted to differ here. Every other
// differing line fails the test and is named in the failure, so nothing else is
// normalized away.
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

		if err := pullRun(pullConfig{
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

	onlineLines := strings.Split(string(online), "\n")
	offlineLines := strings.Split(string(offline), "\n")

	// The tolerance below waives whole lines, so it is only as narrow as the
	// number of lines it can match. generated_at is a single column-0 key; if a
	// lockfile ever carried more than one, the waiver would silently widen.
	const stamp = "generated_at:"
	countStamps := func(lines []string) int {
		n := 0
		for _, l := range lines {
			if strings.HasPrefix(l, stamp) {
				n++
			}
		}
		return n
	}
	if n := countStamps(onlineLines); n != 1 {
		t.Fatalf("online lockfile has %d %s lines; want exactly 1", n, stamp)
	}
	if n := countStamps(offlineLines); n != 1 {
		t.Fatalf("offline lockfile has %d %s lines; want exactly 1", n, stamp)
	}

	if bytes.Equal(online, offline) {
		return
	}
	if len(onlineLines) != len(offlineLines) {
		t.Fatalf("offline lockfile has %d lines, online has %d — the two pulls pinned different content\n--- online ---\n%s\n--- offline ---\n%s",
			len(offlineLines), len(onlineLines), online, offline)
	}
	for i := range onlineLines {
		if onlineLines[i] == offlineLines[i] {
			continue
		}
		if strings.HasPrefix(onlineLines[i], stamp) && strings.HasPrefix(offlineLines[i], stamp) {
			continue
		}
		t.Errorf("line %d differs between an offline and an online pull of identical inputs:\n  online:  %s\n  offline: %s",
			i+1, onlineLines[i], offlineLines[i])
	}
}
