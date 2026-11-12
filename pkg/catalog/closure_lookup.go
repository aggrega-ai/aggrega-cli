// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcecache"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// ValidateClosureScopedPillars validates ONLY the catalog Components and Features
// the closure reaches — the closure-scoped counterpart to whole-catalog pillar
// validation (validateSchemaPillarsPass). It intersects cl.Entities (by
// version-stripped name) with the loaded Components / Features — an entity the walk
// collected as a Component with the Components, one collected as a Feature with
// the Features, so an entity of another kind sharing a name reaches neither — and
// delegates to ValidateClosurePillars against the catalog's SchemaLibraries (local +
// bridged).
//
// A closure entity whose schema `$ref` names a library absent from the catalog —
// a broken library skipped at tolerant load, or a genuinely missing one — fails
// here: the hard in-closure library reject. An out-of-closure entity is never in
// cl.Entities, so a broken one is never validated and never build-fatal. Pillar
// validation runs over the entities as loaded (pre-extends), exactly as the
// whole-catalog pass does.
func (c *Catalog) ValidateClosureScopedPillars(cl closure.Closure) error {
	comps := map[string]types.Component{}
	compSrc := map[string]string{}
	feats := map[string]types.Feature{}
	featSrc := map[string]string{}
	for _, e := range cl.Entities {
		if !e.ReadableAs(closure.KindComponent) {
			continue
		}
		if key, ok, _ := c.resolveComponentKey(e.URI); ok {
			comps[key] = c.Components[key]
			compSrc[key] = c.componentSources[key]
		}
	}
	for _, e := range cl.Entities {
		if !e.ReadableAs(closure.KindFeature) {
			continue
		}
		if key, ok, _ := c.resolveFeatureKey(e.URI); ok {
			feats[key] = c.Features[key]
			featSrc[key] = c.featureSources[key]
		}
	}
	// No remedy provider: the eager catalog does not retain the artifact cache
	// location, and its own walk has always stepped over the cache, so a library
	// missing here was never cache-reachable in the first place.
	return ValidateClosurePillars(comps, compSrc, feats, featSrc, c.SchemaLibraries, nil)
}

// ResolveClosureExtends resolves the extends chain for ONLY the Components and
// Features the closure reaches, in sorted-name order, mutating them in place —
// the closure-scoped counterpart to ResolveAllExtends. An out-of-closure entity's
// extends is never resolved, so a broken out-of-closure parent never fails the
// build; an in-closure child whose parent is missing (skipped at tolerant load)
// or malformed fails loudly. Each child's full chain is re-resolved from the
// child via the catalog lookup, so an out-of-closure (unresolved) parent in the
// chain is still merged correctly. Names are deduped (cl.Entities may list one
// entity under several version-suffixed URIs) so no chain is double-merged. A
// link whose parent the catalog holds at other versions only is reported as the
// version no catalog entry satisfies, as the whole-catalog pass reports it,
// rather than as a parent no catalog entity provides.
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
		if !e.ReadableAs(closure.KindComponent) {
			continue
		}
		if key, ok, _ := c.resolveComponentKey(e.URI); ok {
			compSet[key] = struct{}{}
		}
	}
	for _, e := range cl.Entities {
		if !e.ReadableAs(closure.KindFeature) {
			continue
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
		lookup, refusal := recordExtendsRefusal(compLookup, c.resolveComponentKey)
		resolved, vErrs := validator.ResolveComponentExtends(comp, lookup)
		if len(vErrs) > 0 {
			errs = append(errs, refusal.refine(envelope.KindComponent, name, vErrs)...)
			continue
		}
		c.Components[name] = resolved
	}
	for _, name := range featNames {
		feat := c.Features[name]
		if feat.Spec.Extends == "" {
			continue
		}
		lookup, refusal := recordExtendsRefusal(featLookup, c.resolveFeatureKey)
		resolved, vErrs := validator.ResolveFeatureExtends(feat, lookup)
		if len(vErrs) > 0 {
			errs = append(errs, refusal.refine(envelope.KindFeature, name, vErrs)...)
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
//	Tier 2: cache slot at <workspaceRoot>/<cacheDir>/entities/<encoded-kind>/<encoded-reference>/<encoded-version>/.
//	Tier 3: pull via the supplied OCIPuller. When ociPuller is nil
//	        (build-time hermetic path), Tier 3 returns Resolution{NotFound: true}
//	        so the closure walker surfaces ErrUnresolvableURI cleanly
//	        rather than attempting an out-of-band network call.
//
// Digest discipline: every tier returns the digest the row it feeds
// records, never the manifest digest pkg/oci.Pull also returns. An
// entity's row records what its publication carries — each file's path
// and bytes — so a catalog-directory entity is
// digested over its declared files (AuthoredEntityDigest) and a fetched
// or cached one over the entries of the bytes it was published as
// (oci.PublishedDigest), and the two agree for one publication. A
// library's row records its bytes, which are its document at every tier.
// The manifest digest is recorded beside the row, and a cached read holds
// the bytes to the manifest's descriptor before it reads the row's digest.
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
// ClosureLookup, additionally primed with a map of external entities the
// in-memory catalog does not index, keyed by the identity the walk gives each
// (kind and URI) and holding its bytes. Used by
// the pull / build CLI verbs to register the Release's referenced
// Aggregate and Manifest bytes (these are deployment-side entities
// loaded via resolver.LoadAggregate / resolver.LoadManifest, not via
// catalog.Load) so the closure walker's ResolveCatalogEntity hits
// these URIs at Tier 1 instead of falling through to Tier 3.
//
// The externals map is consulted BEFORE the in-memory catalog;
// duplicate keys (same URI registered both as external and as
// catalog entity) are resolved as external. Each is recorded by the
// digest its row records for a document published alone (lookupExternal).
func (c *Catalog) ClosureLookupWithExternals(ociPuller OCIPuller, workspaceRoot, cacheDir string, externals map[closure.EntityKey][]byte) closure.Lookup {
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
	externals     map[closure.EntityKey][]byte

	// registrySeeds carries pre-resolved registry entities (a registry-resolved
	// top-level spec.aggregate the triad resolver fetched at pull), keyed by the
	// entity's identity — its kind and its reference, as externals is — →
	// {version, digest, tier}. The closure pins these as real registry entities
	// rather than version-less externals, without re-fetching. Empty on the build
	// and serve paths (the top-level resolves there via the cache→catalog bridge).
	registrySeeds map[closure.EntityKey]closure.Resolution

	// priorPins, when non-nil, makes the pull HONOR a prior lockfile: a closure
	// entity carrying a pin resolves to the pin's exact ResolvedVersion (never
	// floated to list-tags→highest), and a Tier-2 cache slot is reused only when
	// its content digest matches the pin's Digest; a registry whose pinned-version
	// bytes no longer match the pin Digest HALTS (ErrFetchIntegrity), never
	// re-pinned. refetchPending records each cache slot the reuse check passed
	// over, keyed by the slot's kind and the URI it is fetched at, so the Tier-3 re-fetch records
	// a notice via warn and a Tier-3 miss halts naming the slot; both are nil
	// unless a ReuseVerification was supplied (the hermetic build and serve paths
	// leave them nil and reuse cache slots unconditionally — the build is frozen
	// against a complete lockfile).
	priorPins      Pins
	refetchPending map[PinKey]passedOverSlot
	warn           func(notice string)
}

// The recipe axis is reached by assertion rather than through the Lookup method
// set, so satisfaction is asserted here. Without it a signature drift would turn
// closure.RequireRecipeLoader into a refusal at every site that passes this
// value, instead of a compile error at the value itself.
var _ closure.RecipeLoaderSource = (*catalogLookup)(nil)

// The pinned-socket resolution is asserted for the same reason: it is reached
// through closure.ResolvePinnedSocketEntity, which falls back to the general
// cascade for a lookup lacking it, so a signature drift would otherwise go unseen.
var _ closure.PinnedSocketEntityResolver = (*catalogLookup)(nil)

// ReuseVerification, when supplied to ClosureLookupWithReuse, makes
// every Tier-2 cache reuse verify the cached bytes against a prior
// lockfile's pins before trusting them. A reused entity whose content
// digest no longer matches its pin is re-fetched from the registry
// (Tier-3), which rewrites the cache slot from authoritative bytes,
// and a notice naming the slot and both digests is handed to Warn. Where
// no registry serves the pinned version — the run reaches none, or the
// registry has none — the resolution halts naming the slot, what it holds
// and the pin, rather than reporting the reference as one nothing
// resolves. A nil *ReuseVerification disables the check, leaving behavior
// identical to ClosureLookupWithExternals.
//
// PriorPins maps an artifact's kind and canonical URI (PinKey) to the pin the
// prior lockfile recorded for it. A key absent from the map is reused without
// verification — there is nothing to compare against (e.g. an entity
// that entered closure after the prior lock).
type ReuseVerification struct {
	PriorPins Pins
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
func (c *Catalog) ClosureLookupWithReuse(ociPuller OCIPuller, workspaceRoot, cacheDir string, externals map[closure.EntityKey][]byte, registrySeeds map[closure.EntityKey]closure.Resolution, reuse *ReuseVerification) closure.Lookup {
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
		l.refetchPending = map[PinKey]passedOverSlot{}
		l.warn = reuse.Warn
	}
	return l
}

// LoadAggregate loads a resolved sub-aggregate's parsed body for transitive
// closure descent, satisfying closure.AggregateLoader. A catalog-resident
// Aggregate is read from the catalog's retained authored bytes of the version
// the resolution selected — the same bytes its tier-1 digest pins. A pulled
// Aggregate is read from the content cache through the verifying read, which
// checks the cached bytes against the supplied pin digest before returning.
// Either way the descent walks the exact bytes the pin covers, never a
// re-serialization or an unverified read.
func (l *catalogLookup) LoadAggregate(uri, version, digest string) (*types.Aggregate, error) {
	name := lookupNameFromURI(uri)
	if data, ok := l.catalog.EntityBytes("Aggregate", entityIndexKey(name, version)); ok {
		return resolver.LoadAggregateFromBytes(data, name)
	}
	// The sub-aggregate loader interface carries the content pin but not the
	// manifest digest; the manifest self-verify step is skipped, while the
	// persisted manifest still authorities the content (VerifyContentAgainstManifest)
	// and the content pin remains the independent root of trust.
	blob, err := tier2ReadVerified(l.workspaceRoot, l.cacheDir, aggregateKind, versionedSlotURI(uri, version), digest, "")
	if err != nil {
		return nil, err
	}
	// The slot holds the artifact the pin covers, which for some kinds is an
	// archive; the descent parses the document inside it. The verify above runs
	// over the blob for exactly that reason — narrowing it to the document would
	// verify less than the lockfile committed to.
	document, derr := oci.EntityYAML(blob)
	if derr != nil {
		return nil, fmt.Errorf("catalog: sub-aggregate %s: %w", uri, derr)
	}
	return resolver.LoadAggregateFromBytes(document, uri)
}

// ClosureRecipeLoader hands out the recipe loader over this lookup's in-memory
// catalog, satisfying closure.RecipeLoaderSource so the recipe axis can be
// computed against the lookup a walk already carries. It is the same loader that
// catalog hands out directly; exposing it here means a caller holding only the
// closure surface does not need the catalog value as well.
func (l *catalogLookup) ClosureRecipeLoader() closure.RecipeLoader {
	return l.catalog.ClosureRecipeLoader()
}

// versionedSlotURI returns the cache-slot key for an entity. The slot is keyed
// by the versioned URI; a reference that already carries its version
// (`name:version`, a host-bearing `host/path:version`) is used as-is, while a
// bare-name reference gets the resolved version appended. Centralising the rule
// keeps the bare-name case (a registry-published aggregate referenced by
// identity) and the versioned-URI case from clashing on a double-appended tag.
//
// It is the one derivation of that key for writing a slot and for reading one:
// the registry tiers write a fetched entity through it (writeEntityToCacheSlot,
// tier3Slot), and every reader of a pinned entity reads through it — the pull's
// reuse check (pinnedFetchURI), the lazy view's verified read (tier2Verified),
// its pinned fold and its integrity gate. So a bare reference a pull fetches
// with no pin to hand is cached where its pin is read back from, by the pull's
// own render, by a later pull and by the build alike.
func versionedSlotURI(uri, version string) string {
	if _, _, hasV := resolver.SplitVersionedURI(uri); hasV {
		return uri
	}
	if version == "" {
		return uri
	}
	return uri + ":" + version
}

func (l *catalogLookup) ResolveCatalogEntity(ctx context.Context, uri, kindHint string) (closure.Resolution, error) {
	return l.resolveCatalogEntity(ctx, uri, kindHint, false)
}

// ResolvePinnedSocketEntity resolves the Plugin entity of a workspace-socket
// attachment the lockfile pins, satisfying closure.PinnedSocketEntityResolver. It is
// ResolveCatalogEntity's cascade with one tier more: where no catalog directory holds
// the entity at the version the reference asks for, the entities the binary carries
// answer it at every plugin type, read through embeddedPluginByURI — the reading the
// build's plugin lookup (Catalog.PluginByURI) takes of them. So a governance
// attachment at a transform role naming the binary's own version resolves here as it
// resolves where a verdict attachment naming it is loaded, and is pinned as an
// output-shaping attachment the binary carries is pinned.
//
// The tier sits where the provider one does — after the catalog directories, before
// the content cache and the registry — which is the order the build's plugin lookup
// reads catalog directories and the binary in. A core provider keeps the provider
// tier's own rule, so the wider tier answers only the types that one leaves to the
// registry.
//
// The content cache includes an entry the catalog's index received from a
// lockfile-pinned cache slot, which the local tier would otherwise answer first: a
// pull resolves the entity from the binary and records it so, while a build over the
// same lockfile holds the copy a registry served under that pin. Such an entry
// answers here only where neither tier reading the binary answers the reference, so
// the pull and the build give the pinned row one source.
func (l *catalogLookup) ResolvePinnedSocketEntity(ctx context.Context, uri string) (closure.Resolution, error) {
	return l.resolveCatalogEntity(ctx, uri, closure.KindPlugin, true)
}

// resolveCatalogEntity is the one cascade behind ResolveCatalogEntity and
// ResolvePinnedSocketEntity. everyEmbeddedType admits the binary's entities of every
// plugin type after the provider tier, ahead of an index entry the content cache
// supplied; without it only core providers resolve from the binary, and the local
// tier answers every index entry first.
func (l *catalogLookup) resolveCatalogEntity(ctx context.Context, uri, kindHint string, everyEmbeddedType bool) (closure.Resolution, error) {
	entityKind := closure.EntityKindOf(kindHint)
	if res, ok, err := l.lookupExternal(uri, entityKind); err != nil || ok {
		return res, err
	}
	if res, ok := l.lookupRegistry(uri, entityKind); ok {
		return res, nil
	}
	local, indexed, err := l.tier1CatalogEntity(uri, entityKind)
	if err != nil {
		return local, err
	}
	// An index entry the pinned fold placed from a cache slot reports the cache as its
	// source, and for a pinned socket entity the cache comes after the binary.
	cacheEntryAfterBinary := indexed && everyEmbeddedType && closure.AcquiredFromRegistry(local.ResolutionPath)
	if indexed && !cacheEntryAfterBinary {
		return local, nil
	}
	// A core provider that ships inside the binary is already on the machine: resolve
	// it here, before the registry tiers, so no network round-trip is attempted for
	// something the binary carries. It sits after the local catalog so a
	// locally-authored entity keeps resolving exactly where it did, and it is scoped to
	// providers so every other plugin type keeps its full cascade.
	if res, ok := l.embeddedPluginEntity(uri, kindHint); ok {
		return res, nil
	}
	if everyEmbeddedType {
		if p, ok := l.catalog.embeddedPluginByURI(uri); ok && plugintype.Type(p.Metadata.PluginType) != plugintype.Provider {
			if res, ok := embeddedEntityResolution(p); ok {
				return res, nil
			}
		}
	}
	if cacheEntryAfterBinary {
		return local, nil
	}
	// Honor a prior pin: resolve the pinned EXACT version (never float to
	// list-tags→highest) and verify the resolved bytes against the pin's digest.
	// The pin and the cache slot are both the reference's under the published kind
	// it was collected as (EntityKindForHint), so a Component and a Recipe of one
	// name are each held to their own pin and read from their own slot.
	// A reference collected as no published kind — one whose kind its caller did not
	// know — has no slot to read, so the cache tier is not asked for it.
	slotKind := EntityKindForHint(kindHint)
	fetchURI := l.pinnedFetchURI(slotKind, uri)
	if slotKind != "" {
		if _, res, ok, err := l.tier2(slotKind, uri, fetchURI); err != nil {
			return res, err
		} else if ok {
			return l.honorPinDigest(slotKind, uri, res)
		}
	}
	_, res, err := l.tier3CatalogEntity(ctx, fetchURI, slotKind)
	if err != nil {
		return res, err
	}
	if err := l.pinHeldNowhere(slotKind, uri, fetchURI, res); err != nil {
		return closure.Resolution{}, err
	}
	if res.NotFound {
		res.UnsatisfiedPin = l.tier1UnsatisfiedPin(uri, entityKind)
	}
	return l.honorPinDigest(slotKind, uri, res)
}

// tier1UnsatisfiedPin reports why the local catalog did not answer uri when the
// reason is the version: an index holds the entity under the name uri gives, and
// no version it holds satisfies uri's pin. It returns the version select's own
// refusal then, and nil otherwise — a name the catalog does not hold, or a pin
// whose text is malformed, which the walk reports as that malformation.
//
// It is asked only once every tier has missed, so it changes no reference that
// resolves: a registry that serves the pinned version still answers it, and the
// local catalog holding the entity at other versions does not shadow that.
//
// It asks the rungs tier1CatalogEntryOfKind resolves through for the kind the
// reference was collected as (tier1RungsFor), each through its own select
// (tier1Rung.unsatisfied), so the reason it gives is about the lookup that just
// missed rather than a second reading of the catalog — and never about another
// kind's entity of the same name.
func (l *catalogLookup) tier1UnsatisfiedPin(uri, entityKind string) error {
	for _, rung := range l.tier1RungsFor(entityKind) {
		if err := rung.unsatisfied(uri); errors.Is(err, ErrUnsatisfiableEntityVersion()) {
			return err
		}
	}
	return nil
}

// pinnedFetchURI returns the URI to resolve for an entity that carries a prior
// lockfile pin: the pin's exact ResolvedVersion appended to a bare reference, so
// a Tier-3 pull fetches the pinned version verbatim (the proven exact path)
// rather than listing tags and floating to the highest. A reference that already
// carries a version, or one with no prior pin (un-pinned: resolve fresh + add),
// is returned unchanged. This is the float→honor flip; floating happens only for
// an un-pinned reference (or under `pull --upgrade`, which supplies no pins).
func (l *catalogLookup) pinnedFetchURI(kind, uri string) string {
	if l.priorPins == nil {
		return uri
	}
	if _, _, hasVersion := resolver.SplitVersionedURI(uri); hasVersion {
		return uri
	}
	pin, ok := l.priorPins[PinKey{Kind: kind, URI: uri}]
	if !ok || pin.ResolvedVersion == "" {
		return uri
	}
	return versionedSlotURI(uri, pin.ResolvedVersion)
}

// honorPinDigest enforces honor≠launder: when the entity carried a prior pin and
// the resolved bytes no longer hash to the pin's digest, the registry's
// pinned-version bytes have changed under the pin — HALT with ErrFetchIntegrity
// rather than re-resolving and laundering the changed bytes into a new pin.
// Keyed by the original (caller's) URI, so a bare reference verifies against its
// bare-keyed pin regardless of the versioned form used to fetch.
func (l *catalogLookup) honorPinDigest(kind, uri string, res closure.Resolution) (closure.Resolution, error) {
	if l.priorPins == nil || res.NotFound {
		return res, nil
	}
	pin, ok := l.priorPins[PinKey{Kind: kind, URI: uri}]
	if !ok || pin.Digest == "" || res.Digest == pin.Digest {
		return res, nil
	}
	return closure.Resolution{}, fmt.Errorf(
		"%w: pinned entity %q: registry now serves digest %s for the pinned version, not the pinned digest %s; run `aggrega pull --upgrade --release <release>` to update the pin",
		oci.ErrFetchIntegrity(), uri, res.Digest, pin.Digest)
}

// embeddedPluginEntity resolves a Plugin-entity reference against the core PROVIDERS
// that ship inside the compiler binary, reporting (resolution, true) on a hit. A hit
// needs no registry access at all: the entity is read from the binary itself.
//
// Three conditions narrow it.
//
// It fires only for a Plugin-entity reference — only plugins ship inside the binary,
// so Component / Feature / Recipe resolution is untouched.
//
// It fires only when the embedded entity declares pluginType provider. The binary
// carries entities for every core plugin it ships, governance guests included, so the
// narrowing is a real restriction rather than a formality. It is needed because the
// pull-side entity pin sites decline whatever this branch reports WITHOUT inspecting
// any plugin type: widening it here would silently stop the closure pinning some other
// socket's plugin entity. A provider is the type for which declining is right — the
// build selects its rendering engine from the binary's own copy, so a provider entity
// resolved here is available at build with nothing recorded. The test reads the
// entity's own declared plugin type, which is authoritative and in hand here — a
// plugin's identity coordinate is built from its (pluginType, name) pair and is never
// re-parsed to recover either half.
//
// And it fires only for a reference the embedded copy actually satisfies: a bare
// identity, or an exact version equal to the embedded entity's own metadata.version.
// A reference pinned to a different version, or written as a range, keeps its registry
// cascade — an author who names a published version gets that published version rather
// than whatever the binary happens to carry. The version test is a plain equality
// rather than the three-form selection rule because the embedded set holds exactly one
// version per identity, the binary's own, so there is nothing to select among; do not
// "unify" it with the version selector, which would answer a different question.
//
// A hit reports the embedded resolution path, which the pull-side entity-axis pin
// sites read as already-present-and-not-a-pulled-artifact and leave out of the
// closure. It is deliberately NOT a NotFound: a workspace provider socket treats
// NotFound as an unresolvable reference and halts, so reporting a miss here would turn
// a core provider the binary carries into a hard failure. A non-provider reference,
// including one whose entity the binary does carry, falls through to the registry
// cascade — save the entity of a pinned workspace-socket attachment, which
// ResolvePinnedSocketEntity reads from the binary at every type, because the pin
// sites record such an entity wherever it resolved from.
func (l *catalogLookup) embeddedPluginEntity(uri, kindHint string) (closure.Resolution, bool) {
	if kindHint != closure.KindPlugin {
		return closure.Resolution{}, false
	}
	identity, requested, hasVersion := resolver.SplitVersionedURI(uri)
	embeds, _ := embeddedPluginEntities()
	for _, p := range embeds {
		if plugintype.Type(p.Metadata.PluginType) != plugintype.Provider {
			continue
		}
		if p.Coordinate() != identity {
			continue
		}
		if hasVersion && requested != p.Metadata.Version {
			continue
		}
		return embeddedEntityResolution(p)
	}
	return closure.Resolution{}, false
}

// embeddedEntityResolution is the resolution of a Plugin entity read from the binary:
// the entity's own version, the digest of the document the binary carries for it, and
// the from-the-binary path. It is the one description of such a resolution, so the
// provider tier and the pinned-socket tier report one shape. It reports false when the
// binary carries no document under the entity's identity.
func embeddedEntityResolution(p types.Plugin) (closure.Resolution, bool) {
	raw, ok := embedded.LookupEntity(p.Coordinate())
	if !ok {
		return closure.Resolution{}, false
	}
	return closure.Resolution{
		ResolvedVersion: p.Metadata.Version,
		Digest:          oci.DocumentDigest(raw),
		ResolutionPath:  closure.ResolutionPathEmbedded,
	}, true
}

// lookupRegistry returns a pre-resolved registry entity the caller seeded — a
// registry-resolved top-level spec.aggregate the triad resolver fetched at pull.
// It pins the entity as a real registry entity (its metadata.version + digest)
// rather than letting it fall to the version-less external tier, so the lockfile
// records a real version and the build-side bridge re-derives the same.
//
// A seed is keyed by its identity and found as the Release's own entities are
// (lookupReleaseEntity), so it answers a reference collected as its kind, or one
// whose kind its caller does not know, and never one collected as another kind
// under the same name — the Release's own Manifest named like its Aggregate.
func (l *catalogLookup) lookupRegistry(uri, entityKind string) (closure.Resolution, bool) {
	return lookupReleaseEntity(l.registrySeeds, uri, entityKind)
}

// lookupReleaseEntity returns what m holds for the Release's own entity uri names,
// for a reference collected as entityKind: of the kinds the Release's own entities
// are recorded as (closure.ReleaseExternalKinds), in that order, the first a
// reference collected as entityKind may be read as (closure.KindReadableAs) that m
// holds under uri — the entry of the reference's own kind, or for a reference whose
// kind its caller does not know, the first of them m holds. It is the one lookup of the
// Release's own entities, by the bytes a caller registered for them (externals) and
// by a registry resolution a caller seeded (registrySeeds) alike.
func lookupReleaseEntity[V any](m map[closure.EntityKey]V, uri, entityKind string) (V, bool) {
	for _, kind := range closure.ReleaseExternalKinds() {
		if !closure.KindReadableAs(entityKind, kind) {
			continue
		}
		if v, ok := m[closure.EntityKey{Kind: kind, URI: uri}]; ok {
			return v, true
		}
	}
	var none V
	return none, false
}

// ResolveAggregate resolves a sub-aggregate reference (a KindHint=="aggregate"
// closure entity), satisfying closure.Lookup. It cascades like
// ResolveCatalogEntity — external → tier-1 → tier-2 — but a bare-name miss falls
// to tier3Aggregate (the AggregatePuller capability), not the tag-requiring
// generic tier-3. This is the aggregate counterpart of ResolveSchemaLibrary, so
// a registry-published sub-aggregate referenced by bare identity resolves at
// pull (and the puller is nil on the hermetic build path, so it never fetches at
// build — the sub resolves there via the build-side cache→catalog bridge).
func (l *catalogLookup) ResolveAggregate(ctx context.Context, uri string) (closure.Resolution, error) {
	if res, ok, err := l.lookupExternal(uri, closure.KindAggregate); err != nil || ok {
		return res, err
	}
	if res, ok, err := l.tier1CatalogEntity(uri, closure.KindAggregate); err != nil || ok {
		return res, err
	}
	fetchURI := l.pinnedFetchURI(aggregateKind, uri)
	if _, res, ok, err := l.tier2(aggregateKind, uri, fetchURI); err != nil {
		return res, err
	} else if ok {
		return l.honorPinDigest(aggregateKind, uri, res)
	}
	res, err := l.tier3Aggregate(ctx, fetchURI)
	if err != nil {
		return res, err
	}
	if err := l.pinHeldNowhere(aggregateKind, uri, fetchURI, res); err != nil {
		return closure.Resolution{}, err
	}
	return l.honorPinDigest(aggregateKind, uri, res)
}

// lookupExternal resolves uri against the Release's own Aggregate and Manifest,
// which a caller registered by their bytes. Each is recorded by the digest of what
// it would publish as — its document alone — so the row is the one subject every
// entity row has.
//
// The two are registered under their kinds, and a reference finds the one of the
// kind it was collected as, so an Aggregate and a Manifest that share a name each
// resolve to their own document. A reference whose kind its caller does not know
// finds either, in the order closure.ReleaseExternalKinds lists them
// (lookupReleaseEntity).
func (l *catalogLookup) lookupExternal(uri, entityKind string) (closure.Resolution, bool, error) {
	bytes, ok := lookupReleaseEntity(l.externals, uri, entityKind)
	if !ok {
		return closure.Resolution{}, false, nil
	}
	d, err := oci.PublishedDigest(bytes)
	if err != nil {
		return closure.Resolution{}, false, fmt.Errorf("catalog: %s: %w", uri, err)
	}
	_, version, _ := resolver.SplitVersionedURI(uri)
	return closure.Resolution{
		ResolvedVersion: version,
		Digest:          d,
		ResolutionPath:  closure.ResolutionPathExternal,
	}, true, nil
}

// ResolveLibrary resolves a library reference on any axis through the same three
// tiers every axis uses: the locally-authored catalog, the pinned content cache,
// then the registry. The axis selects the local-catalog reader and names the kind
// the registry tier fetches under; nothing else differs between the axes, so the
// cascade is written once here rather than copied per axis.
func (l *catalogLookup) ResolveLibrary(ctx context.Context, axis closure.LibraryAxis, uri string) (closure.LibraryResolution, error) {
	if res, entries, ok := l.tier1Library(axis, uri); ok {
		return closure.LibraryResolution{Resolution: res, Entries: entries}, nil
	}
	fetchURI := l.pinnedFetchURI(axis.Kind, uri)
	if _, res, ok, err := l.tier2(axis.Kind, uri, fetchURI); err != nil {
		return closure.LibraryResolution{Resolution: res}, err
	} else if ok {
		res, err := l.honorPinDigest(axis.Kind, uri, res)
		return closure.LibraryResolution{Resolution: res}, err
	}
	res, err := l.tier3Library(ctx, fetchURI, axis.Kind)
	if err != nil {
		return closure.LibraryResolution{Resolution: res}, err
	}
	if err := l.pinHeldNowhere(axis.Kind, uri, fetchURI, res); err != nil {
		return closure.LibraryResolution{}, err
	}
	res, err = l.honorPinDigest(axis.Kind, uri, res)
	return closure.LibraryResolution{Resolution: res}, err
}

func (l *catalogLookup) ResolvePlugin(ctx context.Context, uri, version string) (closure.Resolution, error) {
	// Embed tier: a core plugin's .wasm embedded in the binary resolves offline by its
	// bare identity (<socket>/<name>), whatever its socket. Its reference digest is
	// derived from the embedded bytes on demand — never stored — so the closure walker
	// no longer drops such a reference as a NotFound miss and no registry round-trip is
	// attempted. Whether the resolved artifact is then written into the lockfile is the
	// pull-side pin site's decision, not this tier's, and it turns on the plugin type:
	// an output-shaping plugin is pinned so the bytes it produces are held still, while
	// a core provider is not, because the build selects its rendering engine from the
	// same binary. A pinned row carries the derived digest in the existing digest
	// field; identity stays the bare plugin identity.
	if b, ok := embedded.Lookup(uri); ok {
		return embeddedPluginResolution(contentDigest(b)), nil
	}
	// A Plugin entity's .wasm artifact is identified by the entity's bare identity
	// (<socket>/<name>). The .wasm is not a standalone artifact: it rides inline,
	// as a layer, in the single plugin manifest published under the entity's
	// version tag. Resolve it from that manifest by version rather than falling
	// through to the entity-resolving catalog tiers.
	return l.resolveVersionedPluginArtifact(ctx, uri, version)
}

// pluginArtifactReference binds a plugin's bare identity (<socket>/<name>) to the
// physical OCI repository reference its .wasm is fetched from —
// `<registry>/plugin/<socket>/<name>` — through the single location rule
// (entityReference → OCILocation), so the plugin artifact fetch location is
// derived exactly where the entity's own location is, never carried in the entity.
// The configured registry comes from the puller — the location prefix the entity
// pull binds against, the default registry's host followed by any repository
// path it carries; a puller that cannot report one, or an identity that is
// already registry-qualified, is refused loudly rather than fetched from a
// double-prefixed location.
func (l *catalogLookup) pluginArtifactReference(identity string) (string, error) {
	registry := ""
	if rp, ok := l.ociPuller.(interface{ RegistryPrefix() string }); ok {
		registry = rp.RegistryPrefix()
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
// Either way the resolution pins ResolvedVersion = Digest = the derived content
// digest and ManifestDigest = the manifest digest; a cache hit is reported as
// served from the cache and a pull as fetched from the registry. A prior pin is
// HONORED, not laundered: a re-pull whose derived content or served manifest no
// longer matches the recorded pin halts with oci.ErrFetchIntegrity rather than
// re-pinning the changed bytes.
func (l *catalogLookup) resolveVersionedPluginArtifact(ctx context.Context, uri, version string) (closure.Resolution, error) {
	// Cache-first: a prior lockfile pin names the content slot to read. Without a
	// prior pin the slot cannot be addressed (its key is the yet-unknown content
	// digest), so a fresh resolution must pull to learn the pin.
	if prior, ok := l.priorPins[PluginExecutablePin(uri)]; ok && prior.Digest != "" {
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
	result, err := l.ociPuller.Pull(ctx, reference, version)
	if err != nil {
		if errors.Is(err, oci.ErrFetchNotFound()) {
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
	if prior, ok := l.priorPins[PluginExecutablePin(uri)]; ok {
		if prior.Digest != "" && contentPin != prior.Digest {
			return closure.Resolution{}, fmt.Errorf("catalog: pulled plugin artifact %s content digest %s does not match pin %s: %w",
				uri, contentPin, prior.Digest, oci.ErrFetchIntegrity())
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
	bytes, err := oci.ReadCached(l.workspaceRoot, l.cacheDir, oci.PluginArtifactKind(), uri, contentPin)
	if err != nil {
		if errors.Is(err, oci.ErrFetchNotFound()) {
			return closure.Resolution{}, false, nil
		}
		return closure.Resolution{}, false, fmt.Errorf("catalog: read cached plugin artifact %s: %w", uri, err)
	}
	// A slot with a content blob but no manifest blob is incomplete (the rule
	// persistedManifest decides for every cache reader); it is a cache miss, so the
	// re-pull rewrites both blobs (offline, the miss surfaces the pull-first
	// remediation rather than trusting a half-populated slot).
	manifestBytes, mrerr := persistedManifest(l.workspaceRoot, l.cacheDir, oci.PluginArtifactKind(), uri, contentPin)
	switch {
	case mrerr == nil:
		if prior, ok := l.priorPins[PluginExecutablePin(uri)]; ok && prior.ManifestDigest != "" {
			if verr := oci.VerifyManifestDigest(manifestBytes, prior.ManifestDigest); verr != nil {
				return closure.Resolution{}, false, fmt.Errorf("catalog: cached manifest for plugin artifact %s: %w", uri, verr)
			}
		}
		if verr := oci.VerifyContentAgainstManifest(manifestBytes, bytes); verr != nil {
			return closure.Resolution{}, false, fmt.Errorf("catalog: cached plugin artifact %s: %w", uri, verr)
		}
		if got := contentDigest(bytes); got != contentPin {
			return closure.Resolution{}, false, fmt.Errorf("catalog: cached plugin artifact %s content digest %s does not match pin %s: %w",
				uri, got, contentPin, oci.ErrFetchIntegrity())
		}
		// Served from the cache, and reported as such: the slot is keyed by the
		// content pin, so that pin is the version, and the verified manifest beside
		// the bytes gives the manifest digest. Nothing about this hit touched the
		// registry, so nothing downstream asks the registry about it either — its
		// signature fact is the one the pull that fetched it recorded.
		res, rerr := cacheHitResolution(contentPin, bytes, manifestBytes)
		if rerr != nil {
			return closure.Resolution{}, false, fmt.Errorf("catalog: cached plugin artifact %s: %w", uri, rerr)
		}
		return res, true, nil
	case errors.Is(mrerr, oci.ErrFetchNotFound()):
		return closure.Resolution{}, false, nil
	default:
		return closure.Resolution{}, false, fmt.Errorf("catalog: plugin artifact %s: %w", uri, mrerr)
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
	if err := oci.WriteCached(l.workspaceRoot, l.cacheDir, oci.PluginArtifactKind(), uri, contentPin, result.Bytes); err != nil {
		return fmt.Errorf("catalog: cache write plugin artifact %s: %w", uri, err)
	}
	if len(result.ManifestBytes) > 0 {
		if err := oci.WriteCachedManifest(l.workspaceRoot, l.cacheDir, oci.PluginArtifactKind(), uri, contentPin, result.ManifestBytes); err != nil {
			return fmt.Errorf("catalog: cache write plugin manifest %s: %w", uri, err)
		}
	}
	return nil
}

// pinnedPluginResolution is the resolution descriptor for a content-pinned .wasm
// artifact this run fetched from the registry: ResolvedVersion and Digest are
// both the pinned content digest, so the lockfile pin (Digest) and the cache-read
// key (ResolvedVersion) name the same content. ManifestDigest is the .wasm's OCI
// manifest digest — the verify subject, distinct from the content pin.
// ResolutionPath marks it fetched from the registry, which is what sends it to the
// signature pass. A .wasm the cache served is described by cacheHitResolution
// instead, with the same three digests and the cache as its source.
func pinnedPluginResolution(pinDigest, manifestDigest string) closure.Resolution {
	return closure.Resolution{
		ResolvedVersion: pinDigest,
		Digest:          pinDigest,
		ManifestDigest:  manifestDigest,
		ResolutionPath:  closure.ResolutionPathRegistry,
	}
}

// embeddedPluginResolution is the resolution descriptor for a core .wasm
// artifact resolved from the binary's embed tier: ResolvedVersion and Digest are
// both the reference digest derived from the embedded bytes, so a core plugin
// resolves to a real content digest without any registry access. ManifestDigest is
// empty — an embedded artifact carries no registry manifest to verify against.
// ResolutionPath marks it resolved-from-the-binary, so the closure walker treats it
// as a real resolution rather than a NotFound miss. When a pin site does record such
// an artifact — an output-shaping plugin the binary carries — the derived digest is
// what the lockfile row holds, and the drift gate compares against it on the next
// build.
func embeddedPluginResolution(refDigest string) closure.Resolution {
	return closure.Resolution{
		ResolvedVersion: refDigest,
		Digest:          refDigest,
		ResolutionPath:  closure.ResolutionPathEmbedded,
	}
}

// tier1CatalogEntity resolves uri against the entities this lookup's catalog
// indexes. Each kind is selected by its own resolution rule, and the resolution
// reports the version that rule selected — the entity's own metadata.version —
// rather than the constraint the reference wrote, so a caret reference is
// recorded at the version it resolved to. The digest is the entity row's over that
// same indexed entity (Catalog.entityRowDigest): what a publication of it carries.
// A declared set that cannot be read is returned as the error, because a row
// recorded without it would describe a publication nobody could make.
//
// entityKind is the kind the reference was collected as (closure.EntityKindOf of
// its hint), and the reference resolves as that kind only, so a Recipe reference
// sharing its name with a Component resolves to the Recipe and is pinned by the
// Recipe's own digest. An empty entityKind — a reference whose kind its caller
// does not know — resolves through every kind in the ladder's order.
func (l *catalogLookup) tier1CatalogEntity(uri, entityKind string) (closure.Resolution, bool, error) {
	kind, key, version, ok := l.tier1CatalogEntryOfKind(uri, entityKind)
	if !ok {
		return closure.Resolution{}, false, nil
	}
	d, err := l.catalog.entityRowDigest(kind, key)
	if err != nil {
		return closure.Resolution{}, false, fmt.Errorf("catalog: %s: %w", uri, err)
	}
	return l.tier1Resolution(kind, key, version, d), true, nil
}

// tier1CatalogEntryOfKind is the entry this lookup's catalog indexes that uri
// resolves to: its kind, the key it is indexed under, and the version its kind's
// resolution rule selected. It is the one statement of which entity a reference
// resolves to at this tier, so the resolution tier1CatalogEntity reports and the
// question a pinned-integrity check asks about the same reference
// (Catalog.catalogDirectoryCopies) cannot come to name different entries. It reads
// the rungs a reference collected as entityKind (a closure entity kind) is read at
// (tier1RungsFor). The catalog kind it reports is the rung's entity kind named
// through EntityKindForHint, the one pairing of an entity kind with a catalog kind.
func (l *catalogLookup) tier1CatalogEntryOfKind(uri, entityKind string) (kind, key, version string, ok bool) {
	for _, rung := range l.tier1RungsFor(entityKind) {
		if key, version, ok := rung.resolve(uri); ok {
			return EntityKindForHint(rung.entityKind), key, version, true
		}
	}
	return "", "", "", false
}

// tier1Rung is one kind the local catalog indexes: the closure entity kind it
// answers for, its resolution rule, which reports the key the entity is indexed
// under and the version the rule selected, and the select behind that rule, whose
// refusal says why a reference it did not answer went unanswered
// (tier1UnsatisfiedPin).
type tier1Rung struct {
	entityKind  string
	resolve     func(uri string) (key, version string, ok bool)
	unsatisfied func(uri string) error
}

// tier1Ladder is the local catalog's resolution ladder, in the order a reference
// whose kind is not known tries it.
func (l *catalogLookup) tier1Ladder() []tier1Rung {
	c := l.catalog
	return []tier1Rung{
		{closure.KindComponent, func(uri string) (string, string, bool) {
			if key, ok, _ := c.resolveComponentKey(uri); ok {
				return key, c.Components[key].Metadata.Version, true
			}
			return "", "", false
		}, func(uri string) error { _, _, err := c.resolveComponentKey(uri); return err }},
		{closure.KindFeature, func(uri string) (string, string, bool) {
			if key, ok, _ := c.resolveFeatureKey(uri); ok {
				return key, c.Features[key].Metadata.Version, true
			}
			return "", "", false
		}, func(uri string) error { _, _, err := c.resolveFeatureKey(uri); return err }},
		// Recipe / FeatureRecipe lookups go through RecipeByURI /
		// FeatureRecipeByURI, which implement the provider-qualified /
		// bare-name resolution ladder over the identity-keyed maps. The
		// closure walker passes recipe URIs per Manifest mapping values
		// (bare or provider-qualified), never spec.implements. The retained
		// bytes are keyed by the full provider-scoped identity, so the
		// digest taken under the key returned here is the resolved entity's
		// own bytes even when another provider ships a recipe of the same name.
		{closure.KindRecipe, func(uri string) (string, string, bool) {
			if r, ok := c.RecipeByURI(uri); ok {
				return recipeIdentityKey(r.Spec.Capability, r.Metadata.Name, r.Metadata.Version), r.Metadata.Version, true
			}
			return "", "", false
		}, func(uri string) error {
			_, _, err := selectCapabilityScopedVersion(c.Recipes, uri, recipeVersionMeta)
			return err
		}},
		{closure.KindFeatureRecipe, func(uri string) (string, string, bool) {
			if fr, ok := c.FeatureRecipeByURI(uri); ok {
				return recipeIdentityKey(fr.Spec.Capability, fr.Metadata.Name, fr.Metadata.Version), fr.Metadata.Version, true
			}
			return "", "", false
		}, func(uri string) error {
			_, _, err := selectCapabilityScopedVersion(c.FeatureRecipes, uri, featureRecipeVersionMeta)
			return err
		}},
		// A Plugin is keyed by its coordinate identity (<pluginType>/<name>:version).
		// A workspace plugin socket or a recipe pipeline step references a
		// Plugin ENTITY URI, which the closure walk resolves here; a local
		// (Tier-1) entity must resolve at Tier-1 like every other local entity rather
		// than falling through to a fatal Tier-3 NotFound.
		{closure.KindPlugin, func(uri string) (string, string, bool) {
			if key, ok, _ := c.resolvePluginKey(uri); ok {
				return key, c.Plugins[key].Metadata.Version, true
			}
			return "", "", false
		}, func(uri string) error { _, _, err := c.resolvePluginKey(uri); return err }},
		{closure.KindAggregate, func(uri string) (string, string, bool) {
			if key, ok, _ := c.resolveAggregateKey(uri); ok {
				return key, c.Aggregates[key].Metadata.Version, true
			}
			return "", "", false
		}, func(uri string) error { _, _, err := c.resolveAggregateKey(uri); return err }},
	}
}

// tier1RungsFor is the part of the ladder a reference collected as entityKind (a
// closure entity kind) is read at, in the ladder's order: the rung of that kind
// alone, or every rung where entityKind is empty (closure.KindReadableAs). A kind
// the ladder has no rung for — a Manifest, which is read from beside its Release
// and never indexed here — is read at none.
func (l *catalogLookup) tier1RungsFor(entityKind string) []tier1Rung {
	var rungs []tier1Rung
	for _, rung := range l.tier1Ladder() {
		if closure.KindReadableAs(entityKind, rung.entityKind) {
			rungs = append(rungs, rung)
		}
	}
	return rungs
}

// tier1Library resolves a library from the locally-authored catalog on any axis.
// The version a reference asserts is a floor for selection, so an explicit one in
// the URI wins over the library's own metadata version.
//
// An axis the local catalog holds no map for resolves as a miss rather than an
// error: a library that is not authored locally is exactly what the cache and
// registry tiers below exist to serve.
func (l *catalogLookup) tier1Library(axis closure.LibraryAxis, uri string) (closure.Resolution, map[string]struct{}, bool) {
	name := schemaLibraryNameFromURI(uri)
	metaName, metaVersion, entries, ok := l.localLibrary(axis, name)
	if !ok {
		return closure.Resolution{}, nil, false
	}
	resolved := metaVersion
	if _, v, hasV := resolver.SplitVersionedURI(uri); hasV && v != "" {
		resolved = v
	}
	// A library's row records the digest of its bytes (isLibraryKind).
	d := contentDigest(l.catalog.entityBytes[entityBytesKey(axis.Kind, metaName)])
	return l.tier1Resolution(axis.Kind, metaName, resolved, d), entries, true
}

// localLibrary reads one library out of the locally-authored catalog, returning its
// identity and the set of entry names it publishes.
//
// This is the one place an axis is bound to the catalog map and the spec section
// that hold it — which map and which section is new information per axis, not a
// copy of another axis's — so it is stated here once instead of at every tier.
func (l *catalogLookup) localLibrary(axis closure.LibraryAxis, name string) (metaName, metaVersion string, entries map[string]struct{}, ok bool) {
	entries = map[string]struct{}{}
	switch axis.Kind {
	case "SchemaLibrary":
		lib, found := l.catalog.SchemaLibraries[name]
		if !found {
			return "", "", nil, false
		}
		for n := range lib.Spec.Schemas.All() {
			entries[n] = struct{}{}
		}
		// A SchemaLibrary publishes templates alongside schemas; both are
		// addressable entries of the same library.
		for n := range lib.Spec.Templates.All() {
			entries[n] = struct{}{}
		}
		return lib.Metadata.Name, lib.Metadata.Version, entries, true
	case "MappingLibrary":
		lib, found := l.catalog.MappingLibraries[name]
		if !found {
			return "", "", nil, false
		}
		for n := range lib.Spec.Mappings.All() {
			entries[n] = struct{}{}
		}
		return lib.Metadata.Name, lib.Metadata.Version, entries, true
	case "WorkloadLibrary":
		lib, found := l.catalog.WorkloadLibraries[name]
		if !found {
			return "", "", nil, false
		}
		for n := range lib.Spec.Workloads.All() {
			entries[n] = struct{}{}
		}
		return lib.Metadata.Name, lib.Metadata.Version, entries, true
	case "ArtifactLibrary":
		lib, found := l.catalog.ArtifactLibraries[name]
		if !found {
			return "", "", nil, false
		}
		for n := range lib.Spec.Artifacts.All() {
			entries[n] = struct{}{}
		}
		return lib.Metadata.Name, lib.Metadata.Version, entries, true
	}
	return "", "", nil, false
}

// catalogDirectoryCopies returns the entries this catalog holds from a catalog
// directory that the reference uri resolves to at the catalog tier — through the
// rule tier1CatalogEntryOfKind states for an entity, and localLibrary's for a library on
// each axis — each with the document it was authored in and the digest a
// catalog-tier resolution of the reference pins: the entity row's over what the
// entity's publication would carry, and a library's over its bytes. An entry the
// enumeration placed from a cache slot has no authored document and is not
// returned, and nor is anything from a catalog that records no origins, such as
// one loaded whole from its directories. An entity whose declared set cannot be
// read holds no digest a pin could name, and is not returned either.
//
// kind is the published kind the pin records, and only an entry of that kind is
// returned: a library axis's kind reads that axis alone, and an entity's kind reads
// the catalog tier's rung for it alone (tier1CatalogEntryOfKind), so a document of
// another kind sharing the reference — another entity — never stands in for the
// pinned one.
func (c *Catalog) catalogDirectoryCopies(kind, uri string) []catalogDirectoryCopy {
	lookup := &catalogLookup{catalog: c}
	type entry struct {
		kind, key string
		library   bool
	}
	var entries []entry
	if entityKind := EntityKindForCatalogKind(kind); entityKind != "" {
		if catalogKind, key, _, ok := lookup.tier1CatalogEntryOfKind(uri, entityKind); ok {
			entries = append(entries, entry{catalogKind, key, false})
		}
	}
	name := schemaLibraryNameFromURI(uri)
	for _, axis := range closure.LibraryAxes() {
		if axis.Kind != kind {
			continue
		}
		if metaName, _, _, ok := lookup.localLibrary(axis, name); ok {
			entries = append(entries, entry{axis.Kind, metaName, true})
		}
	}
	var copies []catalogDirectoryCopy
	for _, e := range entries {
		bytesKey := entityBytesKey(e.kind, e.key)
		document, ok := c.origins[bytesKey].authoredDocument()
		if !ok {
			continue
		}
		d := contentDigest(c.entityBytes[bytesKey])
		if !e.library {
			var err error
			if d, err = c.entityRowDigest(e.kind, e.key); err != nil {
				continue
			}
		}
		copies = append(copies, catalogDirectoryCopy{path: document, digest: d})
	}
	return copies
}

// tier1Resolution is the resolution descriptor for the entity this lookup's
// catalog indexes under kind and indexKey, reported at resolvedVersion with the
// digest its row records, which the caller takes: an entity's over what its
// publication carries (tier1CatalogEntity), a library's over its bytes
// (tier1Library).
//
// The resolution path is the catalog's own answer to where that index entry came
// from, never an assumption that an indexed entity was authored in a catalog
// directory: an index can also hold an entity it received from a lockfile-pinned
// cache slot, and a pull-time pin decision keys on the difference.
//
// An entry received from a cache slot is described with the manifest digest the
// read that placed it held its bytes to, as every other cache-tier read describes
// what it served (cacheHitResolution) — so the manifest a signature fact on the
// entry's row is held to having been made for is the manifest the build actually
// read, whichever tier answered. An entry authored in a catalog directory has none.
func (l *catalogLookup) tier1Resolution(kind, indexKey, resolvedVersion, digest string) closure.Resolution {
	return closure.Resolution{
		ResolvedVersion: resolvedVersion,
		Digest:          digest,
		ManifestDigest:  l.catalog.indexedManifestDigest(kind, indexKey),
		ResolutionPath:  l.catalog.indexedResolutionPath(kind, indexKey),
	}
}

// indexedManifestDigest reports the digest of the manifest the entry this catalog
// indexes under kind and indexKey was held to when it was read from a cache slot,
// read from the same record indexedResolutionPath reads, and empty for an entry
// authored in a catalog directory and for any entry of a catalog that holds no such
// record.
func (c *Catalog) indexedManifestDigest(kind, indexKey string) string {
	if origin, ok := c.origins[entityBytesKey(kind, indexKey)]; ok {
		return origin.heldManifestDigest()
	}
	return ""
}

// indexedResolutionPath reports where the entry this catalog indexes under kind
// and indexKey came from: the content cache, for an entry the pinned fold placed
// from a cache slot, and otherwise a catalog directory. It reads the record the
// fold wrote when it placed the entry, which is the one place that fact is
// decided, for every kind the fold places — a Recipe, FeatureRecipe, Plugin or
// Aggregate, and a library on any axis. A catalog that holds no such record — one
// loaded from directories — answers every entry as a catalog-directory one.
func (c *Catalog) indexedResolutionPath(kind, indexKey string) string {
	if origin, ok := c.origins[entityBytesKey(kind, indexKey)]; ok {
		return origin.resolutionPath()
	}
	return closure.ResolutionPathCatalog
}

// tier2 is the cache tier of the closure cascade: the slot the versioned uri
// addresses, when it holds a complete entry, and — where a prior lockfile is in
// play — only while that entry still holds the prior pin. A slot the shared read
// reports as absent or incomplete (tier2ReadSlotUnverified) is a miss here as it
// is at every other reader, so the cascade goes on to the registry, which rewrites
// the slot, or finds nothing where no registry is reachable.
//
// A version that cannot address a slot at all is not a miss. The registry tier
// would be handed the same reference, so the refusal is returned as the error and
// the resolution halts naming the value rather than going on to ask a registry
// for it. Nor is a slot the read hold refused — a cache directory, or a file of the
// slot, leading through a link out of the root the cache is held to: nothing was
// read through it, and the halt names the link rather than a registry fetch
// writing over a place the cache may not reach.
//
// uri is the reference as the caller wrote it and fetchURI the versioned form the
// slot is addressed at, which differ for a bare reference resolved at its pinned
// version (pinnedFetchURI). The prior pin is looked up under the caller's form
// first, as honorPinDigest looks it up, so a bare reference whose lockfile row is
// the bare one alone is held to that pin here too, rather than having its slot
// taken on trust and only the digest refused afterwards; the versioned form is
// the fallback for a pin recorded under it.
func (l *catalogLookup) tier2(kind, uri, fetchURI string) ([]byte, closure.Resolution, bool, error) {
	if kind == "" {
		_, _, _, err := tier2Slot(l.workspaceRoot, l.cacheDir, kind, fetchURI)
		return nil, closure.Resolution{}, false, err
	}
	bytes, manifest, version, err := tier2ReadSlotUnverified(l.workspaceRoot, l.cacheDir, kind, fetchURI)
	if err != nil {
		if refusesTheSlot(err) {
			return nil, closure.Resolution{}, false, err
		}
		return nil, closure.Resolution{}, false, nil
	}
	// A slot whose bytes yield no digest — an archive that does not unpack — holds
	// nothing a pin could name, so it is a miss here as an absent one is, and the
	// cascade goes on to the registry, which rewrites it.
	res, err := cacheHitResolution(version, bytes, manifest)
	if err != nil {
		return nil, closure.Resolution{}, false, nil
	}
	if l.priorPins == nil {
		return bytes, res, true, nil
	}
	pin, havePin := l.priorPins[PinKey{Kind: kind, URI: uri}]
	if !havePin {
		pin, havePin = l.priorPins[PinKey{Kind: kind, URI: fetchURI}]
	}
	if !havePin || slotHoldsPin(res, pin) {
		return bytes, res, true, nil
	}
	// Re-pull reuse-check: the cached slot no longer holds what the prior
	// lockfile pinned. Pass the slot over and record it, so the Tier-3 re-fetch
	// (which rewrites the slot) reports what the slot held, and a Tier-3 miss
	// halts naming it (pinHeldNowhere). Re-establishing the pin from
	// authoritative registry bytes is the correct action where the registry is
	// reachable, rather than laundering the drifted slot into the new lockfile.
	l.refetchPending[PinKey{Kind: kind, URI: fetchURI}] = passedOverSlot{
		pin:               pin.Digest,
		held:              heldInsteadOfPin(res.Digest, pin),
		manifestDisagrees: res.Digest == pin.Digest,
	}
	return nil, closure.Resolution{}, false, nil
}

// passedOverSlot is what the reuse check found in a cache slot it did not reuse:
// the content pin the slot no longer holds, and what it holds instead, worded by
// heldInsteadOfPin. manifestDisagrees records that the slot holds the pinned
// content and it is the manifest beside it that is not the pinned one, which is
// the condition a halt over the slot reports (pinDisagreementCondition).
type passedOverSlot struct {
	pin               string
	held              string
	manifestDisagrees bool
}

// heldInsteadOfPin words what a cache entry holds where a pin names other bytes,
// for a notice or a halt to quote after "holds": the content digest the entry
// holds when that is not the pinned one, and otherwise that the manifest beside the
// pinned content is not the pinned manifest. heldDigest is empty where what the
// entry holds could not be established from the verifying read's refusal — a slot
// whose manifest is not the pinned one, or whose content does not verify against
// its own manifest — and the entry is then worded as one that does not verify.
func heldInsteadOfPin(heldDigest string, pin Pin) string {
	switch {
	case heldDigest == "":
		return fmt.Sprintf("an entry that does not verify against the pinned %s", pin.Digest)
	case heldDigest != pin.Digest:
		return fmt.Sprintf("%s, not the pinned %s", heldDigest, pin.Digest)
	default:
		return fmt.Sprintf("the pinned %s beside a manifest other than the pinned %s", pin.Digest, pin.ManifestDigest)
	}
}

// pinHeldNowhere is the halt for a reference whose cache slot the reuse check
// passed over when the registry tier then served nothing for it. Every source the
// cascade reads has been read by then: the catalog tier, which runs first and did
// not answer; the slot, which holds other bytes; and the registry, which served no
// copy or was not reachable. A pin names bytes, so the reference is not one
// nothing resolves — a slot of its name and version exists — and reporting it as
// such would hide the one fact a reader needs; the halt names each source instead
// (errNoSourceHoldsPin).
//
// It returns nil for every other outcome, which the cascade reports as it always
// has: a registry that served the reference, and a miss the reuse check never saw
// a slot for.
func (l *catalogLookup) pinHeldNowhere(kind, uri, fetchURI string, res closure.Resolution) error {
	if !res.NotFound || l.refetchPending == nil {
		return nil
	}
	slot, passed := l.refetchPending[PinKey{Kind: kind, URI: fetchURI}]
	if !passed {
		return nil
	}
	registry, remedy := fmt.Sprintf("the registry serves no %s", fetchURI), ""
	if l.ociPuller == nil {
		registry = "this run reaches no registry"
		remedy = pinRefetchRemedy
	}
	return errNoSourceHoldsPin(uri, slot.pin, []string{
		fmt.Sprintf("no catalog directory this workspace reads holds %s", fetchURI),
		fmt.Sprintf("the cache entry for %s holds %s", fetchURI, slot.held),
		registry,
	}, remedy, pinDisagreementCondition(slot.manifestDisagrees))
}

// pinRefetchRemedy is the step that supplies a pinned version's bytes again when
// no source a run read held them and no registry was asked: a pull with a registry
// reachable fetches the pinned version, and holds what it fetches to the pin.
const pinRefetchRemedy = "with a registry reachable, `aggrega pull --release <release>` fetches the pinned version again"

// errNoSourceHoldsPin is the halt a read of a pinned entity meets when no source
// it reads holds bytes that hash to the pin. tried names each source read and what
// it held, in the order the read reached them; remedy, when not empty, is the step
// that can supply the pinned bytes. It is the one wording every reader of a pinned
// entity gives it: the build's integrity gate (LazyLookup.VerifyPinnedIntegrity)
// and the pull's reuse check (pinHeldNowhere).
//
// condition is the integrity condition the halt reports, which the reader takes
// from what its own read of the cache entry found (pinDisagreementCondition). The
// wording names every source either way; the condition says which record
// disagreed.
func errNoSourceHoldsPin(uri, pin string, tried []string, remedy string, condition error) error {
	msg := fmt.Sprintf("pinned entity %s: no source holds the pinned bytes %s (%s)", uri, pin, strings.Join(tried, "; "))
	if remedy != "" {
		msg += "; " + remedy
	}
	return fmt.Errorf("%s: %w", msg, condition)
}

// pinDisagreementCondition returns the integrity condition a halt reports for a
// cache entry that does not hold its pin: the cached-manifest condition where it is
// the manifest persisted beside the content that disagrees with the manifest digest
// the pin records, and the fetch-integrity condition where it is the content —
// other content than the pin names, or content that does not verify against its
// own manifest. Each reader supplies which of the two its own read found: the
// build's integrity gate from its verifying read's refusal, which compares the
// manifest first (tier2ReadVerifiedWithManifest), and the pull's reuse check from
// the entry's digests against the pin (slotHoldsPin). So a build, a verifying pull
// and a pull that halt over one entry name one condition for it, and a tampered
// manifest is reported as a manifest that does not match its pin rather than as
// content that does not match.
func pinDisagreementCondition(manifestDisagrees bool) error {
	if manifestDisagrees {
		return oci.ErrCachedManifestIntegrity()
	}
	return oci.ErrFetchIntegrity()
}

// slotHoldsPin reports whether a cache hit still holds what a prior lockfile pin
// recorded for it: the content digest, and the manifest digest wherever the pin
// records one.
//
// The manifest is held to the pin as well as the content because a cache hit now
// reports the manifest it holds, and what it reports is written into the lockfile.
// A check on the content alone would therefore carry a replaced manifest into the
// new lockfile under the old content pin — the same laundering the content check
// exists to prevent, one field over. A pin that records no manifest digest — an
// entry resolved without one, or a row written before cache hits reported it —
// is held to its content alone, as it always was.
func slotHoldsPin(hit closure.Resolution, pin Pin) bool {
	if hit.Digest != pin.Digest {
		return false
	}
	return pin.ManifestDigest == "" || hit.ManifestDigest == pin.ManifestDigest
}

// tier3CatalogEntity fetches uri for a reference whose cache slot is kept under
// slotKind — the published kind the reference was collected as — and writes it to
// that slot (tier3PullCatalogEntity), then records the re-fetch notice for the slot
// the reuse check passed over.
func (l *catalogLookup) tier3CatalogEntity(ctx context.Context, uri, slotKind string) ([]byte, closure.Resolution, error) {
	bytes, res, err := tier3PullCatalogEntity(ctx, l.workspaceRoot, l.cacheDir, l.ociPuller, uri, slotKind)
	if err == nil && !res.NotFound {
		l.recordRefetchNotice(slotKind, uri, res.Digest)
	}
	return bytes, res, err
}

// recordRefetchNotice emits a re-fetch notice via the configured warn
// sink when uri was re-pulled because its cached slot drifted from the
// prior pin (the Tier-2 reuse-check flagged it). A Tier-3 pull reached
// by a plain cache miss is not flagged and records nothing. The notice
// distinguishes a benign refresh (the slot was stale but the registry
// content still hashes to the prior pin), which names what the slot held
// beside the pin, from a moved pin (the registry content changed — the
// reproducibility baseline shifted).
func (l *catalogLookup) recordRefetchNotice(kind, uri, newDigest string) {
	if l.refetchPending == nil {
		return
	}
	key := PinKey{Kind: kind, URI: uri}
	slot, flagged := l.refetchPending[key]
	if !flagged {
		return
	}
	delete(l.refetchPending, key)
	if l.warn == nil {
		return
	}
	if newDigest == slot.pin {
		l.warn(fmt.Sprintf("cache entry for %s held %s; refreshed from the registry (pin unchanged)", uri, slot.held))
		return
	}
	l.warn(fmt.Sprintf("pin for %s moved %s -> %s; the reproducibility baseline changed", uri, slot.pin, newDigest))
}

// tier2ReadSlotUnverified reads the cached entity for a versioned URI from the
// workspace cache directory — the content and the OCI manifest persisted beside
// it — WITHOUT verifying either against any pin.
//
// DO NOT call this from a new read path. Integrity lives at the cache boundary:
// every cache read that returns bytes to a consumer must verify them against a
// lockfile pin via tier2ReadVerified. This unverified primitive survives for
// exactly two internal callers that verify the bytes themselves and must see the
// raw slot to do so:
//   - tier2ReadVerifiedWithManifest — verifies the manifest against the pin where
//     one is recorded, the content against the manifest, and the content against
//     the required lockfile pin (the one verifying read all consumers route through).
//   - catalogLookup.tier2 — the pull reuse-check, which compares the slot digest
//     to the PRIOR lockfile pin to decide re-fetch vs reuse (build reaches it
//     with no prior pin and relies on the closure-level lockfile.Verify gate).
//
// A new caller that wants cache bytes is a gap to wire through tier2ReadVerified,
// never a reason to reach for this. (See the verify-on-read posture in
// pkg/sourcecache.LookupAndExtract, which this mirrors.)
//
// Both callers meet a slot through this read, so both meet the rule
// persistedManifest decides in the same way: a slot holding content but no
// manifest is an incomplete entry and is reported exactly as an absent one. The
// pull's reuse check and the build's verifying read therefore cannot come to
// disagree about such a slot — neither is handed its content.
//
// It returns, beside the bytes and the manifest, the version the slot is keyed
// by, which is what a caller describing the hit reports it at
// (cacheHitResolution).
//
// Returns an error wrapping oci.ErrFetchNotFound when the cache directory is
// unset, when the URI carries no version pin, when the cache slot holds no
// readable content, or when it holds content but no manifest; a manifest that
// exists but cannot be read is reported as that failure, and a version that
// cannot address a slot as the refusal oci.CachePath returns. A file of the slot
// that leads through a link out of the root the cache is held to is not read, and
// the read hold's refusal is returned as itself rather than as a miss, so no
// caller fetches around it (oci.ReadCachedFile).
func tier2ReadSlotUnverified(workspaceRoot, cacheDir, kind, uri string) ([]byte, []byte, string, error) {
	_, version, ok, err := tier2Slot(workspaceRoot, cacheDir, kind, uri)
	if err != nil {
		return nil, nil, "", err
	}
	if !ok {
		return nil, nil, "", errNotInContentCache(uri)
	}
	bytes, err := readCacheSlotBytes(workspaceRoot, cacheDir, kind, uri, version)
	if err != nil {
		if errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
			return nil, nil, "", err
		}
		return nil, nil, "", errNotInContentCache(uri)
	}
	manifest, err := persistedManifest(workspaceRoot, cacheDir, kind, uri, version)
	if err != nil {
		return nil, nil, "", err
	}
	return bytes, manifest, version, nil
}

// errNotInContentCache is the miss a cache read reports for a slot that holds no
// content at all, with the pull that fills it as the remedy.
func errNotInContentCache(uri string) error {
	return fmt.Errorf("catalog: %s not present in the content cache; run `aggrega pull --release <release>` first: %w", uri, oci.ErrFetchNotFound())
}

// persistedManifest returns the OCI manifest persisted in the cache slot for uri
// at version, beside the content a reader has found there.
//
// It is the one home of the rule that a cache slot holding content but no
// manifest is an incomplete entry, and every reader in this package that serves a
// slot's content reads the manifest through it: the Tier-2 read beneath the pull's
// reuse check and the build's verifying read (tier2ReadSlotUnverified), the cached
// plugin entity a verdict socket reads (LazyLookup.CachedPluginByURI), and a
// plugin's content-addressed .wasm (catalogLookup.readCachedPluginArtifact).
//
// The rule holds because a registry fetch writes both files — the content, then
// the manifest the registry served for it (writeEntityToCacheSlot,
// cachePluginArtifact) — and the manifest is the record the content is verified
// against. A slot with content and no manifest is therefore a write that stopped
// between the two, or a file placed there by hand; it is never what a fetch left.
// Such a slot is reported as absent, wrapping oci.ErrFetchNotFound, so each reader
// treats it as the miss it is: a pull or a verifying pull fetches the entity again
// where a registry is reachable, and otherwise — and always at a build, which
// fetches nothing — the reference is refused as one nothing resolves. "No
// manifest" means no manifest bytes — a missing file or an empty one — the same
// reading cacheHitResolution gives a slot holding none.
//
// The rule covers the entity cache alone. A source artifact's slot, which a
// non-OCI source legitimately fills without a manifest, is read by
// pkg/sourcecache and is not reached from here.
//
// A manifest that exists but cannot be read is an error of its own, not a miss.
func persistedManifest(workspaceRoot, cacheDir, kind, uri, version string) ([]byte, error) {
	manifest, err := oci.ReadCachedManifest(workspaceRoot, cacheDir, kind, uri, version)
	if err != nil && !errors.Is(err, oci.ErrFetchNotFound()) {
		return nil, fmt.Errorf("catalog: read cached manifest for %s: %w", uri, err)
	}
	if len(manifest) == 0 {
		return nil, fmt.Errorf("catalog: %s cached without its OCI manifest; run `aggrega pull --release <release>` first: %w", uri, oci.ErrFetchNotFound())
	}
	return manifest, nil
}

// cacheHitResolution is the one description of an entry the content cache served:
// the version its slot is keyed by, the digest the row pinning the bytes the slot
// holds records for them (PinDigest), and the digest of the OCI manifest persisted
// beside them — every fact the registry read that filled the slot reported —
// marked as served from the cache. Every cache-tier read describes what it served
// through this, so a warm pull reports what a cold pull did and the lockfile it
// writes is the same.
//
// The manifest digest is derived from the persisted manifest rather than carried
// in from a record, so a cache hit reports the manifest it actually holds and a
// reader that holds it to a pin compares like with like. Nil or empty manifest
// bytes are described with none; no cache reader hands it such a slot, because a
// slot holding no manifest is not an entry at all (persistedManifest).
//
// It verifies nothing. What a reader has established about the bytes before
// describing them is that reader's own discipline, and each states it where it
// reads. Bytes that yield no digest — an archive that does not unpack — are
// returned as the error.
func cacheHitResolution(version string, content, manifest []byte) (closure.Resolution, error) {
	d, err := PinDigest(content)
	if err != nil {
		return closure.Resolution{}, err
	}
	res := closure.Resolution{
		ResolvedVersion: version,
		Digest:          d,
		ResolutionPath:  closure.ResolutionPathCache,
	}
	if len(manifest) > 0 {
		res.ManifestDigest = contentDigest(manifest)
	}
	return res, nil
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
// slot is an incomplete cache entry, not a content-pin-only fallback: the rule
// persistedManifest decides for every reader) — in either case the caller issues
// a pull-first remediation. Returns
// ErrFetchIntegrity when the cached content does not match the pin. An empty
// expectedDigest is a programming error at the call site (a read that cannot
// supply a pin is a gap to wire, never a silent skip) and is reported as such.
func tier2ReadVerified(workspaceRoot, cacheDir, kind, uri, expectedDigest, expectedManifestDigest string) ([]byte, error) {
	bytes, _, err := tier2ReadVerifiedWithManifest(workspaceRoot, cacheDir, kind, uri, expectedDigest, expectedManifestDigest)
	return bytes, err
}

// tier2ReadVerifiedWithManifest is tier2ReadVerified returning, beside the
// verified bytes, the persisted manifest they were verified against, for a reader
// that describes what it served (cacheHitResolution) and so needs the manifest
// digest. It is the one verifying read; tier2ReadVerified is this read with the
// manifest dropped, so the two cannot come to verify differently.
func tier2ReadVerifiedWithManifest(workspaceRoot, cacheDir, kind, uri, expectedDigest, expectedManifestDigest string) ([]byte, []byte, error) {
	if expectedDigest == "" {
		return nil, nil, fmt.Errorf("catalog: tier-2 verified read of %s requires a non-empty expected digest", uri)
	}
	// The slot read reports an absent slot and an incomplete one — content with no
	// manifest beside it — alike, as the miss persistedManifest decides, so the
	// caller re-pulls either way.
	bytes, manifestBytes, _, err := tier2ReadSlotUnverified(workspaceRoot, cacheDir, kind, uri)
	if err != nil {
		return nil, nil, err
	}
	// The download is held to the manifest: the manifest to the pinned manifest
	// digest where the pin records one, and the bytes to the digest the manifest's
	// descriptor names for them. That is transport integrity, and it is complete
	// before the pin's own digest is read. The pin's digest is then compared over
	// the subject its row records (PinDigest): for an entity, what the publication
	// carries rather than its compressed bytes, which is why a recompression of the
	// same entries still verifies.
	// Manifest authority. Every registry pull persists the OCI manifest beside the
	// content; it is the root the content is verified against. Verify the manifest
	// against the independent pin (when one is recorded — a registry-resolved
	// entity), then verify the content against the manifest's layer digest, so a
	// forged content+content-pin pair cannot survive an untampered manifest.
	if expectedManifestDigest != "" {
		if err := oci.VerifyManifestDigest(manifestBytes, expectedManifestDigest); err != nil {
			return nil, nil, fmt.Errorf("catalog: cached manifest for %s: %w", uri, err)
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
			return nil, nil, fmt.Errorf("catalog: cached %s: %w", uri, err)
		}
	} else if err := oci.VerifyContentAgainstManifest(manifestBytes, bytes); err != nil {
		return nil, nil, fmt.Errorf("catalog: cached %s: %w", uri, err)
	}
	// Content still matches the recorded content pin.
	got, derr := PinDigest(bytes)
	if derr != nil {
		return nil, nil, fmt.Errorf("catalog: cached %s: %w", uri, derr)
	}
	if got != expectedDigest {
		return nil, nil, &cachedContentMismatch{uri: uri, held: got, pin: expectedDigest}
	}
	return bytes, manifestBytes, nil
}

// cachedContentMismatch is the verifying read's refusal of a complete cache entry
// whose content, verified against the manifest beside it, is not the content the
// pin names: an entry of the same name and version holding other bytes, as an
// entity edited in place leaves behind. It is the fetch-integrity condition, and it
// carries the digest the entry holds, so a caller that goes on to read another
// source for the pinned bytes can name both digests without reading the entry a
// second time.
type cachedContentMismatch struct {
	uri  string
	held string
	pin  string
}

func (e *cachedContentMismatch) Error() string {
	return fmt.Sprintf("catalog: cached %s content digest %s does not match lockfile pin %s: %s",
		e.uri, e.held, e.pin, oci.ErrFetchIntegrity().Error())
}

func (e *cachedContentMismatch) Unwrap() error { return oci.ErrFetchIntegrity() }

// tier3PullCatalogEntity is the package-level Tier-3 OCI pull
// primitive. Pulls the catalog entity at uri from the registry via
// the supplied OCIPuller and writes the pulled bytes to the
// workspace cache slot. Returns the bytes plus the standard
// closure.Resolution descriptor.
//
// slotKind is the published kind the reader keeps the reference's cache slot
// under (EntityKindForHint), and the one kind this pull reads: the fetch binds by
// the kind derived from it (registryFetchKind), the pulled body is put through
// that kind's schema before it is written (admitFetchedEntity), so an entity the
// schema refuses halts here and nothing is cached for it, and the bytes are
// written to the slot under it. A reference with no slot kind has no slot any
// reader addresses, so nothing is written for it.
//
// Returns (nil, Resolution{NotFound: true}, nil) when the puller is
// nil (hermetic-build path) or when the URI cannot be split into a
// valid OCI reference + tag. Returns a wrapped error on pull, schema or
// cache-write failure.
func tier3PullCatalogEntity(ctx context.Context, workspaceRoot, cacheDir string, puller OCIPuller, uri, slotKind string) ([]byte, closure.Resolution, error) {
	if puller == nil {
		return nil, closure.Resolution{NotFound: true}, nil
	}
	kind := registryFetchKind(slotKind)
	// A bare catalog identity carries no registry or kind segment. When the kind
	// is known and the puller can bind it, fetch through EntityPuller — the
	// pull-side mirror of push (`<registry>/<OCILocation(kind, …)>`) — resolving a
	// version-less reference (e.g. an Aggregate component `type` with no version)
	// by list-tags→highest, mirroring libraries/aggregates. This is the only path
	// that can fetch a bare identity; without the kind or the capability the entity
	// tier falls to the legacy already-located, versioned reference path.
	if ep, ok := puller.(EntityPuller); ok && kind != "" {
		name, floor, hasFloor := resolver.SplitVersionedURI(uri)
		result, resolvedVersion, err := ep.PullEntity(ctx, name, kind, floor)
		if err != nil {
			if errors.Is(err, oci.ErrFetchNotFound()) {
				return nil, closure.Resolution{NotFound: true}, nil
			}
			// For the EntityPuller path the bound host is the configured registry's,
			// not the bare identity's leading segment — ask the puller for the
			// prefix it binds under and read the host out of it, so the docker-login
			// remediation names the host a login is stored for and never the path.
			host := registryhost.Of(name)
			if rp, ok := puller.(interface{ RegistryPrefix() string }); ok {
				host = registryhost.Of(rp.RegistryPrefix())
			}
			// remediationFor, not oci.RemediationFor: this is the one pull path that
			// can surface a reference-binding refusal (PullEntity binds the identity
			// before it fetches), and those sentinels are declared in this package, so
			// only the catalog-side mapping reaches them. It delegates to the fetch
			// mapping first, so every fetch remediation still arrives unchanged.
			if rem := remediationFor(err, host); rem != "" {
				return nil, closure.Resolution{}, fmt.Errorf("catalog: tier-3 pull %s: %w\n  %s", uri, err, rem)
			}
			return nil, closure.Resolution{}, fmt.Errorf("catalog: tier-3 pull %s: %w", uri, err)
		}
		if aerr := admitFetchedEntity(slotKind, result.Bytes, name+":"+resolvedVersion); aerr != nil {
			return nil, closure.Resolution{}, aerr
		}
		// A bare-name reference pins the fetched body's metadata.version (identity);
		// a floored reference keeps the floor. The cache slot is keyed by the
		// closure URI at the pinned version (writeEntityToCacheSlot), the key every
		// reader of the pin addresses, so a bare reference is cached under the
		// pinned version appended to it rather than under the reference alone.
		pinnedVersion := resolvedVersion
		if !hasFloor {
			mv, verr := entityMetadataVersion(result.Bytes)
			if verr != nil {
				return nil, closure.Resolution{}, fmt.Errorf("catalog: tier-3 pull %s: %w", uri, verr)
			}
			if mv != "" {
				pinnedVersion = mv
			}
		}
		d, derr := oci.PublishedDigest(result.Bytes)
		if derr != nil {
			return nil, closure.Resolution{}, fmt.Errorf("catalog: tier-3 pull %s: %w", uri, derr)
		}
		if err := writeEntityToCacheSlot(workspaceRoot, cacheDir, slotKind, uri, pinnedVersion, result); err != nil {
			return nil, closure.Resolution{}, err
		}
		return result.Bytes, closure.Resolution{
			ResolvedVersion: pinnedVersion,
			Digest:          d,
			ManifestDigest:  result.Digest,
			ResolutionPath:  closure.ResolutionPathRegistry,
		}, nil
	}

	// Legacy already-located, versioned reference path (no kind / no capability).
	reference, tag, ok := SplitURIForOCI(uri)
	if !ok {
		return nil, closure.Resolution{NotFound: true}, nil
	}
	result, err := puller.Pull(ctx, reference, tag)
	if err != nil {
		if errors.Is(err, oci.ErrFetchNotFound()) {
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
	if aerr := admitFetchedEntity(slotKind, result.Bytes, reference+":"+tag); aerr != nil {
		return nil, closure.Resolution{}, aerr
	}
	d, derr := oci.PublishedDigest(result.Bytes)
	if derr != nil {
		return nil, closure.Resolution{}, fmt.Errorf("catalog: tier-3 pull %s: %w", uri, derr)
	}
	if err := writeEntityToCacheSlot(workspaceRoot, cacheDir, slotKind, uri, tag, result); err != nil {
		return nil, closure.Resolution{}, err
	}
	return result.Bytes, closure.Resolution{
		ResolvedVersion: tag,
		Digest:          d,
		ManifestDigest:  result.Digest,
		ResolutionPath:  closure.ResolutionPathRegistry,
	}, nil
}

// tier3Library is the registry tier of a library on any axis: it fetches the
// library, puts the fetched body through the schema of the axis's kind before
// anything is cached (admitFetchedEntity), and caches it. A published library its
// own schema refuses therefore halts the resolution here rather than being
// cached and then passed over when the cached copy is read.
func (l *catalogLookup) tier3Library(ctx context.Context, uri, kind string) (closure.Resolution, error) {
	if l.ociPuller == nil {
		return closure.Resolution{NotFound: true}, nil
	}

	// Bare-name identity (the form discovery records): resolve location + version
	// via the LibraryPuller capability — name→location, then list-tags→highest
	// (when the URI carries no version) or the asserted floor as the tag (when a
	// consumer's `@version` threaded a `name:floor` URI here). The cache
	// slot is keyed `<name>:<version>` (writeEntityToCacheSlot) — the symmetric
	// key the lazy lookup's verified Tier-2 read addresses.
	if lp, ok := l.ociPuller.(LibraryPuller); ok {
		name, floor, hasFloor := resolver.SplitVersionedURI(uri)
		result, resolvedVersion, err := lp.PullLibrary(ctx, name, kind, floor)
		if err != nil {
			if errors.Is(err, oci.ErrFetchNotFound()) {
				return closure.Resolution{NotFound: true}, nil
			}
			return closure.Resolution{}, fmt.Errorf("catalog: tier-3 pull %s %s: %w", kind, name, err)
		}
		if aerr := admitFetchedEntity(kind, result.Bytes, name+":"+resolvedVersion); aerr != nil {
			return closure.Resolution{}, aerr
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
			mv, verr := entityMetadataVersion(result.Bytes)
			if verr != nil {
				return closure.Resolution{}, fmt.Errorf("catalog: tier-3 pull %s %s: %w", kind, name, verr)
			}
			if mv != "" {
				pinnedVersion = mv
			}
		}
		if err := writeEntityToCacheSlot(l.workspaceRoot, l.cacheDir, kind, name, pinnedVersion, result); err != nil {
			return closure.Resolution{}, err
		}
		digest := contentDigest(result.Bytes)
		l.recordRefetchNotice(kind, uri, digest)
		return closure.Resolution{
			ResolvedVersion: pinnedVersion,
			Digest:          digest,
			ManifestDigest:  result.Digest,
			ResolutionPath:  closure.ResolutionPathRegistry,
		}, nil
	}

	// Pull-only puller (no library capability): the legacy full-reference path for
	// an already-located, versioned URI.
	reference, tag, ok := SplitURIForOCI(uri)
	if !ok {
		return closure.Resolution{NotFound: true}, nil
	}
	result, err := l.ociPuller.Pull(ctx, reference, tag)
	if err != nil {
		if errors.Is(err, oci.ErrFetchNotFound()) {
			return closure.Resolution{NotFound: true}, nil
		}
		return closure.Resolution{}, fmt.Errorf("catalog: tier-3 pull %s %s: %w", kind, uri, err)
	}
	if aerr := admitFetchedEntity(kind, result.Bytes, reference+":"+tag); aerr != nil {
		return closure.Resolution{}, aerr
	}
	if err := writeEntityToCacheSlot(l.workspaceRoot, l.cacheDir, kind, uri, tag, result); err != nil {
		return closure.Resolution{}, err
	}
	digest := contentDigest(result.Bytes)
	l.recordRefetchNotice(kind, uri, digest)
	return closure.Resolution{
		ResolvedVersion: tag,
		Digest:          digest,
		ManifestDigest:  result.Digest,
		ResolutionPath:  closure.ResolutionPathRegistry,
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
func (l *catalogLookup) tier3Aggregate(ctx context.Context, uri string) (closure.Resolution, error) {
	_, version, digest, manifestDigest, found, err := PullAndCacheAggregate(ctx, l.workspaceRoot, l.cacheDir, l.ociPuller, uri)
	if err != nil {
		return closure.Resolution{}, err
	}
	if !found {
		return closure.Resolution{NotFound: true}, nil
	}
	l.recordRefetchNotice(aggregateKind, uri, digest)
	return closure.Resolution{
		ResolvedVersion: version,
		Digest:          digest,
		ManifestDigest:  manifestDigest,
		ResolutionPath:  closure.ResolutionPathRegistry,
	}, nil
}

// PullAndCacheAggregate fetches a bare-name published Aggregate via the puller's
// AggregatePuller capability, caches it under name:metadata.version, and returns
// the entity document + resolved version (the fetched metadata.version identity)
// + content digest + the OCI manifest digest the artifact's signature commits
// to. found is false when the puller is nil (the hermetic build path), lacks the
// AggregatePuller capability, or the registry has no such aggregate
// (ErrFetchNotFound) — so a build, or an offline pull, never networks. It is the
// single fetch+cache+pin primitive shared by the closure's tier3Aggregate (sub
// path) and the triad resolver's Tier-3 seam (top-level path): one cache layout,
// one identity rule, two call sites. The fetched body is put through the
// Aggregate schema before anything is cached (admitFetchedEntity), so an
// Aggregate its own schema refuses is never written.
//
// # The document is returned, the digest covers the artifact
//
// data is what a caller parses and interpolates, so it is the entity document,
// already taken out of whatever shape the coordinate served — the caller does
// not meet the published shape and therefore cannot get it wrong. digest is the
// entity row's over everything the fetched artifact carries
// (oci.PublishedDigest), because that is what the content pin commits to; the
// two are deliberately not the same thing, and collapsing them either pins less
// than was fetched or hands a parser something that is not a document.
func PullAndCacheAggregate(ctx context.Context, workspaceRoot, cacheDir string, puller OCIPuller, ref string) (data []byte, version, digest, manifestDigest string, found bool, err error) {
	if puller == nil {
		return nil, "", "", "", false, nil
	}
	ap, ok := puller.(AggregatePuller)
	if !ok {
		return nil, "", "", "", false, nil
	}
	name, floor, _ := resolver.SplitVersionedURI(ref)
	result, fetchVersion, perr := ap.PullAggregate(ctx, name, floor)
	if perr != nil {
		if errors.Is(perr, oci.ErrFetchNotFound()) {
			return nil, "", "", "", false, nil
		}
		return nil, "", "", "", false, fmt.Errorf("catalog: tier-3 pull Aggregate %s: %w", name, perr)
	}
	if aerr := admitFetchedEntity(aggregateKind, result.Bytes, name+":"+fetchVersion); aerr != nil {
		return nil, "", "", "", false, aerr
	}
	// Pin the fetched entity's metadata.version (identity), not the OCI tag it
	// was fetched under (fetchVersion, a location coordinate now discarded) — so
	// the pin matches the build-side tier-1 (bridged) derivation and a
	// tag != metadata.version publish does not drift. Fall back to the fetch
	// coordinate only for a body that carries no version.
	version, verr := entityMetadataVersion(result.Bytes)
	if verr != nil {
		return nil, "", "", "", false, fmt.Errorf("catalog: tier-3 pull Aggregate %s: %w", name, verr)
	}
	if version == "" {
		version = fetchVersion
	}
	if werr := writeEntityToCacheSlot(workspaceRoot, cacheDir, aggregateKind, name, version, result); werr != nil {
		return nil, "", "", "", false, werr
	}
	document, derr := oci.EntityYAML(result.Bytes)
	if derr != nil {
		return nil, "", "", "", false, fmt.Errorf("catalog: tier-3 pull Aggregate %s: %w", name, derr)
	}
	d, perr := oci.PublishedDigest(result.Bytes)
	if perr != nil {
		return nil, "", "", "", false, fmt.Errorf("catalog: tier-3 pull Aggregate %s: %w", name, perr)
	}
	return document, version, d, result.Digest, true, nil
}

// admitFetchedEntity puts a body a registry fetch returned through the schema of
// the kind it was fetched as, before anything is written for it: the same
// envelope gate the parse that later reads the cached entity applies (the kind's
// parse in catalog.go). No rule is added by it, only moved ahead of the write, so
// a published entity its own schema refuses halts the fetch instead of landing in
// the cache and being refused, or skipped, when something reads it.
//
// kind is the published kind the body's cache slot is kept under, which is the
// kind every later reader of the slot parses it as. A fetch for a reference with
// no such kind — one whose kind its caller did not know — is held to the kind the
// document itself declares; no slot is written for it. A body that carries no
// entity document at all is refused by the same unwrap every reader applies.
//
// coordinate names what was fetched, as `<name>:<version>`; the refusal positions
// its findings against it, since the document has no file on this machine yet.
func admitFetchedEntity(kind string, published []byte, coordinate string) error {
	document, err := oci.EntityYAML(published)
	if err != nil {
		return fmt.Errorf("fetched %s was not cached: %w", coordinate, err)
	}
	if kind == "" {
		kind = peekEntityKind(document)
	}
	if _, err := envelope.ValidateBytes(envelope.Kind(kind), document, coordinate); err != nil {
		return fmt.Errorf("fetched %s was not cached: %w", coordinate, err)
	}
	return nil
}

// writeEntityToCacheSlot stores the pulled bytes at the standard
// cache slot for kind and uri@version — kind being the published kind the slot is
// kept under (oci.CachePath). A library on any declared axis lands
// under the canonical "library.yaml" filename (matching the
// local-authoring layout); every other mediatype writes via
// pkg/oci.WriteCached's "blob" filename. An empty kind names no slot: every
// reader addresses a slot under a published kind (tier2Slot), so nothing is
// written for a reference whose kind its caller did not know.
//
// The slot is keyed by versionedSlotURI(uri, version), the key every reader of a
// pinned entity addresses, so a caller hands over the reference it resolved —
// bare or versioned — and never composes the key itself. A bare reference keyed
// by itself would be cached where no pinned read looks: the pull that fetched it
// would pin it, and its own render, a later pull and the build would each find
// its slot empty.
//
// A failed write is returned as pkg/oci words it — what was being done, with which
// path, and the operating system's reason — and the registry tiers return it as it
// is, leaving the entity to the frame that asked for it: the dependency walk's frame
// names the reference it was resolving, so a frame of their own would name it a
// second time.
func writeEntityToCacheSlot(workspaceRoot, cacheDir, kind, uri, version string, result *oci.PullResult) error {
	if cacheDir == "" || kind == "" {
		return nil
	}
	uri = versionedSlotURI(uri, version)
	if isLibraryMediaType(result.MediaType) {
		if err := oci.WriteCachedFile(workspaceRoot, cacheDir, kind, uri, version, "library.yaml", result.Bytes); err != nil {
			return err
		}
	} else {
		if err := oci.WriteCached(workspaceRoot, cacheDir, kind, uri, version, result.Bytes); err != nil {
			return err
		}
	}
	// Persist the OCI manifest as a second self-verifying blob in the same slot.
	// The offline read verifies the cached content against the manifest's layer
	// digest — the manifest is the authority — rather than trusting the recorded
	// content pin alone. A slot written without a manifest is incomplete, and every
	// reader of the slot treats it as a cache miss (persistedManifest).
	if len(result.ManifestBytes) > 0 {
		if err := oci.WriteCachedManifest(workspaceRoot, cacheDir, kind, uri, version, result.ManifestBytes); err != nil {
			return err
		}
	}
	return nil
}

// isLibraryMediaType reports whether mt is the mediatype of a library on any
// declared axis, published as its document.
//
// It is derived from the declared axis set rather than listed here, because a list
// is what let this decision cover some axes and not others: the branch it gates
// named two of the four, so the remaining two were cached under a different
// filename than their siblings with nothing reporting the difference.
//
// It matches the document shape alone because what it gates writes the bytes as a
// YAML file: a library declares no file beside its document, so the document is
// the one shape it publishes in, and an archive that claimed a library's kind would
// be cached as the archive it is rather than under a YAML file name.
func isLibraryMediaType(mt string) bool {
	for _, axis := range closure.LibraryAxes() {
		if libraryMT, err := oci.MediaTypeFor(axis.Kind, false); err == nil && libraryMT == mt {
			return true
		}
	}
	return false
}

// tier2Slot returns the cache slot a Tier-2 read of the versioned URI uri
// addresses, and the version that slot is keyed by, or false when there is no
// such slot to read: no cache directory is configured, or the URI carries no
// version. It is the one derivation of that slot, so the read that verifies a
// slot's bytes and the unpack that lays them out afterwards cannot come to
// address different directories.
//
// The version is read out of uri, so it is whatever the reference carries — an
// author's `name:version`, or a pin a record supplied — and a version that would
// address a place outside the slot's own is refused rather than read from; the
// refusal is the error, returned with false. See oci.CachePath.
func tier2Slot(workspaceRoot, cacheDir, kind, uri string) (string, string, bool, error) {
	if kind == "" {
		// Every slot is kept under a published kind, and every reader names one — a
		// pin's kind, or the kind a reference was collected as — so a read naming none
		// is a fault in its caller, not a slot that happens to be absent.
		return "", "", false, fmt.Errorf("internal: catalog: a cache slot for %q was asked for with no kind; "+
			"this is a defect in the compiler rather than in the workspace — report it with the Release", uri)
	}
	if cacheDir == "" {
		return "", "", false, nil
	}
	_, version, hasV := resolver.SplitVersionedURI(uri)
	if !hasV || version == "" {
		return "", "", false, nil
	}
	slot, err := oci.CachePath(workspaceRoot, cacheDir, kind, uri, version)
	if err != nil {
		return "", "", false, err
	}
	return slot, version, true, nil
}

// refusesTheSlot reports whether err refuses the cache slot a read addressed rather
// than reporting what the slot held: a version that cannot address a slot
// (oci.ErrCacheLocationOutsideCache), or a slot the read hold refused because the
// cache directory, or a file of the slot, leads through a link out of the root the
// cache is held to (readscope.ErrReadLeadsOutOfWorkspace). Neither is a miss, so
// neither sends a resolution on to the registry tier, whose fetch would be written
// to the same slot.
func refusesTheSlot(err error) bool {
	return errors.Is(err, oci.ErrCacheLocationOutsideCache()) || errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace())
}

// tier3Slot returns the cache slot tier3PullCatalogEntity wrote the bytes it
// resolved uri to, given the resolution it returned: the slot is keyed as the
// write keyed it, by versionedSlotURI over the URI as passed and the version the
// resolution records, which is the version the write used. It returns "" where
// no slot was written, because no cache directory is configured, and the refusal
// oci.CachePath returns where the version could not address a slot — which the
// write it follows would have refused first.
func tier3Slot(workspaceRoot, cacheDir, kind, uri string, res closure.Resolution) (string, error) {
	if cacheDir == "" {
		return "", nil
	}
	return oci.CachePath(workspaceRoot, cacheDir, kind, versionedSlotURI(uri, res.ResolvedVersion), res.ResolvedVersion)
}

// unpackEntitySlot lays out, in the cache slot at slot, the files the published
// artifact the slot holds carries, and returns the path of the entity's document
// among them together with the document itself.
//
// published is the slot's artifact, and it must already be verified: the caller
// has checked it against its pin, or has just fetched it from the registry under
// the digest the registry named. The layout is rebuilt from those bytes on every
// call, through the same regeneration the source-artifact cache uses, so nothing
// found in the slot's extracted directory is ever trusted on its own — a file
// changed or added there by hand is discarded rather than read, and an artifact
// that fails verification never reaches this point, so it is never laid out. That
// regeneration also leaves a file already holding the artifact's bytes untouched,
// which is what lets another read of the same slot — a concurrent request, or a
// second command over the same workspace — go on reading the layout it was handed
// while this one rebuilds it.
//
// An archive is laid out as its entries, and a document published on its own is
// laid out as that one file. Handing the entity's parse the document's path in
// this layout is what makes a path the entity declares relative to its own
// document — a Recipe's local source, a file an `$import:` names — resolve inside
// what was published, exactly as it resolves beside the entity in a catalog
// directory; and the rule refusing a content path that leaves the entity's own
// directory holds over the layout unchanged, since that directory is the layout.
func unpackEntitySlot(workspaceRoot, cacheDir, slot string, published []byte) (string, []byte, error) {
	name, document, err := oci.EntityFile(published)
	if err != nil {
		return "", nil, err
	}
	var dir string
	if oci.IsTarGzBundle(published) {
		dir, err = sourcecache.RegenerateExtracted(workspaceRoot, cacheDir, slot, published)
	} else {
		dir, err = sourcecache.RegenerateExtractedDocument(workspaceRoot, cacheDir, slot, name, published)
	}
	if err != nil {
		return "", nil, fmt.Errorf("catalog: lay out the cached entity at %s: %w", slot, err)
	}
	return filepath.Join(dir, name), document, nil
}

// readCacheSlotBytes reads the entity bytes from the cache slot for uri at
// version. Tries the canonical filenames in order: library.yaml (libraries),
// blob (everything else).
//
// Each is read through the one read of a slot's files (oci.ReadCachedFile), which
// holds it to the root the cache's writes are held to; a file that leads through a
// link out of that root is not read, and the refusal is returned rather than
// passed over for the next name.
func readCacheSlotBytes(workspaceRoot, cacheDir, kind, uri, version string) ([]byte, error) {
	for _, name := range []string{"library.yaml", "blob"} {
		data, err := oci.ReadCachedFile(workspaceRoot, cacheDir, kind, uri, version, name)
		if err == nil {
			return data, nil
		}
		if errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
			return nil, err
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
	base, version, hasV := resolver.SplitVersionedURI(uri)
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
// segment stripped: the verbatim metadata.name the reference names
// (which may itself contain "/" separators — e.g., "core/server",
// "hub.aggrega.ai/component/core/api-server"). Stripping path segments
// here would lose load-bearing prefix content; only the version-pin
// suffix is stripped. Which version of that name a reference selects is
// not decided here but by selectEntityVersion.
func lookupNameFromURI(uri string) string {
	base, _, _ := resolver.SplitVersionedURI(uri)
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
	base, _, _ := resolver.SplitVersionedURI(uri)
	if idx := strings.LastIndex(base, "/"); idx >= 0 {
		return base[idx+1:]
	}
	return base
}
