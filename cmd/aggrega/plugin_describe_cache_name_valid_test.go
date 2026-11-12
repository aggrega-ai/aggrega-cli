// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"os"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegmenttest"
)

// TestTheDescribeCacheEntryIsNamedSoEveryPlatformAcceptsIt writes a plugin's
// descriptor through the write installing a plugin performs, holds the entry it
// created to the one validity predicate — its name is the plugin's content pin,
// which carries a colon — and reads it back through the read a later run
// performs, so the two address one entry.
func TestTheDescribeCacheEntryIsNamedSoEveryPlatformAcceptsIt(t *testing.T) {
	dir, ok := wasm.PluginDescribeCacheDir()
	if !ok {
		t.Skip("no plugin-describe cache directory resolves on this machine")
	}
	pin := digest.ContentDigest([]byte(t.Name()))
	entry := describeCacheEntry(dir, pin)
	t.Cleanup(func() { _ = os.Remove(entry) })

	writeDescribeCache(pin, wasm.PluginDescriptor{PluginType: string(wasm.PluginTypeCLI)})
	if _, err := os.Stat(entry); err != nil {
		t.Fatalf("the write created no entry at %s: %v", entry, err)
	}
	pathsegmenttest.RequireValidBeneath(t, dir, entry)
	if desc, ok := readDescribeCache(pin); !ok || desc.PluginType != string(wasm.PluginTypeCLI) {
		t.Fatalf("readDescribeCache(%q) = %+v, %v; the read must find the entry the write named", pin, desc, ok)
	}
}
