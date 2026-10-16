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
// once at init into the registry, keyed by the plugin's bare identity
// (`<socket>/<name>`) — the same identity a Plugin entity derives from its
// pluginType and name. Keying on the bare identity, not a content digest and not
// a
// host-qualified location, means the embed tier resolves the built-ins offline
// and independently of the configured registry, and a rebuild of the embedded
// bytes does not perturb the lookup key: the reference digest is derived from the
// bytes on demand where a caller needs it. Adding another embedded plugin is a
// drop-in: add a row to the core-plugin list (embedded-plugins.tsv) — the single
// declarative source the registry table, the build script, and the freshness and
// drift guards all read — so its gzip-compressed .wasm lands under plugins/ and
// registers automatically. A row whose embed field is "no" is a core plugin the
// binary does not carry: the scripts build and publish it, and its artifacts are
// written beside plugins/ rather than under it, so pluginFS never takes them in.
// Of such a plugin the binary carries only its generated entity and the content
// digest of its guest, which the same scripts write under published-selection/
// and publishedFS embeds (see PublishedPlugins) — never its module.
//
// Freshness caveat: a plain go build does not regenerate these artifacts — it
// embeds whatever .wasm.gz already sit under plugins/ from an earlier build,
// which can predate a later change to a guest or to the shared render core a
// guest reuses, silently shipping stale provider behavior. Regenerate before
// building with `go generate ./internal/wasm/embedded/... && go build`, or use
// the repository build script (scripts/build.sh at the workspace root), which
// always regenerates the guests before the embedding compile. The sibling
// check-embedded-guests.sh script flags any artifact older than its source.
package embedded

import (
	"bytes"
	"compress/gzip"
	"embed"
	"io"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
)

// The embedded guests are reproducible build artifacts, not tracked files:
// `go generate` rebuilds each gzip-compressed .wasm.gz under plugins/ from its
// guest source. One script drives every entry in the embedded-plugin manifest,
// branching on each row's toolchain. It pins a reproducible recipe (GOWORK=off,
// -trimpath, -ldflags=-buildid=, -buildvcs=false, gzip -n for go guests; the
// analogous path-remap, symbol-strip, and non-incremental recipe for rust) so
// the uncompressed digest is stable across rebuilds and matches the repository
// references registered below.
//
//go:generate bash ../../../scripts/build-embedded-plugins.sh

// pluginFS holds the embedded plugin artifacts. The `all:` prefix matches
// every file under plugins/ including the dot-prefixed .gitkeep placeholder
// that keeps the directory present in the tree even when no artifact is checked
// out (a bare `//go:embed plugins` rejects a directory with no non-dot files).
// An embedded artifact added under plugins/ is picked up here automatically;
// register binds it to a URI.
//
//go:embed all:plugins
var pluginFS embed.FS

// publishedFS holds, for each core plugin the binary does not embed, its
// generated Plugin entity and the content digest of its guest, both written by
// the scripts that build and generate every core plugin. The directory never
// holds a module: the guests of those plugins are written to published-only/,
// which no embed directive names. The `all:` prefix takes in the .gitkeep
// placeholder that keeps the directory present before anything is generated.
//
//go:embed all:published-selection
var publishedFS embed.FS

// publishedDir is the directory publishedFS embeds, relative to this package.
// The scripts name the same directory from the module root (scripts/lib/
// core-plugins.sh, CORE_PLUGIN_SELECTION_DIR), and a test holds the two to one
// place.
const publishedDir = "published-selection"

// The bare identity every embedded plugin registers under is derived from its
// manifest row (`{socket}/{name}`) — the same identity a Plugin entity derives
// from its pluginType and name. The reference digest is derived from the decompressed
// bytes on demand where a caller needs it, never stored in the key, so a rebuild
// of a guest does not perturb the lookup.
//
// PublishingLinterURI (exported) is defined in embedded_uris.go so both the
// default and light host builds share the same linter identity; it equals the
// manifest-derived bare identity for the linter/publishing row.

//go:embed embedded-plugins.tsv
var manifestTSV []byte

// embeddedArtifact binds one embedded plugin's digest-free repository reference
// to its gzip-compressed guest bytes, its build-extracted describe() descriptor,
// and its generated Plugin entity, all stored under plugins/. It is the single
// table the byte registry, the descriptor registry, and the entity registry are
// built from, so a plugin's URI and its three asset paths are declared exactly
// once and cannot drift apart.
type embeddedArtifact struct {
	uri        string
	wasmGz     string
	descriptor string
	entity     string
}

// embeddedArtifacts is the table of embedded plugins, derived from the
// embedded-plugin manifest embedded at compile time. Each row's repository
// reference and its two asset paths (gzip-compressed guest, describe()
// descriptor) are computed from the row's socket and name, so a plugin's
// identity and asset locations are authored exactly once — in the manifest,
// alongside the build/check scripts that read the same file. Adding a plugin is
// a single manifest row plus its gzip-compressed guest under plugins/.
func embeddedArtifacts() []embeddedArtifact {
	return parseManifest(manifestTSV)
}

// embedField is the zero-based index of the core-plugin list's sixth field,
// which says whether the binary embeds the row's plugin: empty or absent means
// it does, notEmbedded means it does not.
const embedField = 5

// notEmbedded is the one value of the embed field that keeps a core plugin out
// of the binary. The plugin is still built, stamped, generated and published by
// the scripts that read the same list (scripts/lib/core-plugins.sh); it is only
// never registered here, and its artifacts are written where pluginFS does not
// reach.
const notEmbedded = "no"

// parseManifest derives the embedded-artifact table from the tab-separated
// core-plugin list: the rows the binary embeds, in list order. It reads the list
// through readCoreList and refuses a list naming no plugin the binary embeds.
//
// The scripts read the list through one shell reader, and a test holds that
// reader and this function to the same answer over shared fixtures, so the two
// readings of the embed field cannot come apart.
//
// A malformed or empty list is a build-of-the-binary defect — the file is
// embedded at compile time and drives the scripts that produce the assets — so
// this panics rather than returning an error, mirroring the mustRead and
// mustDecompress contracts for the embedded bytes themselves.
func parseManifest(b []byte) []embeddedArtifact {
	embeds, _ := readCoreList(b)
	if len(embeds) == 0 {
		panic("plugin/embedded: core-plugin list declares no plugin the binary embeds")
	}
	return embeds
}

// publishedArtifact binds one core plugin the binary does not embed to the two
// assets the binary carries of it, both under publishedDir: its generated Plugin
// entity and the file holding the content digest of its guest.
type publishedArtifact struct {
	uri    string
	entity string
	digest string
}

// publishedArtifacts is the table of core plugins the binary does not embed,
// derived from the same embedded core-plugin list as embeddedArtifacts.
func publishedArtifacts() []publishedArtifact {
	_, published := readCoreList(manifestTSV)
	return published
}

// readCoreList is the binary's one reading of the core-plugin list. It ignores
// blank lines and #-prefixed comments and reads the socket (field 1) and name
// (field 2) of each remaining row, from which the repository reference and the
// asset paths are computed, and the embed field (field 6), which says whether
// the row is a plugin the binary embeds or one it carries only the entity and
// guest digest of. The fields between (source path, toolchain, extra roots)
// drive the build and freshness scripts, not the registry, so they are ignored
// here. Both tables are in list order.
//
// A malformed row panics, for the reason parseManifest gives. An embed value
// other than empty or notEmbedded is malformed: read as either, a typo would
// silently embed a plugin or drop one.
func readCoreList(b []byte) (embeds []embeddedArtifact, published []publishedArtifact) {
	for _, raw := range strings.Split(string(b), "\n") {
		line := strings.TrimRight(raw, "\r")
		if t := strings.TrimSpace(line); t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			panic("plugin/embedded: malformed core-plugin list row: " + line)
		}
		socket, name := strings.TrimSpace(fields[0]), strings.TrimSpace(fields[1])
		if socket == "" || name == "" {
			panic("plugin/embedded: core-plugin list row missing socket or name: " + line)
		}
		if len(fields) > embedField {
			switch strings.TrimSpace(fields[embedField]) {
			case "":
			case notEmbedded:
				published = append(published, publishedArtifact{
					uri:    socket + "/" + name,
					entity: publishedDir + "/" + name + ".plugin.yaml",
					digest: publishedDir + "/" + name + ".digest",
				})
				continue
			default:
				panic("plugin/embedded: core-plugin list row has an embed field other than empty or " + notEmbedded + ": " + line)
			}
		}
		embeds = append(embeds, embeddedArtifact{
			uri:        socket + "/" + name,
			wasmGz:     "plugins/" + name + ".wasm.gz",
			descriptor: "plugins/" + name + ".describe.json",
			entity:     "plugins/" + name + ".plugin.yaml",
		})
	}
	return embeds, published
}

// registry maps a plugin's bare identity (`<socket>/<name>`) to the embedded
// artifact's decompressed bytes. It is the lookup the cascade's embed tier
// consults, populated from pluginFS at init by register.
var registry = map[string][]byte{}

// descriptorRegistry maps a plugin's bare identity (`<socket>/<name>`) to its
// build-extracted describe() descriptor JSON. It is the lookup the plugin
// listing consults to report a guest's static metadata without instantiating
// it, populated from pluginFS at init by registerDescriptors.
var descriptorRegistry = map[string][]byte{}

// entityRegistry maps a plugin's bare identity (`<socket>/<name>`) to its
// generated Plugin entity YAML — the complete catalog entity (machine-derived
// describe() contract merged with the authored overlay, runtime block included)
// emitted beside the guest at generation time. It is the lookup provider
// resolution consults to route a core provider by capability with no local
// catalog and no registry round-trip, populated from pluginFS at init by
// registerEntities.
var entityRegistry = map[string][]byte{}

// publishedRegistry holds what the binary carries of each core plugin it does
// not embed — its generated entity and its guest's content digest — in the
// core-plugin list's order. Populated from publishedFS at init by
// registerPublished.
var publishedRegistry []PublishedPlugin

// register binds embedded plugin bare identities to their bytes read from
// pluginFS. It is the single place an embedded plugin's guest is wired: read its
// artifact from pluginFS and assign it under its bare identity. The generated
// artifacts are gzip-compressed, so register decompresses each once here.
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

// registerDescriptors binds each embedded plugin's bare identity to the
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

// registerEntities binds each embedded plugin's bare identity to the generated
// Plugin entity YAML emitted beside its guest at generation time. An entity
// asset that is absent — a tree built before the entities were generated, or a
// hub-less checkout where the overlays the generator merges are unavailable — is
// skipped rather than fatal, mirroring registerDescriptors: provider resolution
// then finds no embedded entity for that coordinate and falls through to the
// catalog/registry tiers exactly as it did before entities were embedded. The
// guest bytes themselves remain mandatory (see register).
func registerEntities() map[string][]byte {
	out := map[string][]byte{}
	for _, a := range embeddedArtifacts() {
		b, err := pluginFS.ReadFile(a.entity)
		if err != nil {
			continue
		}
		out[a.uri] = b
	}
	return out
}

// registerPublished binds each core plugin the binary does not embed to its
// generated entity and its guest's content digest, read from publishedFS. A
// plugin is registered only when both assets are there and the digest file is
// not blank, because the binary may offer a plugin for selection only when it
// can also name the exact module it accepts. An absent asset is skipped rather
// than fatal, as registerEntities skips one: a tree built before the assets were
// generated, or a checkout without the sibling hub whose overlays the entity is
// generated from, carries none, and a request for such a plugin then finds no
// entity advertising what it asked for.
func registerPublished() []PublishedPlugin {
	var out []PublishedPlugin
	for _, a := range publishedArtifacts() {
		entity, err := publishedFS.ReadFile(a.entity)
		if err != nil {
			continue
		}
		raw, err := publishedFS.ReadFile(a.digest)
		if err != nil {
			continue
		}
		d := strings.TrimSpace(string(raw))
		if d == "" {
			continue
		}
		out = append(out, PublishedPlugin{URI: a.uri, Entity: entity, Digest: d})
	}
	return out
}

func init() {
	registry = register()
	descriptorRegistry = registerDescriptors()
	entityRegistry = registerEntities()
	publishedRegistry = registerPublished()
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

// LookupEntity returns the generated Plugin entity YAML for uri and whether one
// is registered. A hit yields a defensive copy so a caller cannot mutate the
// embedded bytes backing every future lookup of the same URI. A miss — an
// unregistered URI, or a tree built before the entity assets were generated —
// returns false, and provider resolution falls through to the catalog/registry
// tiers rather than routing off a core entity it does not have.
func LookupEntity(uri string) ([]byte, bool) {
	b, ok := entityRegistry[uri]
	if !ok {
		return nil, false
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out, true
}

// EntityURIs returns the URIs of every embedded plugin that carries a generated
// entity, in sorted order. Sorted so any consumer that enumerates the embedded
// entity set is deterministic (Deterministic Output invariant).
func EntityURIs() []string {
	uris := make([]string, 0, len(entityRegistry))
	for u := range entityRegistry {
		uris = append(uris, u)
	}
	sort.Strings(uris)
	return uris
}

// PublishedPlugins returns what the binary carries of each core plugin it does
// not embed — its bare identity, its generated entity and its guest's content
// digest — in the core-plugin list's order, so a consumer folding them into a
// resolution set reads them in one order on every run (Deterministic Output
// invariant). Each entity is a defensive copy, so a caller cannot mutate the
// bytes backing every later call.
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

// ContentDigests returns the content digest of every embedded plugin guest, in
// URI-sorted order. Each digest is derived on demand from the decompressed guest
// bytes — the same derivation every content-addressing consumer performs — so a
// caller retaining the embedded set against a content-addressed cache sees the
// exact digests that cache addresses its entries by. It scopes the full embedded
// set (URIs), not only the entity-carrying subset, because a describe-cache entry
// is keyed by a guest's content digest whether or not that guest ships a
// generated entity. It is derived here, in the one place that holds the embedded
// bytes, so no consumer re-enumerates the embedded plugins or re-derives their
// identity. Sorted so any consumer that enumerates it is deterministic
// (Deterministic Output invariant).
func ContentDigests() []string {
	uris := URIs()
	out := make([]string, 0, len(uris))
	for _, u := range uris {
		b, ok := Lookup(u)
		if !ok {
			continue
		}
		out = append(out, digest.ContentDigest(b))
	}
	return out
}

// mustRead reads an embedded artifact or panics. A failure means the embed
// directive and the registered path disagree, which is a compile-of-the-binary
// defect with no recoverable runtime branch.
func mustRead(name string) []byte {
	b, err := pluginFS.ReadFile(name)
	if err != nil {
		panic("plugin/embedded: read embedded plugin " + name + ": " + err.Error())
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
		panic("plugin/embedded: open gzip for embedded plugin " + name + ": " + err.Error())
	}
	defer func() { _ = gz.Close() }()
	b, err := io.ReadAll(gz)
	if err != nil {
		panic("plugin/embedded: decompress embedded plugin " + name + ": " + err.Error())
	}
	return b
}
