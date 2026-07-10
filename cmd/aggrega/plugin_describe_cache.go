// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// describeCacheDigest is the cache key for a resolved plugin's describe()
// descriptor: the hex SHA-256 of the resolved guest bytes. The descriptor is a
// static function of the guest, so keying on the content digest makes a
// re-describe unnecessary while the bytes are unchanged and invalidates the
// entry the moment they change — a new digest is a new key, and the stale entry
// is simply never read again.
func describeCacheDigest(guest []byte) string {
	sum := sha256.Sum256(guest)
	return hex.EncodeToString(sum[:])
}

// readDescribeCache returns the cached describe() descriptor for digest and
// whether a usable entry was found. A missing directory, absent file, or
// unreadable or malformed entry returns false so the caller falls back to
// loading and describing the guest — the cache is an optimization, never a
// source of stale or partial output.
func readDescribeCache(digest string) (wasm.PluginDescriptor, bool) {
	dir, ok := wasm.PluginDescribeCacheDir()
	if !ok {
		return wasm.PluginDescriptor{}, false
	}
	raw, err := os.ReadFile(filepath.Join(dir, digest+".json"))
	if err != nil {
		return wasm.PluginDescriptor{}, false
	}
	var desc wasm.PluginDescriptor
	if err := json.Unmarshal(raw, &desc); err != nil {
		return wasm.PluginDescriptor{}, false
	}
	return desc, true
}

// writeDescribeCache stores desc under digest for later runs to re-read. It is
// best-effort: a cache directory that cannot be resolved or created, a marshal
// failure, or a write failure is silently ignored, since a failed cache write
// only forgoes the optimization and never affects the current listing.
func writeDescribeCache(digest string, desc wasm.PluginDescriptor) {
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
	_ = os.WriteFile(filepath.Join(dir, digest+".json"), data, 0o644)
}
