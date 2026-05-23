// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// OCIPuller is the minimum surface ClosureLookup needs to reach the
// Tier-3 (network) fallback. Production wires this to a small adapter
// over pkg/oci.NewRemoteRepository + pkg/oci.Pull; tests inject an
// in-memory CAS implementing the same shape.
//
// Pull's signature mirrors pkg/oci.Pull's tag form: callers split a
// canonical entity URI into (reference, tag) at the call site. The
// adapter is responsible for any per-host registry routing (default
// registry, per-prefix overrides) — ClosureLookup just supplies the
// reference + tag string.
type OCIPuller interface {
	Pull(ctx context.Context, reference, tag string) (*oci.PullResult, error)
}

// ClosureLookup returns a closure.Lookup that resolves catalog
// entities, libraries, and Wasm plugins by walking three tiers in
// order:
//
//	Tier 1: in-memory catalog (already loaded into this Catalog).
//	Tier 2: cache slot at <workspaceRoot>/<cacheDir>/entities/<URI-encoded>/<version>/.
//	Tier 3: pull via the supplied OCIPuller. When ociPuller is nil
//	        (build-time hermetic path), Tier 3 returns Resolution{NotFound: true}
//	        so the closure walker surfaces ErrUnresolvableURI cleanly
//	        rather than attempting an out-of-band network call.
//
// Digest discipline: every tier returns a CONTENT digest (sha256 of
// the entity bytes), even at Tier-3 where pkg/oci.Pull also returns a
// manifest digest in its PullResult. The ClosureLookup layer
// standardises on content digests so the pull → build round-trip is
// deterministic: pull writes the same bytes to the cache that it
// recorded a content digest of in the lockfile; build re-reads those
// bytes at Tier-2 and computes the identical content digest. Re-using
// pkg/oci.Pull's manifest digest as the lockfile digest would break
// this without a per-slot sidecar metadata file; standardising on
// content digest avoids the sidecar entirely.
//
// Library cache layout: at Tier-3 pulls of SchemaLibrary or
// MappingLibrary mediatypes, ClosureLookup writes the bytes under the
// canonical library.yaml filename (matching the local-authoring
// layout the catalog walker reads from per-version subdirectories);
// every other entity kind writes under the generic blob filename via
// pkg/oci.WriteCached. The dispatch is a small switch on the
// PullResult.MediaType field.
func (c *Catalog) ClosureLookup(ociPuller OCIPuller, workspaceRoot, cacheDir string) closure.Lookup {
	return &catalogLookup{
		catalog:       c,
		ociPuller:     ociPuller,
		workspaceRoot: workspaceRoot,
		cacheDir:      cacheDir,
	}
}

// ClosureLookupWithExternals returns the same closure.Lookup as
// ClosureLookup, additionally primed with a "URI → bytes" map of
// external entities the in-memory catalog does not index. Used by
// the pull / build CLI verbs to register the Release's referenced
// Aggregate and Manifest bytes (these are deployment-side entities
// loaded via resolver.LoadAggregate / resolver.LoadManifest, not via
// catalog.Load) so the closure walker's ResolveCatalogEntity hits
// these URIs at Tier 1 instead of falling through to Tier 3.
//
// The externals map is consulted BEFORE the in-memory catalog;
// duplicate keys (same URI registered both as external and as
// catalog entity) are resolved as external. The bytes are hashed for
// the content-digest exactly like Tier-1 catalog hits.
func (c *Catalog) ClosureLookupWithExternals(ociPuller OCIPuller, workspaceRoot, cacheDir string, externals map[string][]byte) closure.Lookup {
	return &catalogLookup{
		catalog:       c,
		ociPuller:     ociPuller,
		workspaceRoot: workspaceRoot,
		cacheDir:      cacheDir,
		externals:     externals,
	}
}

type catalogLookup struct {
	catalog       *Catalog
	ociPuller     OCIPuller
	workspaceRoot string
	cacheDir      string
	externals     map[string][]byte
}

func (l *catalogLookup) ResolveCatalogEntity(uri string) (closure.Resolution, error) {
	if res, ok := l.lookupExternal(uri); ok {
		return res, nil
	}
	if res, ok := l.tier1CatalogEntity(uri); ok {
		return res, nil
	}
	if res, ok := l.tier2(uri); ok {
		return res, nil
	}
	return l.tier3CatalogEntity(uri)
}

func (l *catalogLookup) lookupExternal(uri string) (closure.Resolution, bool) {
	bytes, ok := l.externals[uri]
	if !ok {
		return closure.Resolution{}, false
	}
	_, version, _ := splitVersionedURI(uri)
	return closure.Resolution{
		ResolvedVersion: version,
		Digest:          contentDigest(bytes),
		ResolutionPath:  "external",
	}, true
}

func (l *catalogLookup) ResolveSchemaLibrary(uri string) (closure.LibraryResolution, error) {
	if res, entries, ok := l.tier1SchemaLibrary(uri); ok {
		return closure.LibraryResolution{Resolution: res, Entries: entries}, nil
	}
	if res, ok := l.tier2(uri); ok {
		return closure.LibraryResolution{Resolution: res}, nil
	}
	res, err := l.tier3Library(uri, "SchemaLibrary")
	return closure.LibraryResolution{Resolution: res}, err
}

func (l *catalogLookup) ResolveMappingLibrary(uri string) (closure.LibraryResolution, error) {
	if res, entries, ok := l.tier1MappingLibrary(uri); ok {
		return closure.LibraryResolution{Resolution: res, Entries: entries}, nil
	}
	if res, ok := l.tier2(uri); ok {
		return closure.LibraryResolution{Resolution: res}, nil
	}
	res, err := l.tier3Library(uri, "MappingLibrary")
	return closure.LibraryResolution{Resolution: res}, err
}

func (l *catalogLookup) ResolveWasmPlugin(uri string) (closure.Resolution, error) {
	if res, ok := l.tier2(uri); ok {
		return res, nil
	}
	return l.tier3CatalogEntity(uri)
}

func (l *catalogLookup) tier1CatalogEntity(uri string) (closure.Resolution, bool) {
	_, version, _ := splitVersionedURI(uri)
	lookupName := lookupNameFromURI(uri)

	if c, ok := l.catalog.Components[lookupName]; ok {
		return l.tier1Resolution("Component", c.Metadata.Name, c.Metadata.Version, version), true
	}
	if f, ok := l.catalog.Features[lookupName]; ok {
		return l.tier1Resolution("Feature", f.Metadata.Name, f.Metadata.Version, version), true
	}
	// Recipe / FeatureRecipe lookups go through RecipeByURI /
	// FeatureRecipeByURI which already implement the three-form
	// precedence against the metadata.name-keyed view (catalog.Recipes
	// is keyed by spec.implements internally; the public API rebuilds
	// the metadata.name view per call). The closure walker passes
	// recipe URIs as metadata.name + version (per Manifest mapping
	// values), not as spec.implements.
	if r, ok := l.catalog.RecipeByURI(uri); ok {
		return l.tier1Resolution("Recipe", r.Metadata.Name, r.Metadata.Version, version), true
	}
	if fr, ok := l.catalog.FeatureRecipeByURI(uri); ok {
		return l.tier1Resolution("FeatureRecipe", fr.Metadata.Name, fr.Metadata.Version, version), true
	}
	if p, ok := l.catalog.Providers[lookupName]; ok {
		return l.tier1Resolution("Provider", p.Metadata.Name, p.Metadata.Version, version), true
	}
	if a, ok := l.catalog.Aggregates[lookupName]; ok {
		return l.tier1Resolution("Aggregate", a.Metadata.Name, a.Metadata.Version, version), true
	}
	return closure.Resolution{}, false
}

func (l *catalogLookup) tier1SchemaLibrary(uri string) (closure.Resolution, map[string]struct{}, bool) {
	name := schemaLibraryNameFromURI(uri)
	lib, ok := l.catalog.SchemaLibraries[name]
	if !ok {
		return closure.Resolution{}, nil, false
	}
	resolved := lib.Metadata.Version
	if _, v, hasV := splitVersionedURI(uri); hasV && v != "" {
		resolved = v
	}
	entries := map[string]struct{}{}
	for n := range lib.Spec.Schemas.All() {
		entries[n] = struct{}{}
	}
	for n := range lib.Spec.Templates.All() {
		entries[n] = struct{}{}
	}
	return l.tier1Resolution("SchemaLibrary", lib.Metadata.Name, lib.Metadata.Version, resolved), entries, true
}

func (l *catalogLookup) tier1MappingLibrary(uri string) (closure.Resolution, map[string]struct{}, bool) {
	name := schemaLibraryNameFromURI(uri)
	lib, ok := l.catalog.MappingLibraries[name]
	if !ok {
		return closure.Resolution{}, nil, false
	}
	resolved := lib.Metadata.Version
	if _, v, hasV := splitVersionedURI(uri); hasV && v != "" {
		resolved = v
	}
	entries := map[string]struct{}{}
	for n := range lib.Spec.Mappings.All() {
		entries[n] = struct{}{}
	}
	return l.tier1Resolution("MappingLibrary", lib.Metadata.Name, lib.Metadata.Version, resolved), entries, true
}

func (l *catalogLookup) tier1Resolution(kind, identifier, metaVersion, requestedVersion string) closure.Resolution {
	bytes := l.catalog.entityBytes[entityBytesKey(kind, identifier)]
	resolved := metaVersion
	if requestedVersion != "" {
		resolved = requestedVersion
	}
	return closure.Resolution{
		ResolvedVersion: resolved,
		Digest:          contentDigest(bytes),
		ResolutionPath:  "tier-1-catalog",
	}
}

func (l *catalogLookup) tier2(uri string) (closure.Resolution, bool) {
	if l.cacheDir == "" {
		return closure.Resolution{}, false
	}
	_, version, hasV := splitVersionedURI(uri)
	if !hasV || version == "" {
		return closure.Resolution{}, false
	}
	slot := oci.CachePath(l.workspaceRoot, l.cacheDir, uri, version)
	bytes, err := readCacheSlotBytes(slot)
	if err != nil {
		return closure.Resolution{}, false
	}
	return closure.Resolution{
		ResolvedVersion: version,
		Digest:          contentDigest(bytes),
		ResolutionPath:  "tier-2-cache",
	}, true
}

func (l *catalogLookup) tier3CatalogEntity(uri string) (closure.Resolution, error) {
	if l.ociPuller == nil {
		return closure.Resolution{NotFound: true}, nil
	}
	reference, tag, ok := splitURIForOCI(uri)
	if !ok {
		return closure.Resolution{NotFound: true}, nil
	}
	result, err := l.ociPuller.Pull(context.Background(), reference, tag)
	if err != nil {
		if errors.Is(err, oci.ErrFetchNotFound) {
			return closure.Resolution{NotFound: true}, nil
		}
		return closure.Resolution{}, fmt.Errorf("catalog: tier-3 pull %s: %w", uri, err)
	}
	if err := writeEntityToCacheSlot(l.workspaceRoot, l.cacheDir, uri, tag, result); err != nil {
		return closure.Resolution{}, fmt.Errorf("catalog: tier-3 cache write %s: %w", uri, err)
	}
	return closure.Resolution{
		ResolvedVersion: tag,
		Digest:          contentDigest(result.Bytes),
		ResolutionPath:  "tier-3-pull",
	}, nil
}

func (l *catalogLookup) tier3Library(uri, kind string) (closure.Resolution, error) {
	if l.ociPuller == nil {
		return closure.Resolution{NotFound: true}, nil
	}
	reference, tag, ok := splitURIForOCI(uri)
	if !ok {
		return closure.Resolution{NotFound: true}, nil
	}
	result, err := l.ociPuller.Pull(context.Background(), reference, tag)
	if err != nil {
		if errors.Is(err, oci.ErrFetchNotFound) {
			return closure.Resolution{NotFound: true}, nil
		}
		return closure.Resolution{}, fmt.Errorf("catalog: tier-3 pull %s %s: %w", kind, uri, err)
	}
	if err := writeEntityToCacheSlot(l.workspaceRoot, l.cacheDir, uri, tag, result); err != nil {
		return closure.Resolution{}, fmt.Errorf("catalog: tier-3 cache write %s %s: %w", kind, uri, err)
	}
	return closure.Resolution{
		ResolvedVersion: tag,
		Digest:          contentDigest(result.Bytes),
		ResolutionPath:  "tier-3-pull",
	}, nil
}

// writeEntityToCacheSlot stores the pulled bytes at the standard
// cache slot for uri@version. For SchemaLibrary and MappingLibrary
// mediatypes the bytes land under the canonical "library.yaml"
// filename (matching the local-authoring layout); every other
// mediatype writes via pkg/oci.WriteCached's "blob" filename.
func writeEntityToCacheSlot(workspaceRoot, cacheDir, uri, version string, result *oci.PullResult) error {
	if cacheDir == "" {
		return nil
	}
	switch result.MediaType {
	case oci.MediaTypeSchemaLibrary, oci.MediaTypeMappingLibrary:
		slot := oci.CachePath(workspaceRoot, cacheDir, uri, version)
		if err := os.MkdirAll(slot, 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(slot, "library.yaml"), result.Bytes, 0o644)
	default:
		return oci.WriteCached(workspaceRoot, cacheDir, uri, version, result.Bytes)
	}
}

// readCacheSlotBytes reads the entity bytes from a cache slot. Tries
// the canonical filenames in order: library.yaml (libraries),
// blob (everything else).
func readCacheSlotBytes(slot string) ([]byte, error) {
	for _, name := range []string{"library.yaml", "blob"} {
		path := filepath.Join(slot, name)
		if data, err := os.ReadFile(path); err == nil {
			return data, nil
		}
	}
	return nil, os.ErrNotExist
}

// contentDigest returns the standard OCI-format SHA-256 digest of
// content ("sha256:<hex>"). Matches pkg/oci.computeDigest's shape
// without re-exporting the unexported helper.
func contentDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// entityBytesKey composes the lookup key the Catalog uses to store
// raw entity YAML for content-digest computation. Same shape as the
// componentSources / featureSources keys but namespaced by kind so
// the same identifier never collides across entity kinds.
func entityBytesKey(kind, identifier string) string {
	return kind + ":" + identifier
}

// splitURIForOCI splits a canonical entity URI into the (repository
// reference, tag) pair pkg/oci.Pull consumes. The reference is the
// URI with any trailing ":<version>" stripped; the tag is the
// version. Returns ok=false when no version is present (OCI pull
// requires a tag).
func splitURIForOCI(uri string) (reference, tag string, ok bool) {
	base, version, hasV := splitVersionedURI(uri)
	if !hasV || version == "" {
		return "", "", false
	}
	return base, version, true
}

// lookupNameFromURI returns the URI with any trailing ":<version>"
// segment stripped. The catalog indexes Component / Feature /
// Provider / Aggregate / SchemaLibrary / MappingLibrary entries by
// the verbatim metadata.name string (which may itself contain "/"
// separators — e.g., "core/server", "hub.aggrega.ai/component/core/api-server").
// Stripping path segments here would lose load-bearing prefix
// content; we strip only the version-pin suffix.
func lookupNameFromURI(uri string) string {
	base, _, _ := splitVersionedURI(uri)
	return base
}

// schemaLibraryNameFromURI returns the library-identifier portion
// of a canonical SchemaLibrary or MappingLibrary URI. Strips any
// trailing ":<version>" suffix and any leading namespace path
// segments — the catalog indexes libraries by their PascalCase
// metadata.name (e.g., "Probes", "BaseMappings"), which is the
// final "/"-separated segment of a canonical library URI like
// "hub.aggrega.ai/schemalibrary/core/Probes".
func schemaLibraryNameFromURI(uri string) string {
	base, _, _ := splitVersionedURI(uri)
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		return base[idx+1:]
	}
	return base
}
