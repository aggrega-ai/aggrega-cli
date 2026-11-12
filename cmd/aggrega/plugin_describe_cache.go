// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegment"
)

// readDescribeCache returns the cached describe() descriptor for the plugin
// pinned at contentPin and whether a usable entry was found. contentPin is the
// canonical content digest ("sha256:<hex>"); the on-disk entry is the one
// describeCacheEntry names, so a reader and a writer cannot disagree on the slot
// the caller addresses. The descriptor is a static function of the guest, so
// keying on the content digest makes a re-describe unnecessary while the bytes
// are unchanged and invalidates the entry the moment they change — a new digest
// is a new key, and the stale entry is simply never read again. A missing
// directory, absent file, or unreadable or malformed entry returns false so the
// caller falls back to loading and describing the guest — the cache is an
// optimization, never a source of stale or partial output.
func readDescribeCache(contentPin string) (wasm.PluginDescriptor, bool) {
	dir, ok := wasm.PluginDescribeCacheDir()
	if !ok {
		return wasm.PluginDescriptor{}, false
	}
	raw, err := os.ReadFile(describeCacheEntry(dir, contentPin))
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
// runs to re-read, in the entry describeCacheEntry names — the one
// readDescribeCache reads. It is best-effort: a cache directory that cannot be
// resolved or created, a marshal failure, or a write failure is silently
// ignored, since a failed cache write only forgoes the optimization and never
// affects the current listing.
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
	_ = os.WriteFile(describeCacheEntry(dir, contentPin), data, 0o644)
}

// describeCacheEntry returns the file in the plugin-describe cache directory dir
// that holds the descriptor for the plugin pinned at contentPin: the pin spelled
// through the one spelling of a derived name (pathsegment.Encode) with a `.json`
// extension, so `sha256:<hex>` is the file `sha256+zx00003a<hex>.json`, a name
// every platform accepts whatever the pin a configuration records carries.
func describeCacheEntry(dir, contentPin string) string {
	return filepath.Join(dir, pathsegment.Encode(contentPin)+".json")
}
