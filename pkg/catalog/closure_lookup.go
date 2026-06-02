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

	// priorPins, when non-nil, turns Tier-2 cache reuse into a verified
	// reuse: a cached slot whose content digest no longer matches its
	// prior-lockfile pin is skipped (re-fetched at Tier-3) instead of
	// trusted. refetchPending flags the URIs that drifted so the Tier-3
	// arm can record a notice via warn; both are nil unless a
	// ReuseVerification was supplied (the hermetic build and serve
	// paths leave them nil and reuse cache slots unconditionally).
	priorPins      map[string]string
	refetchPending map[string]string
	warn           func(notice string)
}

// ReuseVerification, when supplied to ClosureLookupWithReuse, makes
// every Tier-2 cache reuse verify the cached bytes against a prior
// lockfile's pins before trusting them. A reused entity whose content
// digest no longer matches its pin is re-fetched from the registry
// (Tier-3), which rewrites the cache slot from authoritative bytes,
// and a notice is handed to Warn. A nil *ReuseVerification disables the
// check, leaving behavior identical to ClosureLookupWithExternals.
//
// PriorPins maps a canonical entity URI to the content digest the prior
// lockfile recorded for it. A URI absent from the map is reused without
// verification — there is nothing to compare against (e.g. an entity
// that entered closure after the prior lock).
type ReuseVerification struct {
	PriorPins map[string]string
	Warn      func(notice string)
}

// ClosureLookupWithReuse returns the same closure.Lookup as
// ClosureLookupWithExternals, additionally verifying Tier-2 cache reuse
// against a prior lockfile when reuse is non-nil. It is the lookup the
// re-pull flow uses: reusing cached bytes that have drifted from their
// pin would silently launder a corrupt or upstream-re-tagged slot into
// the freshly-written lockfile, so a mismatch re-fetches from the
// registry (re-establishing the pin from authoritative bytes) and
// records a notice rather than trusting the slot.
//
// A nil reuse argument yields exactly ClosureLookupWithExternals'
// behavior, so the hermetic build and serve paths (which pass nil) are
// unaffected.
func (c *Catalog) ClosureLookupWithReuse(ociPuller OCIPuller, workspaceRoot, cacheDir string, externals map[string][]byte, reuse *ReuseVerification) closure.Lookup {
	l := &catalogLookup{
		catalog:       c,
		ociPuller:     ociPuller,
		workspaceRoot: workspaceRoot,
		cacheDir:      cacheDir,
		externals:     externals,
	}
	if reuse != nil {
		l.priorPins = reuse.PriorPins
		l.refetchPending = map[string]string{}
		l.warn = reuse.Warn
	}
	return l
}

func (l *catalogLookup) ResolveCatalogEntity(uri string) (closure.Resolution, error) {
	if res, ok := l.lookupExternal(uri); ok {
		return res, nil
	}
	if res, ok := l.tier1CatalogEntity(uri); ok {
		return res, nil
	}
	if _, res, ok := l.tier2(uri); ok {
		return res, nil
	}
	_, res, err := l.tier3CatalogEntity(uri)
	return res, err
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
	if _, res, ok := l.tier2(uri); ok {
		return closure.LibraryResolution{Resolution: res}, nil
	}
	res, err := l.tier3Library(uri, "SchemaLibrary")
	return closure.LibraryResolution{Resolution: res}, err
}

func (l *catalogLookup) ResolveMappingLibrary(uri string) (closure.LibraryResolution, error) {
	if res, entries, ok := l.tier1MappingLibrary(uri); ok {
		return closure.LibraryResolution{Resolution: res, Entries: entries}, nil
	}
	if _, res, ok := l.tier2(uri); ok {
		return closure.LibraryResolution{Resolution: res}, nil
	}
	res, err := l.tier3Library(uri, "MappingLibrary")
	return closure.LibraryResolution{Resolution: res}, err
}

func (l *catalogLookup) ResolveWasmPlugin(uri string) (closure.Resolution, error) {
	if _, res, ok := l.tier2(uri); ok {
		return res, nil
	}
	_, res, err := l.tier3CatalogEntity(uri)
	return res, err
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

func (l *catalogLookup) tier2(uri string) ([]byte, closure.Resolution, bool) {
	bytes, res, ok := tier2ReadBytes(l.workspaceRoot, l.cacheDir, uri)
	if !ok {
		return nil, closure.Resolution{}, false
	}
	if l.priorPins == nil {
		return bytes, res, true
	}
	pin, havePin := l.priorPins[uri]
	if !havePin || contentDigest(bytes) == pin {
		return bytes, res, true
	}
	// Re-pull reuse-check: the cached slot no longer hashes to the pin
	// the prior lockfile recorded. Skip the hit and flag the URI so the
	// Tier-3 re-fetch (which rewrites the slot) records a drift notice.
	// Re-establishing the pin from authoritative registry bytes is the
	// correct action where the registry is reachable, rather than
	// laundering the drifted slot into the new lockfile.
	l.refetchPending[uri] = pin
	return nil, closure.Resolution{}, false
}

func (l *catalogLookup) tier3CatalogEntity(uri string) ([]byte, closure.Resolution, error) {
	bytes, res, err := tier3PullCatalogEntity(l.workspaceRoot, l.cacheDir, l.ociPuller, uri)
	if err == nil && !res.NotFound {
		l.recordRefetchNotice(uri, res.Digest)
	}
	return bytes, res, err
}

// recordRefetchNotice emits a re-fetch notice via the configured warn
// sink when uri was re-pulled because its cached slot drifted from the
// prior pin (the Tier-2 reuse-check flagged it). A Tier-3 pull reached
// by a plain cache miss is not flagged and records nothing. The notice
// distinguishes a benign refresh (the slot was stale but the registry
// content still hashes to the prior pin) from a moved pin (the registry
// content changed — the reproducibility baseline shifted).
func (l *catalogLookup) recordRefetchNotice(uri, newDigest string) {
	if l.refetchPending == nil {
		return
	}
	priorPin, flagged := l.refetchPending[uri]
	if !flagged {
		return
	}
	delete(l.refetchPending, uri)
	if l.warn == nil {
		return
	}
	if newDigest == priorPin {
		l.warn(fmt.Sprintf("cache entry for %s was stale; refreshed from the registry (pin unchanged)", uri))
		return
	}
	l.warn(fmt.Sprintf("pin for %s moved %s -> %s; the reproducibility baseline changed", uri, priorPin, newDigest))
}

// tier2ReadBytes is the package-level Tier-2 cache primitive. Reads
// the cached entity bytes for a versioned URI from the workspace
// cache directory. Returns the bytes plus the standard
// closure.Resolution descriptor. The lazy lookup (NewLazyLookup)
// consumes the bytes to parse typed entities; the existing
// catalogLookup discards bytes and uses only the Resolution.
//
// Returns (nil, Resolution{}, false) when the cache directory is
// unset, when the URI carries no version pin, or when the cache slot
// is empty or unreadable.
func tier2ReadBytes(workspaceRoot, cacheDir, uri string) ([]byte, closure.Resolution, bool) {
	if cacheDir == "" {
		return nil, closure.Resolution{}, false
	}
	_, version, hasV := splitVersionedURI(uri)
	if !hasV || version == "" {
		return nil, closure.Resolution{}, false
	}
	slot := oci.CachePath(workspaceRoot, cacheDir, uri, version)
	bytes, err := readCacheSlotBytes(slot)
	if err != nil {
		return nil, closure.Resolution{}, false
	}
	return bytes, closure.Resolution{
		ResolvedVersion: version,
		Digest:          contentDigest(bytes),
		ResolutionPath:  "tier-2-cache",
	}, true
}

// tier2ReadVerified is the verifying Tier-2 cache read. It reads the cached
// entity bytes for a versioned URI and verifies them against expectedDigest —
// the lockfile pin — before returning, mirroring the source-artifact cache read
// in pkg/sourcecache (LookupAndExtract): read the slot, recompute the content
// digest, compare to the pin. A non-empty expectedDigest is REQUIRED — there is
// no skip mode. Integrity lives at the cache boundary, not in caller discipline:
// a reader that has a lockfile pin reads through this and cannot get back
// unverified bytes.
//
// Returns the verified bytes on a digest match. Returns ErrFetchNotFound when
// the cache slot is absent (the caller issues a pull-first remediation) and
// ErrFetchIntegrity when the cached content does not match the pin. An empty
// expectedDigest is a programming error at the call site (a read that cannot
// supply a pin is a gap to wire, never a silent skip) and is reported as such.
func tier2ReadVerified(workspaceRoot, cacheDir, uri, expectedDigest string) ([]byte, error) {
	if expectedDigest == "" {
		return nil, fmt.Errorf("catalog: tier-2 verified read of %s requires a non-empty expected digest", uri)
	}
	bytes, _, ok := tier2ReadBytes(workspaceRoot, cacheDir, uri)
	if !ok {
		return nil, fmt.Errorf("catalog: %s not present in the content cache; run `aggrega pull` first: %w", uri, oci.ErrFetchNotFound)
	}
	if got := contentDigest(bytes); got != expectedDigest {
		return nil, fmt.Errorf("catalog: cached %s content digest %s does not match lockfile pin %s: %w",
			uri, got, expectedDigest, oci.ErrFetchIntegrity)
	}
	return bytes, nil
}

// tier3PullCatalogEntity is the package-level Tier-3 OCI pull
// primitive. Pulls the catalog entity at uri from the registry via
// the supplied OCIPuller and writes the pulled bytes to the
// workspace cache slot. Returns the bytes plus the standard
// closure.Resolution descriptor.
//
// Returns (nil, Resolution{NotFound: true}, nil) when the puller is
// nil (hermetic-build path) or when the URI cannot be split into a
// valid OCI reference + tag. Returns a wrapped error on pull or
// cache-write failure.
func tier3PullCatalogEntity(workspaceRoot, cacheDir string, puller OCIPuller, uri string) ([]byte, closure.Resolution, error) {
	if puller == nil {
		return nil, closure.Resolution{NotFound: true}, nil
	}
	reference, tag, ok := SplitURIForOCI(uri)
	if !ok {
		return nil, closure.Resolution{NotFound: true}, nil
	}
	result, err := puller.Pull(context.Background(), reference, tag)
	if err != nil {
		if errors.Is(err, oci.ErrFetchNotFound) {
			return nil, closure.Resolution{NotFound: true}, nil
		}
		return nil, closure.Resolution{}, fmt.Errorf("catalog: tier-3 pull %s: %w", uri, err)
	}
	if err := writeEntityToCacheSlot(workspaceRoot, cacheDir, uri, tag, result); err != nil {
		return nil, closure.Resolution{}, fmt.Errorf("catalog: tier-3 cache write %s: %w", uri, err)
	}
	return result.Bytes, closure.Resolution{
		ResolvedVersion: tag,
		Digest:          contentDigest(result.Bytes),
		ResolutionPath:  "tier-3-pull",
	}, nil
}

func (l *catalogLookup) tier3Library(uri, kind string) (closure.Resolution, error) {
	if l.ociPuller == nil {
		return closure.Resolution{NotFound: true}, nil
	}
	reference, tag, ok := SplitURIForOCI(uri)
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
	digest := contentDigest(result.Bytes)
	l.recordRefetchNotice(uri, digest)
	return closure.Resolution{
		ResolvedVersion: tag,
		Digest:          digest,
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

// SplitURIForOCI splits a canonical entity URI into the (repository
// reference, tag) pair pkg/oci.Pull consumes. The reference is the
// URI with any trailing ":<version>" stripped; the tag is the
// version. Returns ok=false when no version is present (OCI pull
// requires a tag).
//
// Exported so it serves both the closure walker's Tier-3 dispatch
// AND the inverse of the publish-side reference construction the
// push CLI verb performs. The two sides MUST agree on URI shape:
// publish constructs <registry>/<kindLower>/<metadata.name>:<version>;
// SplitURIForOCI parses it back to (<registry>/<kindLower>/<metadata.name>,
// <version>). A round-trip unit test in the push verb's test suite
// pins this symmetry at code-review time so any future divergence
// surfaces before it becomes an end-to-end runtime mismatch.
func SplitURIForOCI(uri string) (reference, tag string, ok bool) {
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
