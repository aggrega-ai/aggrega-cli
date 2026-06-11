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
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// ValidateClosureScopedPillars validates ONLY the catalog Components and Features
// the closure reaches — the closure-scoped counterpart to the whole-catalog
// Pass-2 (validateSchemaPillarsPass). It intersects cl.Entities (by
// version-stripped name) with the loaded Components / Features and delegates to
// ValidateClosurePillars against the catalog's SchemaLibraries (local + bridged).
//
// A closure entity whose schema `$ref` names a library absent from the catalog —
// a broken library skipped at tolerant load, or a genuinely missing one — fails
// here: the hard in-closure library reject. An out-of-closure entity is never in
// cl.Entities, so a broken one is never validated and never build-fatal. Pass-2
// runs over the entities as loaded (pre-extends), exactly as the whole-catalog
// pass does.
func (c *Catalog) ValidateClosureScopedPillars(cl closure.Closure) error {
	comps := map[string]types.Component{}
	compSrc := map[string]string{}
	feats := map[string]types.Feature{}
	featSrc := map[string]string{}
	for _, e := range cl.Entities {
		name := lookupNameFromURI(e.URI)
		if comp, ok := c.Components[name]; ok {
			comps[name] = comp
			compSrc[name] = c.componentSources[name]
		}
		if feat, ok := c.Features[name]; ok {
			feats[name] = feat
			featSrc[name] = c.featureSources[name]
		}
	}
	return ValidateClosurePillars(comps, compSrc, feats, featSrc, c.SchemaLibraries)
}

// ResolveClosureExtends resolves the extends chain for ONLY the Components and
// Features the closure reaches, in sorted-name order, mutating them in place —
// the closure-scoped counterpart to ResolveAllExtends. An out-of-closure entity's
// extends is never resolved, so a broken out-of-closure parent never fails the
// build; an in-closure child whose parent is missing (skipped at tolerant load)
// or malformed fails loudly. Each child's full chain is re-resolved from the
// child via the catalog lookup, so an out-of-closure (unresolved) parent in the
// chain is still merged correctly. Names are deduped (cl.Entities may list one
// entity under several version-suffixed URIs) so no chain is double-merged.
func (c *Catalog) ResolveClosureExtends(cl closure.Closure) []*validator.ValidationError {
	compLookup := func(uri string) (types.Component, bool) { comp, ok := c.Components[uri]; return comp, ok }
	featLookup := func(uri string) (types.Feature, bool) { feat, ok := c.Features[uri]; return feat, ok }

	compSet := map[string]struct{}{}
	featSet := map[string]struct{}{}
	for _, e := range cl.Entities {
		name := lookupNameFromURI(e.URI)
		if _, ok := c.Components[name]; ok {
			compSet[name] = struct{}{}
		}
		if _, ok := c.Features[name]; ok {
			featSet[name] = struct{}{}
		}
	}
	compNames := make([]string, 0, len(compSet))
	for name := range compSet {
		compNames = append(compNames, name)
	}
	sort.Strings(compNames)
	featNames := make([]string, 0, len(featSet))
	for name := range featSet {
		featNames = append(featNames, name)
	}
	sort.Strings(featNames)

	var errs []*validator.ValidationError
	for _, name := range compNames {
		comp := c.Components[name]
		if comp.Spec.Extends == "" {
			continue
		}
		resolved, vErrs := validator.ResolveComponentExtends(comp, compLookup)
		if len(vErrs) > 0 {
			errs = append(errs, vErrs...)
			continue
		}
		c.Components[name] = resolved
	}
	for _, name := range featNames {
		feat := c.Features[name]
		if feat.Spec.Extends == "" {
			continue
		}
		resolved, vErrs := validator.ResolveFeatureExtends(feat, featLookup)
		if len(vErrs) > 0 {
			errs = append(errs, vErrs...)
			continue
		}
		c.Features[name] = resolved
	}
	return errs
}

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

// LibraryPuller is an optional OCIPuller capability: pulling a library by its
// host-less identity (bare name + kind), binding it to a physical location, and
// resolving its version when the caller supplies none — the first-pull case,
// where the puller lists the registry's tags and selects the highest. A puller
// implementing only Pull cannot Tier-3-pull a bare-name library; the bare-name
// path then surfaces NotFound (the hermetic build's nil puller, and Pull-only
// test pullers, both land there). floorVersion, when non-empty, is the asserted
// minimum used directly as the tag. The "latest" resolution happens here, at
// the network step, and is pinned in the lockfile — the build never lists or
// fetches.
type LibraryPuller interface {
	PullLibrary(ctx context.Context, name, kind, floorVersion string) (result *oci.PullResult, resolvedVersion string, err error)
}

// AggregatePuller is the optional puller capability for fetching a published
// Aggregate by its bare identity — the aggregate counterpart of LibraryPuller.
// It binds name → location → version (list-tags → highest, or an authored floor)
// and reports the resolved version, which the closure's tier-3 pins as the
// entity's metadata.version identity. A puller that does not implement it leaves
// a bare-name aggregate Tier-3 as NotFound (the hermetic build path passes a nil
// puller, so Tier-3 never fetches at build).
type AggregatePuller interface {
	PullAggregate(ctx context.Context, name, floorVersion string) (result *oci.PullResult, resolvedVersion string, err error)
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

	// registrySeeds carries pre-resolved registry entities (a registry-resolved
	// top-level spec.aggregate the triad resolver fetched at pull): URI →
	// {version, digest, tier}. The closure pins these as real registry entities
	// rather than version-less externals, without re-fetching. Empty on the build
	// and serve paths (the top-level resolves there via the cache→catalog bridge).
	registrySeeds map[string]closure.Resolution

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
func (c *Catalog) ClosureLookupWithReuse(ociPuller OCIPuller, workspaceRoot, cacheDir string, externals map[string][]byte, registrySeeds map[string]closure.Resolution, reuse *ReuseVerification) closure.Lookup {
	l := &catalogLookup{
		catalog:       c,
		ociPuller:     ociPuller,
		workspaceRoot: workspaceRoot,
		cacheDir:      cacheDir,
		externals:     externals,
		registrySeeds: registrySeeds,
	}
	if reuse != nil {
		l.priorPins = reuse.PriorPins
		l.refetchPending = map[string]string{}
		l.warn = reuse.Warn
	}
	return l
}

// LoadAggregate loads a resolved sub-aggregate's parsed body for transitive
// closure descent, satisfying closure.AggregateLoader. A catalog-resident
// Aggregate is read from the catalog's retained authored bytes — the same
// bytes its tier-1 digest pins. A pulled Aggregate is read from the content
// cache through the verifying read, which checks the cached bytes against the
// supplied pin digest before returning. Either way the descent walks the exact
// bytes the pin covers, never a re-serialization or an unverified read.
func (l *catalogLookup) LoadAggregate(uri, version, digest string) (*types.Aggregate, error) {
	name := lookupNameFromURI(uri)
	if data, ok := l.catalog.EntityBytes("Aggregate", name); ok {
		return resolver.LoadAggregateFromBytes(data, name)
	}
	data, err := tier2ReadVerified(l.workspaceRoot, l.cacheDir, versionedSlotURI(uri, version), digest)
	if err != nil {
		return nil, err
	}
	return resolver.LoadAggregateFromBytes(data, uri)
}

// versionedSlotURI returns the cache-slot key for an entity. The slot is keyed
// by the versioned URI; a reference that already carries its version
// (`name:version`, a host-bearing `host/path:version`) is used as-is, while a
// bare-name reference gets the resolved version appended. Centralising the rule
// keeps the bare-name case (a registry-published aggregate referenced by
// identity) and the versioned-URI case from clashing on a double-appended tag.
func versionedSlotURI(uri, version string) string {
	if _, _, hasV := splitVersionedURI(uri); hasV {
		return uri
	}
	if version == "" {
		return uri
	}
	return uri + ":" + version
}

func (l *catalogLookup) ResolveCatalogEntity(uri string) (closure.Resolution, error) {
	if res, ok := l.lookupExternal(uri); ok {
		return res, nil
	}
	if res, ok := l.lookupRegistry(uri); ok {
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

// lookupRegistry returns a pre-resolved registry entity the caller seeded — a
// registry-resolved top-level spec.aggregate the triad resolver fetched at pull.
// It pins the entity as a real registry entity (its metadata.version + digest)
// rather than letting it fall to the version-less external tier, so the lockfile
// records a real version and the build-side bridge re-derives the same.
func (l *catalogLookup) lookupRegistry(uri string) (closure.Resolution, bool) {
	res, ok := l.registrySeeds[uri]
	return res, ok
}

// ResolveAggregate resolves a sub-aggregate reference (a KindHint=="aggregate"
// closure entity), satisfying closure.Lookup. It cascades like
// ResolveCatalogEntity — external → tier-1 → tier-2 — but a bare-name miss falls
// to tier3Aggregate (the AggregatePuller capability), not the tag-requiring
// generic tier-3. This is the aggregate counterpart of ResolveSchemaLibrary, so
// a registry-published sub-aggregate referenced by bare identity resolves at
// pull (and the puller is nil on the hermetic build path, so it never fetches at
// build — the sub resolves there via the build-side cache→catalog bridge).
func (l *catalogLookup) ResolveAggregate(uri string) (closure.Resolution, error) {
	if res, ok := l.lookupExternal(uri); ok {
		return res, nil
	}
	if res, ok := l.tier1CatalogEntity(uri); ok {
		return res, nil
	}
	if _, res, ok := l.tier2(uri); ok {
		return res, nil
	}
	return l.tier3Aggregate(uri)
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
	// FeatureRecipeByURI, which implement the provider-qualified /
	// bare-name resolution ladder over the identity-keyed maps. The
	// closure walker passes recipe URIs per Manifest mapping values
	// (bare or provider-qualified), never spec.implements. The retained
	// bytes are keyed by the full provider-scoped identity, so the
	// digest below is the resolved entity's own bytes even when another
	// provider ships a recipe of the same name.
	if r, ok := l.catalog.RecipeByURI(uri); ok {
		return l.tier1Resolution("Recipe", recipeIdentityKey(r.Spec.Provider, r.Metadata.Name, r.Metadata.Version), r.Metadata.Version, version), true
	}
	if fr, ok := l.catalog.FeatureRecipeByURI(uri); ok {
		return l.tier1Resolution("FeatureRecipe", recipeIdentityKey(fr.Spec.Provider, fr.Metadata.Name, fr.Metadata.Version), fr.Metadata.Version, version), true
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
	bytes, res, ok := tier2ReadSlotUnverified(l.workspaceRoot, l.cacheDir, uri)
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

// tier2ReadSlotUnverified reads the cached entity bytes for a versioned URI
// from the workspace cache directory WITHOUT verifying them against any pin.
//
// DO NOT call this from a new read path. Integrity lives at the cache boundary:
// every cache read that returns bytes to a consumer must verify them against a
// lockfile pin via tier2ReadVerified. This unverified primitive survives for
// exactly two internal callers that verify the bytes themselves and must see the
// raw slot to do so:
//   - tier2ReadVerified — recomputes the content digest and compares to the
//     required lockfile pin (the one verifying read all consumers route through).
//   - catalogLookup.tier2 — the pull reuse-check, which compares the slot digest
//     to the PRIOR lockfile pin to decide re-fetch vs reuse (build reaches it
//     with no prior pin and relies on the closure-level lockfile.Verify gate).
//
// A new caller that wants cache bytes is a gap to wire through tier2ReadVerified,
// never a reason to reach for this. (See the verify-on-read posture in
// pkg/sourcecache.LookupAndExtract, which this mirrors.)
//
// Returns (nil, Resolution{}, false) when the cache directory is
// unset, when the URI carries no version pin, or when the cache slot
// is empty or unreadable.
func tier2ReadSlotUnverified(workspaceRoot, cacheDir, uri string) ([]byte, closure.Resolution, bool) {
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
	bytes, _, ok := tier2ReadSlotUnverified(workspaceRoot, cacheDir, uri)
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

	// Bare-name identity (the form discovery records): resolve location + version
	// via the LibraryPuller capability — name→location, then list-tags→highest
	// (when the URI carries no version) or the asserted floor as the tag (when a
	// consumer's `@version` threaded a `name:floor` URI here). The cache
	// slot is keyed `<name>:<version>` — the symmetric key the build-side bridge
	// reads through ReadPinnedLibrary.
	if lp, ok := l.ociPuller.(LibraryPuller); ok {
		name, floor, hasFloor := splitVersionedURI(uri)
		result, resolvedVersion, err := lp.PullLibrary(context.Background(), name, kind, floor)
		if err != nil {
			if errors.Is(err, oci.ErrFetchNotFound) {
				return closure.Resolution{NotFound: true}, nil
			}
			return closure.Resolution{}, fmt.Errorf("catalog: tier-3 pull %s %s: %w", kind, name, err)
		}
		// For a bare-name reference (no consumer @version floor), pin the fetched
		// library's metadata.version (identity), not the resolver-selected tag
		// (location): the tag drove discovery (list-tags → highest) but the pinned
		// + cached identity is the body's metadata.version, matching the build-side
		// tier-1 derivation so a tag != metadata.version publish does not drift. A
		// floored reference keeps the floor as the pin — the consumer asked for that
		// version, and minimum-version selection (which overrides ResolvedVersion to
		// the floor) and the cache key must agree.
		pinnedVersion := resolvedVersion
		if !hasFloor {
			if mv := entityMetadataVersion(result.Bytes); mv != "" {
				pinnedVersion = mv
			}
		}
		versionedKey := name + ":" + pinnedVersion
		if err := writeEntityToCacheSlot(l.workspaceRoot, l.cacheDir, versionedKey, pinnedVersion, result); err != nil {
			return closure.Resolution{}, fmt.Errorf("catalog: tier-3 cache write %s %s: %w", kind, name, err)
		}
		digest := contentDigest(result.Bytes)
		l.recordRefetchNotice(uri, digest)
		return closure.Resolution{
			ResolvedVersion: pinnedVersion,
			Digest:          digest,
			ResolutionPath:  "tier-3-pull",
		}, nil
	}

	// Pull-only puller (no library capability): the legacy full-reference path for
	// an already-located, versioned URI.
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

// tier3Aggregate is the bare-name Aggregate registry tier, the aggregate
// counterpart of tier3Library. It binds a bare-name reference to a location via
// the AggregatePuller capability (name → location → version), caches the slot
// under name:metadata.version, and pins the fetched bytes' metadata.version
// (identity), not the OCI tag it was fetched under (location) — so a registry
// sub-aggregate's pinned version is identical to the tier-1 (bridged) version
// build re-derives, and lockfile.Verify never drifts on a tag-vs-version
// mismatch. A nil puller (the hermetic build path) or a puller without the
// AggregatePuller capability resolves NotFound, so the registry tier never
// fetches at build.
func (l *catalogLookup) tier3Aggregate(uri string) (closure.Resolution, error) {
	_, version, digest, found, err := PullAndCacheAggregate(l.workspaceRoot, l.cacheDir, l.ociPuller, uri)
	if err != nil {
		return closure.Resolution{}, err
	}
	if !found {
		return closure.Resolution{NotFound: true}, nil
	}
	l.recordRefetchNotice(uri, digest)
	return closure.Resolution{
		ResolvedVersion: version,
		Digest:          digest,
		ResolutionPath:  "tier-3-pull",
	}, nil
}

// PullAndCacheAggregate fetches a bare-name published Aggregate via the puller's
// AggregatePuller capability, caches it under name:metadata.version, and returns
// the verified bytes + resolved version (the fetched metadata.version identity)
// + content digest. found is false when the puller is nil (the hermetic build
// path), lacks the AggregatePuller capability, or the registry has no such
// aggregate (ErrFetchNotFound) — so a build, or an offline pull, never networks.
// It is the single fetch+cache+pin primitive shared by the closure's
// tier3Aggregate (sub path) and the triad resolver's Tier-3 seam (top-level
// path): one cache layout, one identity rule, two call sites.
func PullAndCacheAggregate(workspaceRoot, cacheDir string, puller OCIPuller, ref string) (data []byte, version, digest string, found bool, err error) {
	if puller == nil {
		return nil, "", "", false, nil
	}
	ap, ok := puller.(AggregatePuller)
	if !ok {
		return nil, "", "", false, nil
	}
	name, floor, _ := splitVersionedURI(ref)
	result, fetchVersion, perr := ap.PullAggregate(context.Background(), name, floor)
	if perr != nil {
		if errors.Is(perr, oci.ErrFetchNotFound) {
			return nil, "", "", false, nil
		}
		return nil, "", "", false, fmt.Errorf("catalog: tier-3 pull Aggregate %s: %w", name, perr)
	}
	// Pin the fetched entity's metadata.version (identity), not the OCI tag it
	// was fetched under (fetchVersion, a location coordinate now discarded) — so
	// the pin matches the build-side tier-1 (bridged) derivation and a
	// tag != metadata.version publish does not drift. Fall back to the fetch
	// coordinate only for a body that carries no version.
	version = entityMetadataVersion(result.Bytes)
	if version == "" {
		version = fetchVersion
	}
	versionedKey := name + ":" + version
	if werr := writeEntityToCacheSlot(workspaceRoot, cacheDir, versionedKey, version, result); werr != nil {
		return nil, "", "", false, fmt.Errorf("catalog: tier-3 cache write Aggregate %s: %w", name, werr)
	}
	return result.Bytes, version, contentDigest(result.Bytes), true, nil
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

// EntityNameFromURI returns the catalog index name for an entity URI — the URI
// with any trailing ":<version>" pin stripped (metadata.name, which may itself
// contain "/" separators). Exported for callers outside the package — the
// closure-scoped library-discovery filter intersects closure entity URIs with the
// name-keyed catalog maps through this.
func EntityNameFromURI(uri string) string {
	return lookupNameFromURI(uri)
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
