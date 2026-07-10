// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1 && !light

// Package embedded is the compile-time home for Wasm plugins shipped inside
// the CLI binary. A plugin embedded here is version-locked to the binary and
// resolves with no filesystem, cache, or network access — the first tier the
// plugin-resolution cascade consults.
//
// The provider rendering guests are embedded here. Each artifact is a
// reproducible build output — gzip-compressed under plugins/ (the uncompressed
// Wasm is large), regenerated from guest source by `go generate` rather than
// tracked in git, so a fresh checkout has none until a build runs — decompressed
// once at init into the registry, keyed by the
// plugin's digest-free repository reference — the same reference a Plugin
// entity's spec.plugin names. Keying on the repository, not a content digest,
// means a rebuild of the embedded bytes does not perturb the lookup key: the
// reference digest is derived from the bytes on demand where a caller needs it.
// Adding another embedded plugin is a drop-in: add a row to the embeddedArtifacts
// table and its build-embedded generate step so its gzip-compressed .wasm lands
// under plugins/.
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
//go:generate bash ../../../scripts/build-embedded-cloudformation-provider.sh
//go:generate bash ../../../scripts/build-embedded-publishing-linter.sh

// pluginFS holds the embedded plugin artifacts. The `all:` prefix matches
// every file under plugins/ including the dot-prefixed .gitkeep placeholder
// that keeps the directory present in the tree even when no artifact is checked
// out (a bare `//go:embed plugins` rejects a directory with no non-dot files).
// An embedded artifact added under plugins/ is picked up here automatically;
// register binds it to a URI.
//
//go:embed all:plugins
var pluginFS embed.FS

// helmProviderURI is the digest-free repository reference the helm provider
// rendering plugin's decompressed bytes register under — the same reference a
// Plugin entity's spec.plugin names. The reference digest is derived from these
// bytes on demand (never stored here), so a rebuild of the guest does not change
// the lookup key.
const helmProviderURI = "oci://hub.aggrega.ai/plugins/core/helm-provider"

// composeProviderURI is the digest-free repository reference the compose provider
// rendering plugin's decompressed bytes register under. Like helmProviderURI, the
// reference digest is derived from the bytes on demand rather than stored in the
// key, so a guest rebuild does not perturb the lookup.
const composeProviderURI = "oci://hub.aggrega.ai/plugins/core/compose-provider"

// helmChartProviderURI is the digest-free repository reference the helm-chart
// provider rendering plugin's decompressed bytes register under. Like the other
// provider references, the reference digest is derived from the bytes on demand,
// so a guest rebuild does not perturb the lookup key.
const helmChartProviderURI = "oci://hub.aggrega.ai/plugins/core/helmchart-provider"

// terraformProviderURI is the digest-free repository reference the terraform
// provider rendering plugin's decompressed bytes register under. Like the other
// provider references, the reference digest is derived from the bytes on demand,
// so a guest rebuild does not perturb the lookup key.
const terraformProviderURI = "oci://hub.aggrega.ai/plugins/core/terraform-provider"

// cloudformationProviderURI is the digest-free repository reference the
// cloudformation provider rendering plugin's decompressed bytes register under.
// Like the other provider references, the reference digest is derived from the
// bytes on demand, so a guest rebuild does not perturb the lookup key.
const cloudformationProviderURI = "oci://hub.aggrega.ai/plugins/core/cloudformation-provider"

// PublishingLinterURI (exported) is defined in embedded_uris.go so both the
// default and light host builds share the same linter identity.

// embeddedArtifact binds one embedded plugin's digest-free repository reference
// to its gzip-compressed guest bytes and its build-extracted describe()
// descriptor, both stored under plugins/. It is the single table the byte
// registry and the descriptor registry are built from, so a plugin's URI and
// its two asset paths are declared exactly once and cannot drift apart.
type embeddedArtifact struct {
	uri        string
	wasmGz     string
	descriptor string
}

// embeddedArtifacts is the one table of embedded plugins. Adding a plugin is a
// single row here plus its gzip-compressed guest (and, once the build has run,
// its describe() descriptor) under plugins/.
func embeddedArtifacts() []embeddedArtifact {
	return []embeddedArtifact{
		{helmProviderURI, "plugins/helm-provider.wasm.gz", "plugins/helm-provider.describe.json"},
		{composeProviderURI, "plugins/compose-provider.wasm.gz", "plugins/compose-provider.describe.json"},
		{helmChartProviderURI, "plugins/helmchart-provider.wasm.gz", "plugins/helmchart-provider.describe.json"},
		{terraformProviderURI, "plugins/terraform-provider.wasm.gz", "plugins/terraform-provider.describe.json"},
		{cloudformationProviderURI, "plugins/cloudformation-provider.wasm.gz", "plugins/cloudformation-provider.describe.json"},
		{PublishingLinterURI, "plugins/publishing-linter.wasm.gz", "plugins/publishing-linter.describe.json"},
	}
}

// registry maps a plugin's digest-free repository reference to the embedded
// artifact's decompressed bytes. It is the lookup the cascade's embed tier
// consults, populated from pluginFS at init by register.
var registry = map[string][]byte{}

// descriptorRegistry maps a plugin's digest-free repository reference to its
// build-extracted describe() descriptor JSON. It is the lookup the plugin
// listing consults to report a guest's static metadata without instantiating
// it, populated from pluginFS at init by registerDescriptors.
var descriptorRegistry = map[string][]byte{}

// register binds embedded plugin repository references to their bytes read from
// pluginFS. It is the single place an embedded plugin's guest is wired: read its
// artifact from pluginFS and assign it under its repository reference. The
// generated artifacts are gzip-compressed, so register decompresses each once
// here.
//
// A read or decompression failure for a registered artifact is a
// build-of-the-binary defect (the file is embedded at compile time), so
// register panics rather than returning an error — there is no recoverable
// runtime branch for a missing or corrupt embedded file.
func register() map[string][]byte {
	out := make(map[string][]byte, len(embeddedArtifacts()))
	for _, a := range embeddedArtifacts() {
		out[a.uri] = mustDecompress(a.wasmGz)
	}
	return out
}

// registerDescriptors binds each embedded plugin's repository reference to the
// describe() descriptor JSON extracted from its guest at build time and stored
// alongside the .wasm.gz. A descriptor asset that is absent — a tree built
// before the descriptors were generated — is skipped rather than fatal: the
// describe read path falls back to loading and describing the guest when no
// static descriptor is registered, so a missing descriptor degrades to the
// load-and-describe path rather than to stale or empty output. This is the one
// place an absent embedded asset is tolerated; the guest bytes themselves remain
// mandatory (see register).
func registerDescriptors() map[string][]byte {
	out := map[string][]byte{}
	for _, a := range embeddedArtifacts() {
		b, err := pluginFS.ReadFile(a.descriptor)
		if err != nil {
			continue
		}
		out[a.uri] = b
	}
	return out
}

func init() {
	registry = register()
	descriptorRegistry = registerDescriptors()
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

// LookupDescriptor returns the build-extracted describe() descriptor JSON for
// uri and whether one is registered. A hit yields a defensive copy so a caller
// cannot mutate the embedded bytes backing every future lookup of the same URI.
// A miss — an unregistered URI, or a tree built before the descriptor assets
// were generated — returns false, and the caller falls back to loading and
// describing the guest rather than reporting stale or empty metadata.
func LookupDescriptor(uri string) ([]byte, bool) {
	b, ok := descriptorRegistry[uri]
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
// means the embed directive and the registered path disagree, or the generated
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
