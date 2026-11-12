// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package oci_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegmenttest"
)

// TestEveryNameTheEntityCacheWritesIsOneEveryPlatformAccepts drives the entity
// cache's write — the content blob and the manifest beside it — for the
// references and versions a cache is named after: a CLI plugin's reference at a
// content-digest version (what installing a plugin writes), a catalog entity's
// reference carrying its version (what a pull writes), a scheme-qualified
// reference whose host carries a port and capitals, and versions that are a
// device name or end in a full stop. Every directory and file name the write
// produced is held to the one validity predicate.
//
// A second assertion holds two references differing only in letter case to two
// slots that stay distinct where a file system ignores case.
func TestEveryNameTheEntityCacheWritesIsOneEveryPlatformAccepts(t *testing.T) {
	t.Parallel()
	const cacheDir = ".aggrega/cache"
	ws := t.TempDir()
	pin := "sha256:" + strings.Repeat("dc0b", 16)
	for _, slot := range []struct{ uri, version string }{
		{"hub.aggrega.ai/plugin/cli/moo:0.2.4", pin},
		{"oci://hub.aggrega.ai/plugin/cli/moo:0.2.4", pin},
		{"core/devtool/adminer:1.0.0", "1.0.0"},
		{"oci://Registry.Example:5000/Team/App", "1.0.0-RC.1+Build.5"},
		{"core/devtool/aux", "con"},
		{"core/devtool/aux", "v1."},
	} {
		if err := oci.WriteCached(ws, cacheDir, "Component", slot.uri, slot.version, []byte("content")); err != nil {
			t.Fatalf("WriteCached(%q, %q): %v", slot.uri, slot.version, err)
		}
		if err := oci.WriteCachedManifest(ws, cacheDir, "Component", slot.uri, slot.version, []byte("{}")); err != nil {
			t.Fatalf("WriteCachedManifest(%q, %q): %v", slot.uri, slot.version, err)
		}
		got, err := oci.ReadCached(ws, cacheDir, "Component", slot.uri, slot.version)
		if err != nil || string(got) != "content" {
			t.Fatalf("ReadCached(%q, %q) = %q, %v; the read must find the slot the write named", slot.uri, slot.version, got, err)
		}
	}
	judged := pathsegmenttest.RequireTreeValid(t, oci.EntitiesRoot(ws, cacheDir))
	t.Logf("judged %d names under the entity cache", judged)

	upper, err := oci.CachePath(ws, cacheDir, "Component", "core/devtool/App", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	lower, err := oci.CachePath(ws, cacheDir, "Component", "core/devtool/app", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if strings.EqualFold(upper, lower) {
		t.Errorf("references differing only in case share a slot under case folding: %s / %s", upper, lower)
	}
}

// TestEveryNameTheIndexAndSignaturePathsSpellIsOneEveryPlatformAccepts holds the
// two other cache paths this package spells — a registry's search index and a
// signature bundle — to the same predicate, over a host carrying a port, a
// repository carrying capitals and a colon, and a digest.
func TestEveryNameTheIndexAndSignaturePathsSpellIsOneEveryPlatformAccepts(t *testing.T) {
	t.Parallel()
	const ws, cacheDir = "/ws", ".aggrega/cache"
	pin := "sha256:" + strings.Repeat("ab", 32)
	for _, host := range []string{"localhost:5000", "hub.aggrega.ai", "oci://Registry.Example:5000"} {
		pathsegmenttest.RequireValidBeneath(t, oci.IndexRoot(ws, cacheDir), oci.IndexBasePath(ws, cacheDir, host)+".updated")
		for _, repo := range []string{"component/core/api", "Team/App:x", "con"} {
			pathsegmenttest.RequireValidBeneath(t, oci.IndexRoot(ws, cacheDir), oci.IndexRepoPath(ws, cacheDir, host, repo))
		}
	}
	pathsegmenttest.RequireValidBeneath(t, ws+"/"+cacheDir, oci.SignatureBundlePath(ws, cacheDir, pin))
}
