// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
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
		if key, ok, _ := c.resolveComponentKey(e.URI); ok {
			comps[key] = c.Components[key]
			compSrc[key] = c.componentSources[key]
		}
		if key, ok, _ := c.resolveFeatureKey(e.URI); ok {
			feats[key] = c.Features[key]
			featSrc[key] = c.featureSources[key]
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
	compLookup := func(uri string) (types.Component, bool) {
		key, ok, _ := c.resolveComponentKey(uri)
		if !ok {
			return types.Component{}, false
		}
		return c.Components[key], true
	}
	featLookup := func(uri string) (types.Feature, bool) {
		key, ok, _ := c.resolveFeatureKey(uri)
		if !ok {
			return types.Feature{}, false
		}
		return c.Features[key], true
	}

	compSet := map[string]struct{}{}
	featSet := map[string]struct{}{}
	for _, e := range cl.Entities {
		if key, ok, _ := c.resolveComponentKey(e.URI); ok {
			compSet[key] = struct{}{}
		}
		if key, ok, _ := c.resolveFeatureKey(e.URI); ok {
			featSet[key] = struct{}{}
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

// EntityPuller is the optional puller capability for fetching a core catalog
// entity (Component / Feature / Recipe / FeatureRecipe) by its bare
// identity — the entity counterpart of LibraryPuller / AggregatePuller. It binds
// the identity to its registry location (`<registry>/<OCILocation(kind, …)>`),
// so a provider-scoped recipe identity is not misparsed with its provider as the
// registry host. A puller that does not implement it (the hermetic-build nil
// puller, or a Pull-only test puller) leaves the bare-identity entity tier on the
// legacy already-located reference path. kind is the catalog kind
// ("Component"/"Feature"/"Recipe"/"FeatureRecipe"); version is the tag.
type EntityPuller interface {
	PullEntity(ctx context.Context, identity, kind, floorVersion string) (result *oci.PullResult, resolvedVersion string, err error)
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

	// priorPins, when non-nil, makes the pull HONOR a prior lockfile: a closure
	// entity carrying a pin resolves to the pin's exact ResolvedVersion (never
	// floated to list-tags→highest), and a Tier-2 cache slot is reused only when
	// its content digest matches the pin's Digest; a registry whose pinned-version
	// bytes no longer match the pin Digest HALTS (ErrFetchIntegrity), never
	// re-pinned. refetchPending flags a drifted cache slot so the Tier-3 re-fetch
	// records a notice via warn; both are nil unless a ReuseVerification was
	// supplied (the hermetic build and serve paths leave them nil and reuse cache
	// slots unconditionally — the build is frozen against a complete lockfile).
	priorPins      map[string]Pin
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
	PriorPins map[string]Pin
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
	// The sub-aggregate loader interface carries the content pin but not the
	// manifest digest; the manifest self-verify step is skipped, while the
	// persisted manifest still authorities the content (VerifyContentAgainstManifest)
	// and the content pin remains the independent root of trust.
	data, err := tier2ReadVerified(l.workspaceRoot, l.cacheDir, versionedSlotURI(uri, version), digest, "")
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

func (l *catalogLookup) ResolveCatalogEntity(uri, kindHint string) (closure.Resolution, error) {
	if res, ok := l.lookupExternal(uri); ok {
		return res, nil
	}
	if res, ok := l.lookupRegistry(uri); ok {
		return res, nil
	}
	if res, ok := l.tier1CatalogEntity(uri); ok {
		return res, nil
	}
	// Honor a prior pin: resolve the pinned EXACT version (never float to
	// list-tags→highest) and verify the resolved bytes against the pin's digest.
	fetchURI := l.pinnedFetchURI(uri)
	if _, res, ok := l.tier2(fetchURI); ok {
		return l.honorPinDigest(uri, res)
	}
	_, res, err := l.tier3CatalogEntity(fetchURI, ociKindForHint(kindHint))
	if err != nil {
		return res, err
	}
	return l.honorPinDigest(uri, res)
}

// pinnedFetchURI returns the URI to resolve for an entity that carries a prior
// lockfile pin: the pin's exact ResolvedVersion appended to a bare reference, so
// a Tier-3 pull fetches the pinned version verbatim (the proven exact path)
// rather than listing tags and floating to the highest. A reference that already
// carries a version, or one with no prior pin (un-pinned: resolve fresh + add),
// is returned unchanged. This is the float→honor flip; floating happens only for
// an un-pinned reference (or under `pull --upgrade`, which supplies no pins).
func (l *catalogLookup) pinnedFetchURI(uri string) string {
	if l.priorPins == nil {
		return uri
	}
	if _, _, hasVersion := splitVersionedURI(uri); hasVersion {
		return uri
	}
	pin, ok := l.priorPins[uri]
	if !ok || pin.ResolvedVersion == "" {
		return uri
	}
	return uri + ":" + pin.ResolvedVersion
}

// honorPinDigest enforces honor≠launder: when the entity carried a prior pin and
// the resolved bytes no longer hash to the pin's digest, the registry's
// pinned-version bytes have changed under the pin — HALT with ErrFetchIntegrity
// rather than re-resolving and laundering the changed bytes into a new pin.
// Keyed by the original (caller's) URI, so a bare reference verifies against its
// bare-keyed pin regardless of the versioned form used to fetch.
func (l *catalogLookup) honorPinDigest(uri string, res closure.Resolution) (closure.Resolution, error) {
	if l.priorPins == nil || res.NotFound {
		return res, nil
	}
	pin, ok := l.priorPins[uri]
	if !ok || pin.Digest == "" || res.Digest == pin.Digest {
		return res, nil
	}
	return closure.Resolution{}, fmt.Errorf(
		"%w: pinned entity %q: registry now serves digest %s for the pinned version, not the pinned digest %s; run `aggrega pull --upgrade` to update the pin",
		oci.ErrFetchIntegrity, uri, res.Digest, pin.Digest)
}

// ociKindForHint maps a closure KindHint to the catalog kind the OCI location
// rule expects. An unmapped or empty hint yields "", which leaves the entity
// Tier-3 pull on the legacy already-located reference path (no name→location
// binding).
func ociKindForHint(hint string) string {
	switch hint {
	case "component":
		return "Component"
	case "feature":
		return "Feature"
	case "recipe":
		return "Recipe"
	case "feature_recipe":
		return "FeatureRecipe"
	case "plugin":
		return "Plugin"
	default:
		return ""
	}
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
	fetchURI := l.pinnedFetchURI(uri)
	if _, res, ok := l.tier2(fetchURI); ok {
		return l.honorPinDigest(uri, res)
	}
	res, err := l.tier3Aggregate(fetchURI)
	if err != nil {
		return res, err
	}
	return l.honorPinDigest(uri, res)
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
	fetchURI := l.pinnedFetchURI(uri)
	if _, res, ok := l.tier2(fetchURI); ok {
		res, err := l.honorPinDigest(uri, res)
		return closure.LibraryResolution{Resolution: res}, err
	}
	res, err := l.tier3Library(fetchURI, "SchemaLibrary")
	if err != nil {
		return closure.LibraryResolution{Resolution: res}, err
	}
	res, err = l.honorPinDigest(uri, res)
	return closure.LibraryResolution{Resolution: res}, err
}

func (l *catalogLookup) ResolveMappingLibrary(uri string) (closure.LibraryResolution, error) {
	if res, entries, ok := l.tier1MappingLibrary(uri); ok {
		return closure.LibraryResolution{Resolution: res, Entries: entries}, nil
	}
	fetchURI := l.pinnedFetchURI(uri)
	if _, res, ok := l.tier2(fetchURI); ok {
		res, err := l.honorPinDigest(uri, res)
		return closure.LibraryResolution{Resolution: res}, err
	}
	res, err := l.tier3Library(fetchURI, "MappingLibrary")
	if err != nil {
		return closure.LibraryResolution{Resolution: res}, err
	}
	res, err = l.honorPinDigest(uri, res)
	return closure.LibraryResolution{Resolution: res}, err
}

func (l *catalogLookup) ResolveWorkloadLibrary(uri string) (closure.LibraryResolution, error) {
	if res, entries, ok := l.tier1WorkloadLibrary(uri); ok {
		return closure.LibraryResolution{Resolution: res, Entries: entries}, nil
	}
	fetchURI := l.pinnedFetchURI(uri)
	if _, res, ok := l.tier2(fetchURI); ok {
		res, err := l.honorPinDigest(uri, res)
		return closure.LibraryResolution{Resolution: res}, err
	}
	res, err := l.tier3Library(fetchURI, "WorkloadLibrary")
	if err != nil {
		return closure.LibraryResolution{Resolution: res}, err
	}
	res, err = l.honorPinDigest(uri, res)
	return closure.LibraryResolution{Resolution: res}, err
}

func (l *catalogLookup) ResolvePlugin(uri, version string) (closure.Resolution, error) {
	// Embed tier: a core plugin embedded in the binary resolves offline by its
	// bare identity (<socket>/<name>). Its reference digest is derived from the
	// embedded bytes on demand — never stored — so pull records a lockfile entry
	// for a core plugin with no registry round-trip, and the closure walker no
	// longer drops it as a NotFound miss. The lockfile carries the derived digest
	// in the existing digest field; identity stays the bare plugin identity.
	if b, ok := embedded.Lookup(uri); ok {
		return embeddedPluginResolution(contentDigest(b)), nil
	}
	// A Plugin entity's .wasm artifact is identified by the entity's bare identity
	// (<socket>/<name>). The .wasm is not a standalone artifact: it rides inline,
	// as a layer, in the single plugin manifest published under the entity's
	// version tag. Resolve it from that manifest by version rather than falling
	// through to the entity-resolving catalog tiers.
	return l.resolveVersionedPluginArtifact(uri, version)
}

// pluginArtifactReference binds a plugin's bare identity (<socket>/<name>) to the
// physical OCI repository reference its .wasm is fetched from —
// `<registry>/plugin/<socket>/<name>` — through the single location rule
// (entityReference → OCILocation), so the plugin artifact fetch location is
// derived exactly where the entity's own location is, never carried in the entity.
// The configured registry comes from the puller (the same registry the entity
// pull binds against); a puller that cannot report one, or an identity that is
// already registry-qualified, is refused loudly rather than fetched from a
// double-prefixed location.
func (l *catalogLookup) pluginArtifactReference(identity string) (string, error) {
	registry := ""
	if rh, ok := l.ociPuller.(interface{ RegistryHost() string }); ok {
		registry = rh.RegistryHost()
	}
	if registry == "" {
		return "", fmt.Errorf("catalog: cannot resolve plugin artifact %q: no configured registry to bind its bare identity to a location", identity)
	}
	return entityReference(registry, "Plugin", identity)
}

// resolveVersionedPluginArtifact resolves a plugin's bare-identity .wasm artifact
// reference (<socket>/<name>) to the executable carried inline in the single
// plugin manifest published under the version tag. The .wasm is a layer of the
// manifest at <registry>/plugin/<socket>/<name>:<version>, not a standalone
// content-addressed artifact, so:
//
//   - Cache / build-offline: when a prior lockfile pin names the content digest,
//     read the (uri, contentDigest) slot and self-verify it. A nil puller with no
//     readable slot resolves to NotFound (the build-loader gate halts).
//   - Lock-time pull: fetch the manifest by version, take its inline .wasm layer,
//     DERIVE the content pin from those bytes, and cache both blobs under the
//     derived pin — the same slot shape the build re-reads.
//
// The resolution pins ResolvedVersion = Digest = the derived content digest and
// ManifestDigest = the manifest digest. A prior pin is HONORED, not laundered: a
// re-pull whose derived content or served manifest no longer matches the recorded
// pin halts with oci.ErrFetchIntegrity rather than re-pinning the changed bytes.
func (l *catalogLookup) resolveVersionedPluginArtifact(uri, version string) (closure.Resolution, error) {
	// Cache-first: a prior lockfile pin names the content slot to read. Without a
	// prior pin the slot cannot be addressed (its key is the yet-unknown content
	// digest), so a fresh resolution must pull to learn the pin.
	if prior, ok := l.priorPins[uri]; ok && prior.Digest != "" {
		if res, hit, err := l.readCachedPluginArtifact(uri, prior.Digest); err != nil || hit {
			return res, err
		}
	}

	if l.ociPuller == nil {
		return closure.Resolution{NotFound: true}, nil
	}

	reference, err := l.pluginArtifactReference(uri)
	if err != nil {
		return closure.Resolution{}, err
	}
	result, err := l.ociPuller.Pull(context.Background(), reference, version)
	if err != nil {
		if errors.Is(err, oci.ErrFetchNotFound) {
			return closure.Resolution{NotFound: true}, nil
		}
		if rem := oci.RemediationFor(err, registryhost.Of(reference)); rem != "" {
			return closure.Resolution{}, fmt.Errorf("catalog: tier-3 pull plugin artifact %s: %w\n  %s", uri, err, rem)
		}
		return closure.Resolution{}, fmt.Errorf("catalog: tier-3 pull plugin artifact %s: %w", uri, err)
	}
	contentPin := contentDigest(result.Bytes)
	// Honor≠launder: a prior pin's content and manifest digests are independent
	// records the re-pull must still satisfy. A drifted registry serving different
	// bytes (or a different manifest) under the same version halts rather than
	// re-pinning silently.
	if prior, ok := l.priorPins[uri]; ok {
		if prior.Digest != "" && contentPin != prior.Digest {
			return closure.Resolution{}, fmt.Errorf("catalog: pulled plugin artifact %s content digest %s does not match pin %s: %w",
				uri, contentPin, prior.Digest, oci.ErrFetchIntegrity)
		}
		if prior.ManifestDigest != "" {
			if verr := oci.VerifyManifestDigest(result.ManifestBytes, prior.ManifestDigest); verr != nil {
				return closure.Resolution{}, fmt.Errorf("catalog: pulled manifest for plugin artifact %s: %w", uri, verr)
			}
		}
	}
	if err := l.cachePluginArtifact(uri, contentPin, result); err != nil {
		return closure.Resolution{}, err
	}
	return pinnedPluginResolution(contentPin, result.Digest), nil
}

// readCachedPluginArtifact attempts to satisfy a plugin-artifact resolution from
// the cache slot keyed by (uri, contentPin). It reports (resolution, hit, error):
// a hit means both blobs were present and self-verified; hit==false with a nil
// error means the slot is absent or incomplete and the caller should re-pull; a
// non-nil error is a verification failure that HALTS (a tampered slot is never
// silently re-pulled around).
//
// The manifest is the root of trust: when a prior lockfile recorded the manifest
// digest, the stored manifest is self-verified against that independent pin — the
// manifest is anchored to the lockfile, not re-derived from the cached content.
// The content is then trusted only against the manifest's layer digest, and
// finally against contentPin as defense in depth.
func (l *catalogLookup) readCachedPluginArtifact(uri, contentPin string) (closure.Resolution, bool, error) {
	if l.cacheDir == "" {
		return closure.Resolution{}, false, nil
	}
	bytes, err := oci.ReadCached(l.workspaceRoot, l.cacheDir, uri, contentPin)
	if err != nil {
		if errors.Is(err, oci.ErrFetchNotFound) {
			return closure.Resolution{}, false, nil
		}
		return closure.Resolution{}, false, fmt.Errorf("catalog: read cached plugin artifact %s: %w", uri, err)
	}
	// A slot with a content blob but no manifest blob is incomplete; treat it as a
	// cache miss so the re-pull rewrites both blobs (offline, the miss surfaces the
	// pull-first remediation rather than trusting a half-populated slot).
	manifestBytes, mrerr := oci.ReadCachedManifest(l.workspaceRoot, l.cacheDir, uri, contentPin)
	switch {
	case mrerr == nil:
		if prior, ok := l.priorPins[uri]; ok && prior.ManifestDigest != "" {
			if verr := oci.VerifyManifestDigest(manifestBytes, prior.ManifestDigest); verr != nil {
				return closure.Resolution{}, false, fmt.Errorf("catalog: cached manifest for plugin artifact %s: %w", uri, verr)
			}
		}
		if verr := oci.VerifyContentAgainstManifest(manifestBytes, bytes); verr != nil {
			return closure.Resolution{}, false, fmt.Errorf("catalog: cached plugin artifact %s: %w", uri, verr)
		}
		if got := contentDigest(bytes); got != contentPin {
			return closure.Resolution{}, false, fmt.Errorf("catalog: cached plugin artifact %s content digest %s does not match pin %s: %w",
				uri, got, contentPin, oci.ErrFetchIntegrity)
		}
		return pinnedPluginResolution(contentPin, contentDigest(manifestBytes)), true, nil
	case errors.Is(mrerr, oci.ErrFetchNotFound):
		return closure.Resolution{}, false, nil
	default:
		return closure.Resolution{}, false, fmt.Errorf("catalog: read cached manifest for plugin artifact %s: %w", uri, mrerr)
	}
}

// cachePluginArtifact writes a pulled plugin artifact's content and manifest as
// the two self-verifying blobs of the (uri, contentPin) cache slot the build
// re-reads. The manifest is stored so a later offline read can re-derive the
// manifest digest from a stored artifact rather than trusting a recorded value
// with nothing to check it against. A nil cache dir is a no-op.
func (l *catalogLookup) cachePluginArtifact(uri, contentPin string, result *oci.PullResult) error {
	if l.cacheDir == "" {
		return nil
	}
	if err := oci.WriteCached(l.workspaceRoot, l.cacheDir, uri, contentPin, result.Bytes); err != nil {
		return fmt.Errorf("catalog: cache write plugin artifact %s: %w", uri, err)
	}
	if len(result.ManifestBytes) > 0 {
		if err := oci.WriteCachedManifest(l.workspaceRoot, l.cacheDir, uri, contentPin, result.ManifestBytes); err != nil {
			return fmt.Errorf("catalog: cache write plugin manifest %s: %w", uri, err)
		}
	}
	return nil
}

// pinnedPluginResolution is the resolution descriptor for a content-pinned .wasm
// artifact: ResolvedVersion and Digest are both the pinned content digest, so
// the lockfile pin (Digest) and the cache-read key (ResolvedVersion) name the
// same content. ManifestDigest is the .wasm's OCI manifest digest — the verify
// subject, distinct from the content pin. ResolutionPath marks it a
// registry-tier pin, like every other above-Tier-1 resolution the closure
// records.
func pinnedPluginResolution(pinDigest, manifestDigest string) closure.Resolution {
	return closure.Resolution{
		ResolvedVersion: pinDigest,
		Digest:          pinDigest,
		ManifestDigest:  manifestDigest,
		ResolutionPath:  "tier-3-pull",
	}
}

// embeddedPluginResolution is the resolution descriptor for a core .wasm
// artifact resolved from the binary's embed tier: ResolvedVersion and Digest are
// both the reference digest derived from the embedded bytes, so the lockfile
// records a real content digest for a core plugin without any registry access.
// ManifestDigest is empty — an embedded artifact carries no registry manifest to
// verify against. ResolutionPath marks it a resolved (above-Tier-1) pin, so the
// closure walker records it rather than skipping it as a NotFound miss.
func embeddedPluginResolution(refDigest string) closure.Resolution {
	return closure.Resolution{
		ResolvedVersion: refDigest,
		Digest:          refDigest,
		ResolutionPath:  "tier-0-embed",
	}
}

func (l *catalogLookup) tier1CatalogEntity(uri string) (closure.Resolution, bool) {
	_, version, _ := splitVersionedURI(uri)

	if key, ok, _ := l.catalog.resolveComponentKey(uri); ok {
		c := l.catalog.Components[key]
		return l.tier1Resolution("Component", c.Metadata.Name, c.Metadata.Version, version), true
	}
	if key, ok, _ := l.catalog.resolveFeatureKey(uri); ok {
		f := l.catalog.Features[key]
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
		return l.tier1Resolution("Recipe", recipeIdentityKey(r.Spec.Capability, r.Metadata.Name, r.Metadata.Version), r.Metadata.Version, version), true
	}
	if fr, ok := l.catalog.FeatureRecipeByURI(uri); ok {
		return l.tier1Resolution("FeatureRecipe", recipeIdentityKey(fr.Spec.Capability, fr.Metadata.Name, fr.Metadata.Version), fr.Metadata.Version, version), true
	}
	// A Plugin is keyed by its coordinate identity (<pluginType>/<name>:version).
	// A workspace plugin socket or a recipe pipeline step references a
	// Plugin ENTITY URI, which the closure walk resolves here; a local
	// (Tier-1) entity must resolve at Tier-1 like every other local entity rather
	// than falling through to a fatal Tier-3 NotFound.
	if key, ok, _ := l.catalog.resolvePluginKey(uri); ok {
		wp := l.catalog.Plugins[key]
		return l.tier1Resolution("Plugin", wp.Coordinate(), wp.Metadata.Version, version), true
	}
	if key, ok, _ := l.catalog.resolveAggregateKey(uri); ok {
		a := l.catalog.Aggregates[key]
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

func (l *catalogLookup) tier1WorkloadLibrary(uri string) (closure.Resolution, map[string]struct{}, bool) {
	name := schemaLibraryNameFromURI(uri)
	lib, ok := l.catalog.WorkloadLibraries[name]
	if !ok {
		return closure.Resolution{}, nil, false
	}
	resolved := lib.Metadata.Version
	if _, v, hasV := splitVersionedURI(uri); hasV && v != "" {
		resolved = v
	}
	entries := map[string]struct{}{}
	for n := range lib.Spec.Workloads.All() {
		entries[n] = struct{}{}
	}
	return l.tier1Resolution("WorkloadLibrary", lib.Metadata.Name, lib.Metadata.Version, resolved), entries, true
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
	if !havePin || contentDigest(bytes) == pin.Digest {
		return bytes, res, true
	}
	// Re-pull reuse-check: the cached slot no longer hashes to the pin
	// the prior lockfile recorded. Skip the hit and flag the URI so the
	// Tier-3 re-fetch (which rewrites the slot) records a drift notice.
	// Re-establishing the pin from authoritative registry bytes is the
	// correct action where the registry is reachable, rather than
	// laundering the drifted slot into the new lockfile.
	l.refetchPending[uri] = pin.Digest
	return nil, closure.Resolution{}, false
}

func (l *catalogLookup) tier3CatalogEntity(uri, kind string) ([]byte, closure.Resolution, error) {
	bytes, res, err := tier3PullCatalogEntity(l.workspaceRoot, l.cacheDir, l.ociPuller, uri, kind)
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
// the cache slot is absent OR when the slot carries content but no persisted
// OCI manifest (a catalog entity is always OCI-distributed, so a manifest-less
// slot is an incomplete cache entry, not a content-pin-only fallback) — in
// either case the caller issues a pull-first remediation. Returns
// ErrFetchIntegrity when the cached content does not match the pin. An empty
// expectedDigest is a programming error at the call site (a read that cannot
// supply a pin is a gap to wire, never a silent skip) and is reported as such.
func tier2ReadVerified(workspaceRoot, cacheDir, uri, expectedDigest, expectedManifestDigest string) ([]byte, error) {
	if expectedDigest == "" {
		return nil, fmt.Errorf("catalog: tier-2 verified read of %s requires a non-empty expected digest", uri)
	}
	bytes, res, ok := tier2ReadSlotUnverified(workspaceRoot, cacheDir, uri)
	if !ok {
		return nil, fmt.Errorf("catalog: %s not present in the content cache; run `aggrega pull` first: %w", uri, oci.ErrFetchNotFound)
	}
	// Manifest authority. Every registry pull persists the OCI manifest beside the
	// content; it is the root the content is verified against. Verify the manifest
	// against the independent pin (when one is recorded — a registry-resolved
	// entity), then verify the content against the manifest's layer digest, so a
	// forged content+content-pin pair cannot survive an untampered manifest. A
	// catalog entity is always OCI-distributed, so a slot that carries content but
	// no persisted manifest is an incomplete cache entry, not a content-pin-only
	// fallback: treat it as a miss so the caller re-pulls, mirroring the
	// plugin-artifact read.
	manifestBytes, mrerr := oci.ReadCachedManifest(workspaceRoot, cacheDir, uri, res.ResolvedVersion)
	switch {
	case mrerr == nil:
		if expectedManifestDigest != "" {
			if err := oci.VerifyManifestDigest(manifestBytes, expectedManifestDigest); err != nil {
				return nil, fmt.Errorf("catalog: cached manifest for %s: %w", uri, err)
			}
		}
		// A plugin entity's content is the manifest's CONFIG blob (the entity YAML),
		// not a layer — the single plugin manifest carries the .wasm as its layer.
		// Verify the cached entity against the descriptor that actually commits to
		// it: the config for a plugin manifest, the content layer for every other
		// entity kind. The manifest itself declares which, so the routing needs no
		// kind thread and non-plugin entities keep the layer-based verify unchanged.
		if oci.IsPluginManifest(manifestBytes) {
			if err := oci.VerifyConfigAgainstManifest(manifestBytes, bytes); err != nil {
				return nil, fmt.Errorf("catalog: cached %s: %w", uri, err)
			}
		} else if err := oci.VerifyContentAgainstManifest(manifestBytes, bytes); err != nil {
			return nil, fmt.Errorf("catalog: cached %s: %w", uri, err)
		}
	case errors.Is(mrerr, oci.ErrFetchNotFound):
		return nil, fmt.Errorf("catalog: %s cached without its OCI manifest; run `aggrega pull` first: %w", uri, oci.ErrFetchNotFound)
	default:
		return nil, fmt.Errorf("catalog: read cached manifest for %s: %w", uri, mrerr)
	}
	// Content still matches the recorded content pin.
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
func tier3PullCatalogEntity(workspaceRoot, cacheDir string, puller OCIPuller, uri, kind string) ([]byte, closure.Resolution, error) {
	if puller == nil {
		return nil, closure.Resolution{NotFound: true}, nil
	}
	// A bare catalog identity carries no registry or kind segment. When the kind
	// is known and the puller can bind it, fetch through EntityPuller — the
	// pull-side mirror of push (`<registry>/<OCILocation(kind, …)>`) — resolving a
	// version-less reference (e.g. an Aggregate component `type` with no version)
	// by list-tags→highest, mirroring libraries/aggregates. This is the only path
	// that can fetch a bare identity; without the kind or the capability the entity
	// tier falls to the legacy already-located, versioned reference path.
	if ep, ok := puller.(EntityPuller); ok && kind != "" {
		name, floor, hasFloor := splitVersionedURI(uri)
		result, resolvedVersion, err := ep.PullEntity(context.Background(), name, kind, floor)
		if err != nil {
			if errors.Is(err, oci.ErrFetchNotFound) {
				return nil, closure.Resolution{NotFound: true}, nil
			}
			// For the EntityPuller path the bound host is the configured registry,
			// not the bare identity's leading segment — ask the puller for it so the
			// docker-login remediation points correctly.
			host := registryhost.Of(name)
			if rh, ok := puller.(interface{ RegistryHost() string }); ok {
				host = rh.RegistryHost()
			}
			if rem := oci.RemediationFor(err, host); rem != "" {
				return nil, closure.Resolution{}, fmt.Errorf("catalog: tier-3 pull %s: %w\n  %s", uri, err, rem)
			}
			return nil, closure.Resolution{}, fmt.Errorf("catalog: tier-3 pull %s: %w", uri, err)
		}
		// A bare-name reference pins the fetched body's metadata.version (identity);
		// a floored reference keeps the floor. The cache slot is keyed by the
		// closure URI (what the build re-reads) plus the pinned version.
		pinnedVersion := resolvedVersion
		if !hasFloor {
			if mv := entityMetadataVersion(result.Bytes); mv != "" {
				pinnedVersion = mv
			}
		}
		if err := writeEntityToCacheSlot(workspaceRoot, cacheDir, uri, pinnedVersion, result); err != nil {
			return nil, closure.Resolution{}, fmt.Errorf("catalog: tier-3 cache write %s: %w", uri, err)
		}
		return result.Bytes, closure.Resolution{
			ResolvedVersion: pinnedVersion,
			Digest:          contentDigest(result.Bytes),
			ManifestDigest:  result.Digest,
			ResolutionPath:  "tier-3-pull",
		}, nil
	}

	// Legacy already-located, versioned reference path (no kind / no capability).
	reference, tag, ok := SplitURIForOCI(uri)
	if !ok {
		return nil, closure.Resolution{NotFound: true}, nil
	}
	result, err := puller.Pull(context.Background(), reference, tag)
	if err != nil {
		if errors.Is(err, oci.ErrFetchNotFound) {
			return nil, closure.Resolution{NotFound: true}, nil
		}
		// Fetch sentinels carry an operator remediation (e.g. the
		// docker-login pointer for an unauthorized registry); surface
		// it on the diagnostic rather than leaving the raw transport
		// failure to speak for itself.
		if rem := oci.RemediationFor(err, registryhost.Of(reference)); rem != "" {
			return nil, closure.Resolution{}, fmt.Errorf("catalog: tier-3 pull %s: %w\n  %s", uri, err, rem)
		}
		return nil, closure.Resolution{}, fmt.Errorf("catalog: tier-3 pull %s: %w", uri, err)
	}
	if err := writeEntityToCacheSlot(workspaceRoot, cacheDir, uri, tag, result); err != nil {
		return nil, closure.Resolution{}, fmt.Errorf("catalog: tier-3 cache write %s: %w", uri, err)
	}
	return result.Bytes, closure.Resolution{
		ResolvedVersion: tag,
		Digest:          contentDigest(result.Bytes),
		ManifestDigest:  result.Digest,
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
	// slot is keyed `<name>:<version>` — the symmetric key the lazy lookup's
	// verified Tier-2 read addresses.
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
			ManifestDigest:  result.Digest,
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
		ManifestDigest:  result.Digest,
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
	_, version, digest, manifestDigest, found, err := PullAndCacheAggregate(l.workspaceRoot, l.cacheDir, l.ociPuller, uri)
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
		ManifestDigest:  manifestDigest,
		ResolutionPath:  "tier-3-pull",
	}, nil
}

// PullAndCacheAggregate fetches a bare-name published Aggregate via the puller's
// AggregatePuller capability, caches it under name:metadata.version, and returns
// the verified bytes + resolved version (the fetched metadata.version identity)
// + content digest + the OCI manifest digest the artifact's signature commits
// to. found is false when the puller is nil (the hermetic build path), lacks the
// AggregatePuller capability, or the registry has no such aggregate
// (ErrFetchNotFound) — so a build, or an offline pull, never networks. It is the
// single fetch+cache+pin primitive shared by the closure's tier3Aggregate (sub
// path) and the triad resolver's Tier-3 seam (top-level path): one cache layout,
// one identity rule, two call sites.
func PullAndCacheAggregate(workspaceRoot, cacheDir string, puller OCIPuller, ref string) (data []byte, version, digest, manifestDigest string, found bool, err error) {
	if puller == nil {
		return nil, "", "", "", false, nil
	}
	ap, ok := puller.(AggregatePuller)
	if !ok {
		return nil, "", "", "", false, nil
	}
	name, floor, _ := splitVersionedURI(ref)
	result, fetchVersion, perr := ap.PullAggregate(context.Background(), name, floor)
	if perr != nil {
		if errors.Is(perr, oci.ErrFetchNotFound) {
			return nil, "", "", "", false, nil
		}
		return nil, "", "", "", false, fmt.Errorf("catalog: tier-3 pull Aggregate %s: %w", name, perr)
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
		return nil, "", "", "", false, fmt.Errorf("catalog: tier-3 cache write Aggregate %s: %w", name, werr)
	}
	return result.Bytes, version, contentDigest(result.Bytes), result.Digest, true, nil
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
		if err := os.WriteFile(filepath.Join(slot, "library.yaml"), result.Bytes, 0o644); err != nil {
			return err
		}
	default:
		if err := oci.WriteCached(workspaceRoot, cacheDir, uri, version, result.Bytes); err != nil {
			return err
		}
	}
	// Persist the OCI manifest as a second self-verifying blob in the same slot.
	// The offline read verifies the cached content against the manifest's layer
	// digest — the manifest is the authority — rather than trusting the recorded
	// content pin alone. A slot written without a manifest is incomplete and the
	// offline read treats it as a cache miss.
	if len(result.ManifestBytes) > 0 {
		if err := oci.WriteCachedManifest(workspaceRoot, cacheDir, uri, version, result.ManifestBytes); err != nil {
			return err
		}
	}
	return nil
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

// contentDigest returns the standard OCI-format SHA-256 digest of content
// ("sha256:<hex>"). Forwards to the single content-address spelling in
// pkg/digest so this package never re-derives the format.
func contentDigest(data []byte) string {
	return digest.ContentDigest(data)
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
// Aggregate / SchemaLibrary / MappingLibrary entries by
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
