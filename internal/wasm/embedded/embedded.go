// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1 && !light

// Package embedded is the compile-time home for Wasm plugins shipped inside
// the CLI binary. A plugin embedded here is version-locked to the binary and
// resolves with no filesystem, cache, or network access — the first tier the
// plugin-resolution cascade consults.
//
// The helm and compose provider rendering plugins are embedded here. Each
// artifact is committed gzip-compressed under plugins/ (the uncompressed Wasm is
// large) and decompressed once at init into the registry, keyed by the
// artifact's canonical content-addressed URI. Adding another embedded plugin is
// a drop-in: place its gzip-compressed .wasm under plugins/ and register its
// canonical URI in register.
package embedded

import (
	"bytes"
	"compress/gzip"
	"embed"
	"io"
	"sort"
)

// The embedded provider guests are reproducible build artifacts, not tracked
// files: `go generate` rebuilds each gzip-compressed .wasm.gz under plugins/
// from its guest source via the build-embedded scripts. The scripts pin a
// reproducible recipe (GOWORK=off, -trimpath, -ldflags=-buildid=,
// -buildvcs=false, gzip -n) so the uncompressed digest is stable across
// rebuilds and matches the content-addressed URIs registered below.
//
//go:generate bash ../../../scripts/build-embedded-helm-provider.sh
//go:generate bash ../../../scripts/build-embedded-compose-provider.sh
//go:generate bash ../../../scripts/build-embedded-helm-chart-provider.sh
//go:generate bash ../../../scripts/build-embedded-terraform-provider.sh

// pluginFS holds the embedded plugin artifacts. The `all:` prefix matches
// every file under plugins/ including the dot-prefixed .gitkeep placeholder
// that keeps the directory present in the tree even when no artifact is checked
// out (a bare `//go:embed plugins` rejects a directory with no non-dot files).
// An embedded artifact added under plugins/ is picked up here automatically;
// register binds it to a URI.
//
//go:embed all:plugins
var pluginFS embed.FS

// helmProviderURI is the canonical content-addressed URI the helm provider
// rendering plugin's decompressed bytes register under. It is the artifact
// reference a Plugin entity's spec.plugin names; the sha256 digest pins the
// exact bytes committed (uncompressed) under plugins/, so a resolution of this
// URI through the embed tier verifies byte-for-byte against a lockfile pin.
const helmProviderURI = "oci://hub.aggrega.ai/plugins/helm-provider@sha256:0c5528aecec1a13fa95a0b273051d8d33e68730a316d1fac7ebc5b3da7567fe7"

// composeProviderURI is the canonical content-addressed URI the compose provider
// rendering plugin's decompressed bytes register under. Like helmProviderURI, the
// sha256 digest pins the exact bytes committed (uncompressed) under plugins/, so a
// resolution of this URI through the embed tier verifies byte-for-byte against a
// lockfile pin.
const composeProviderURI = "oci://hub.aggrega.ai/plugins/compose-provider@sha256:79f80c762604bba96dbc2edc25fcb49ff497357c58c060f8ca5205dddeba75ce"

// helmChartProviderURI is the canonical content-addressed URI the helm-chart
// provider rendering plugin's decompressed bytes register under. Like the other
// provider URIs, the sha256 digest pins the exact bytes committed (uncompressed)
// under plugins/, so a resolution of this URI through the embed tier verifies
// byte-for-byte against a lockfile pin.
const helmChartProviderURI = "oci://hub.aggrega.ai/plugins/helmchart-provider@sha256:3250b9b11f42242d73207e92e5e63b607a254334cd61c722255a46f44ce15630"

// terraformProviderURI is the canonical content-addressed URI the terraform
// provider rendering plugin's decompressed bytes register under. Like the other
// provider URIs, the sha256 digest pins the exact bytes committed (uncompressed)
// under plugins/, so a resolution of this URI through the embed tier verifies
// byte-for-byte against a lockfile pin.
const terraformProviderURI = "oci://hub.aggrega.ai/plugins/terraform-provider@sha256:903e3bd6fcfb0d43bbd11bc46a5636c85f8c680ecb1001c5fb844249ef7a4544"

// registry maps a canonical plugin URI to the embedded artifact's decompressed
// bytes. It is the lookup the cascade's embed tier consults, populated from
// pluginFS at init by register.
var registry = map[string][]byte{}

// register binds embedded plugin URIs to their bytes read from pluginFS. It is
// the single place an embedded plugin is wired: read its artifact from pluginFS
// and assign it under its canonical URI. The committed artifacts are
// gzip-compressed, so register decompresses each once here.
//
// A read or decompression failure for a registered artifact is a
// build-of-the-binary defect (the file is embedded at compile time), so
// register panics rather than returning an error — there is no recoverable
// runtime branch for a missing or corrupt embedded file.
func register() map[string][]byte {
	return map[string][]byte{
		helmProviderURI:      mustDecompress("plugins/helm-provider.wasm.gz"),
		composeProviderURI:   mustDecompress("plugins/compose-provider.wasm.gz"),
		helmChartProviderURI: mustDecompress("plugins/helmchart-provider.wasm.gz"),
		terraformProviderURI: mustDecompress("plugins/terraform-provider.wasm.gz"),
	}
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
// package can register an isolated URI→bytes pair — for example a tampered-bytes
// variant that must not disturb the production embedded set. It is test-only:
// the production wiring populates the registry through register at init, never
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
// (Deterministic Output invariant).
func URIs() []string {
	uris := make([]string, 0, len(registry))
	for u := range registry {
		uris = append(uris, u)
	}
	sort.Strings(uris)
	return uris
}

// mustRead reads an embedded artifact or panics. A failure means the embed
// directive and the registered path disagree, which is a compile-of-the-binary
// defect with no recoverable runtime branch.
func mustRead(name string) []byte {
	b, err := pluginFS.ReadFile(name)
	if err != nil {
		panic("wasm/embedded: read embedded plugin " + name + ": " + err.Error())
	}
	return b
}

// mustDecompress reads a gzip-compressed embedded artifact and returns its
// decompressed bytes, panicking on any read or decompression failure. A failure
// means the embed directive and the registered path disagree, or the committed
// artifact is not valid gzip — both are compile-of-the-binary defects with no
// recoverable runtime branch, mirroring mustRead's contract.
func mustDecompress(name string) []byte {
	gz, err := gzip.NewReader(bytes.NewReader(mustRead(name)))
	if err != nil {
		panic("wasm/embedded: open gzip for embedded plugin " + name + ": " + err.Error())
	}
	defer func() { _ = gz.Close() }()
	b, err := io.ReadAll(gz)
	if err != nil {
		panic("wasm/embedded: decompress embedded plugin " + name + ": " + err.Error())
	}
	return b
}
