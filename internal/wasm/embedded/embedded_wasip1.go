// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build wasip1

// Package embedded is the compile-time home for Wasm plugins shipped inside
// the CLI binary. This is the guest-side (wasip1) build of the package: a Wasm
// guest is itself a plugin and never resolves plugins, so it carries no embed
// registry and no embedded artifacts. The registry is empty and Lookup always
// misses.
//
// Splitting the package this way keeps the host-only embed of the shipped
// artifacts out of guest binaries. Without it a guest that transitively imports
// the plugin-resolution path would embed a copy of every shipped artifact into
// itself and decompress it at startup.
package embedded

// registry is empty in a guest build: a guest resolves no plugins.
var registry = map[string][]byte{}

// Lookup mirrors the host signature so the plugin-resolution path compiles for
// wasip1. The guest registry is empty, so every lookup misses.
func Lookup(uri string) ([]byte, bool) {
	b, ok := registry[uri]
	if !ok {
		return nil, false
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out, true
}

// LookupEntity mirrors the host signature so any resolution path that reaches it
// compiles for wasip1. A guest embeds no entities, so it always misses.
func LookupEntity(uri string) ([]byte, bool) { return nil, false }

// EntityURIs mirrors the host signature so embedded-entity enumeration compiles
// for wasip1. A guest embeds no entities, so it returns none.
func EntityURIs() []string { return nil }

// ContentDigests mirrors the host signature so any retain caller that reaches it
// compiles for wasip1. A guest embeds no plugins, so it returns no digests.
func ContentDigests() []string { return nil }

// PublishedPlugins mirrors the host signature so any resolution path that reaches
// it compiles for wasip1. A guest resolves no plugins, so it returns none.
func PublishedPlugins() []PublishedPlugin { return nil }
