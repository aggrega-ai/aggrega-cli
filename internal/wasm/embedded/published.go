// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package embedded

// PublishedPlugin is what the binary carries of a core plugin it does not embed:
// enough to select the plugin and to name the exact module it accepts, and none
// of the module's bytes. Such a plugin is built, stamped and published with each
// compiler release like every core plugin; the binary holds its generated Plugin
// entity and the content digest of the guest that release publishes, so a caller
// can choose it by what the entity advertises and hold any module it later reads
// to the digest.
//
// It is declared here, outside every build-specific file, so the default, light
// and guest builds share one shape; only the default build carries any.
type PublishedPlugin struct {
	// URI is the plugin's bare identity, <socket>/<name> — the identity a Plugin
	// entity derives from its pluginType and name, and the reference the plugin
	// cascade resolves its module by.
	URI string

	// Entity is the plugin's generated Plugin entity, the same document the
	// release publishes beside the guest.
	Entity []byte

	// Digest is the content digest of the decompressed guest, in the spelling
	// every content-addressing consumer compares (sha256:<lowercase hex>): the
	// digest a module read for this plugin must match.
	Digest string
}

// publishedRegistry holds what the binary carries of each core plugin it does
// not embed — its generated entity and its guest's content digest — in the
// core-plugin list's order. The default host build fills it at init from the
// assets the scripts generate; the light and guest builds carry no entities and
// leave it empty.
var publishedRegistry []PublishedPlugin

// PublishedPlugins returns what the binary carries of each core plugin it does
// not embed — its bare identity, its generated entity and its guest's content
// digest — in the core-plugin list's order, so a consumer folding them into a
// resolution set reads them in one order on every run (Deterministic Output
// invariant). Each entity is a defensive copy, so a caller cannot mutate the
// bytes backing every later call. A light or guest build returns none.
//
// These are not embedded plugins, and EntityURIs and LookupEntity never report
// them: an embedded plugin's entity is served beside the module the binary
// carries for it, while a plugin here has no module in the binary at all.
func PublishedPlugins() []PublishedPlugin {
	out := make([]PublishedPlugin, 0, len(publishedRegistry))
	for _, p := range publishedRegistry {
		entity := make([]byte, len(p.Entity))
		copy(entity, p.Entity)
		out = append(out, PublishedPlugin{URI: p.URI, Entity: entity, Digest: p.Digest})
	}
	return out
}

// SeedPublishedForTest replaces what the binary carries of the core plugins it
// does not embed with plugins, in the order given, and returns a function that
// restores what it carried before.
//
// It exists so a test can run a resolution against a carried set it chose rather
// than the one the build happened to generate: none at all, which is what a light
// build or a checkout without the overlays carries, or one whose entity and
// digest the test controls. The set the build generates depends on how that
// build was run, so a test whose outcome turns on the set's contents would
// otherwise prove something about the build rather than about the code. It is
// test-only: the default build fills the set at init, never through this seam.
//
// The set is process-wide, so a test seeding it must not run in parallel with
// any test that reads it, and must restore it before returning.
func SeedPublishedForTest(plugins []PublishedPlugin) (restore func()) {
	prev := publishedRegistry
	seeded := make([]PublishedPlugin, 0, len(plugins))
	for _, p := range plugins {
		entity := make([]byte, len(p.Entity))
		copy(entity, p.Entity)
		seeded = append(seeded, PublishedPlugin{URI: p.URI, Entity: entity, Digest: p.Digest})
	}
	publishedRegistry = seeded
	return func() { publishedRegistry = prev }
}
