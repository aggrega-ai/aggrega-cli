// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1 && light

// Package embedded is the compile-time home for Wasm plugins shipped inside
// the CLI binary. This is the light host build of the package: it embeds no
// artifacts, so the binary carries no plugin bytes and stays small. Every
// plugin resolves instead through the later tiers of the plugin-resolution
// cascade (colocated, local, workspace cache, registry), pinned by content
// digest to the lockfile. The registry is empty and Lookup always misses,
// which makes the cascade's embed tier a clean no-op for a light binary.
//
// Splitting the package this way keeps the shipped artifacts out of the light
// binary while the default build still embeds them. The default build's
// registry, decompression, and diagnostic surface live in the sibling file
// gated to the non-light host build.
package embedded

// registry is empty in a light build: no plugin bytes are embedded.
var registry = map[string][]byte{}

// Lookup mirrors the default-host signature so the plugin-resolution path
// compiles for a light build. The registry is empty, so every lookup misses
// and resolution continues through the later cascade tiers.
func Lookup(uri string) ([]byte, bool) {
	b, ok := registry[uri]
	if !ok {
		return nil, false
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out, true
}

// SeedForTest registers uri→bytes in the embedded set and returns a function
// that removes it, restoring the prior state. It mirrors the default-host seam
// so plugin-resolution tests compile and run under a light build; a test can
// inject an isolated URI→bytes pair against the otherwise-empty light registry.
// It is test-only: the light binary embeds nothing, so production resolution
// never populates the registry through this seam.
func SeedForTest(uri string, b []byte) (restore func()) {
	prev, had := registry[uri]
	registry[uri] = b
	return func() {
		if had {
			registry[uri] = prev
			return
		}
		delete(registry, uri)
	}
}

// URIs mirrors the default-host signature so plugin enumeration compiles for a
// light build. A light binary embeds no plugins, so it returns no built-in
// references — `plugin list` sees only configured plugins.
func URIs() []string { return nil }

// LookupDescriptor mirrors the default-host signature so it compiles for a light
// build. A light binary embeds no descriptors, so it always misses — no embedded
// entry is ever enumerated to look one up for.
func LookupDescriptor(uri string) ([]byte, bool) { return nil, false }

// LookupEntity mirrors the default-host signature so provider resolution
// compiles for a light build. A light binary embeds no entities, so it always
// misses and resolution continues through the catalog/registry tiers.
func LookupEntity(uri string) ([]byte, bool) { return nil, false }

// EntityURIs mirrors the default-host signature so embedded-entity enumeration
// compiles for a light build. A light binary embeds no entities, so it returns
// none.
func EntityURIs() []string { return nil }

// ContentDigests mirrors the default-host signature so an embedded-set retain
// caller compiles for a light build. A light binary embeds no plugins, so it
// returns no digests.
func ContentDigests() []string { return nil }

// PublishedPlugins mirrors the default-host signature so a caller selecting a
// core plugin the binary does not embed compiles for a light build. A light
// binary carries no entities at all, so it returns none and such a plugin is
// selected only through the workspace's catalog.
func PublishedPlugins() []PublishedPlugin { return nil }
