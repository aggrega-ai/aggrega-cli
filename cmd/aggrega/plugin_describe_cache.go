// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// readDescribeCache returns the cached describe() descriptor for the plugin
// pinned at contentPin and whether a usable entry was found. contentPin is the
// canonical content digest ("sha256:<hex>"); the on-disk entry is keyed by the
// colon-free tag form of that pin (oci.ContentDigestTag), applied here so a
// reader and a writer cannot disagree on the slot the caller addresses. The
// descriptor is a static function of the guest, so keying on the content digest
// makes a re-describe unnecessary while the bytes are unchanged and invalidates
// the entry the moment they change — a new digest is a new key, and the stale
// entry is simply never read again. A missing directory, absent file, or
// unreadable or malformed entry returns false so the caller falls back to
// loading and describing the guest — the cache is an optimization, never a
// source of stale or partial output.
func readDescribeCache(contentPin string) (wasm.PluginDescriptor, bool) {
	dir, ok := wasm.PluginDescribeCacheDir()
	if !ok {
		return wasm.PluginDescriptor{}, false
	}
	raw, err := os.ReadFile(filepath.Join(dir, oci.ContentDigestTag(contentPin)+".json"))
	if err != nil {
		return wasm.PluginDescriptor{}, false
	}
	var desc wasm.PluginDescriptor
	if err := json.Unmarshal(raw, &desc); err != nil {
		return wasm.PluginDescriptor{}, false
	}
	return desc, true
}

// writeDescribeCache stores desc for the plugin pinned at contentPin for later
// runs to re-read. contentPin is the canonical content digest ("sha256:<hex>");
// the entry is keyed by the colon-free tag form of that pin
// (oci.ContentDigestTag), the same transform readDescribeCache applies, so the
// write and read address one slot. It is best-effort: a cache directory that
// cannot be resolved or created, a marshal failure, or a write failure is
// silently ignored, since a failed cache write only forgoes the optimization and
// never affects the current listing.
func writeDescribeCache(contentPin string, desc wasm.PluginDescriptor) {
	dir, ok := wasm.PluginDescribeCacheDir()
	if !ok {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	data, err := json.Marshal(desc)
	if err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(dir, oci.ContentDigestTag(contentPin)+".json"), data, 0o644)
}
