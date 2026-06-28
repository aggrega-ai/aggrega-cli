// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package embedded is the compile-time home for Wasm plugins shipped inside
// the CLI binary. A plugin embedded here is version-locked to the binary and
// resolves with no filesystem, cache, or network access — the first tier the
// plugin-resolution cascade consults.
//
// The embedded set is empty today: no core provider is yet compiled to Wasm,
// so there is nothing to embed. The plumbing is real regardless — the embed
// directory is wired, the registry is populated from it at init, and the
// cascade checks this tier first — so a future embedded core provider is a
// drop-in: place its .wasm under plugins/ and register its canonical URI in
// register, with no change to the resolution cascade.
package embedded

import (
	"embed"
	"sort"
)

// pluginFS holds the embedded plugin artifacts. The `all:` prefix matches
// every file under plugins/ including the dot-prefixed .gitkeep placeholder
// that keeps the otherwise-empty directory present in the tree (a bare
// `//go:embed plugins` rejects a directory with no non-dot files). An embedded
// .wasm added under plugins/ is picked up here automatically; register binds it
// to a URI.
//
//go:embed all:plugins
var pluginFS embed.FS

// registry maps a canonical plugin URI to the embedded artifact's bytes. It is
// the lookup the cascade's embed tier consults. Empty today; a future embedded
// provider adds its entry in register.
var registry = map[string][]byte{}

// register binds embedded plugin URIs to their bytes read from pluginFS. It is
// the single place a future embedded core provider is wired: read its .wasm
// from pluginFS and assign it under its canonical URI. The body is empty today
// (no core provider is Wasm yet); the function exists so the wiring site is
// named and the embed tier is exercised end-to-end the moment an entry lands.
//
// A read failure for a registered artifact is a build-of-the-binary defect (the
// file is embedded at compile time), so register panics rather than returning
// an error — there is no recoverable runtime branch for a missing embedded
// file. With no entries the function never reads, so it never panics today.
func register() map[string][]byte {
	r := map[string][]byte{}
	// Future: r[canonicalURI] = mustRead("plugins/<name>.wasm")
	return r
}

func init() {
	registry = register()
}

// Lookup returns the embedded bytes for uri and whether the URI is embedded.
// A hit yields a defensive copy so a caller cannot mutate the embedded bytes
// backing every future resolution of the same URI.
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
// that removes it, restoring the prior state. It exists so a test in another
// package can prove the embed tier is consulted first even though no production
// plugin is embedded yet (the registry is empty today). It is test-only: the
// production wiring populates the registry through register at init, never
// through this seam.
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

// URIs returns the registered embedded plugin URIs in sorted order. Sorted so
// any diagnostic that enumerates the embedded set is deterministic
// (Deterministic Output invariant). Empty today.
func URIs() []string {
	uris := make([]string, 0, len(registry))
	for u := range registry {
		uris = append(uris, u)
	}
	sort.Strings(uris)
	return uris
}

// mustRead reads an embedded artifact or panics. Reserved for register's use
// when a future embedded plugin lands; a failure means the embed directive and
// the registered path disagree, which is a compile-of-the-binary defect.
func mustRead(name string) []byte {
	b, err := pluginFS.ReadFile(name)
	if err != nil {
		panic("wasm/embedded: read embedded plugin " + name + ": " + err.Error())
	}
	return b
}
