// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginpath

// PinPosture names how a construction site treats digest pins, so the posture is
// legible at every call rather than inferred from which maps happen to be
// populated. It is a construction-time legibility axis, not a resolution switch:
// it governs whether the pin maps are threaded into the Resolver, never how the
// cascade behaves. Resolve reads no posture — it treats any resulting populated
// Pins map identically, and the pin comparison, the cascade order and the
// resolved bytes are the same under every posture.
//
// It carries one further meaning, and only for a resolution that finds no pin at
// all: the posture is this package's record of whether a recorded digest was
// meant to govern what the site resolves. A pinned posture that meets a reference
// with no pin has found a gap worth telling the reader about; the pin-free base
// has found the state it declared. That reading changes what is SAID and never
// what is resolved.
type PinPosture int

const (
	// PinPostureNone is the pin-free base: no pins participate in resolution.
	// The publish-time linter carries it — it resolves a not-yet-pinned proposed
	// entity, so there is nothing to verify against. Under this posture the pin
	// maps are left unset regardless of what the caller supplies, so a pin-free
	// site cannot carry a pin by accident.
	PinPostureNone PinPosture = iota

	// PinPostureLockfile is the pinned cascade: the caller supplies per-URI pins
	// derived from the lockfile, and every tier's resolved bytes verify against
	// them. The build and the CLI-install path carry it. An empty pin set is a
	// valid pinned posture — a build whose lockfile records no plugins pins
	// nothing yet.
	PinPostureLockfile

	// PinPostureVerifyOnRead is the cache-not-pin read: the caller supplies
	// per-URI pins from a source other than the lockfile — synthesized from the
	// cached manifest, or taken from a record of the identities trusted for the
	// gates that return an accept-or-reject verdict — so a guest cached at pull
	// but never pinned still resolves with its bytes digest-checked on read.
	// Signature-bundle digests are a lockfile-only fact and do not participate
	// under this posture.
	PinPostureVerifyOnRead
)

// ResolutionContext is the caller-neutral set of tier SOURCES a Resolver draws
// from, independent of any pin posture. Each caller fills the fields its socket
// has from its own workspace handles; a zero field disables the corresponding
// tier or option exactly as it does on the Resolver. The value carries no
// lockfile, pipeline, or command type, so it stays package-neutral and every
// caller can construct it from its own context.
type ResolutionContext struct {
	// ColocatedDirs maps a plugin's artifact reference to where its co-located
	// .wasm is read (Tier 0b): the directory its Plugin entity was authored in, and
	// the root a read there is held to. nil or empty disables the tier.
	ColocatedDirs map[string]ColocatedDir

	// WorkspaceRoot and CacheDir address the workspace content cache (Tier 2).
	// Both empty disables the cache tier.
	WorkspaceRoot string
	CacheDir      string

	// Override is an explicit filesystem path that wins over the cascade
	// entirely (the linter's --plugin flag). Empty leaves the cascade intact.
	Override string
}

// ColocatedDir is where the co-located tier reads a plugin's .wasm, beside its
// Plugin entity in a catalog directory.
//
// Dir is the directory the entity's document was authored in, which the .wasm sits
// in under the filename [PluginLeaf] derives from the reference. Root is the root a
// read there is held to: the one the catalog directory holding that document picks
// (workspaceconfig.CatalogDirRoot) — the root the catalog's own walk read the
// document under — so a .wasm that is a link leading out of it is refused rather
// than read. The root comes from whoever found the entity, because only the walk
// that found it knows which catalog directory it was found in; the tier never
// decides one. An empty Root holds nothing.
type ColocatedDir struct {
	Dir  string
	Root string
}

// PinMaps groups the already-derived per-URI pin maps a pinned construction site
// threads into a Resolver. Each map is keyed by the plugin URI the cascade
// resolves by. The caller derives these from its own pin source — the build reads
// them from the lockfile; the cache-not-pin read synthesizes them from the cached
// manifest; a gate returning an accept-or-reject verdict takes them from the
// record of the identities trusted for such gates — so this package stays unaware
// of where pins originate. The list is illustrative rather than closed, which is
// the whole point of the type: a new pin source needs no change here. Every field
// is optional: a nil or empty map disables the corresponding check exactly as it
// does on the Resolver.
type PinMaps struct {
	// Pins maps a plugin URI to its digest pin; resolved bytes verify against it
	// at every tier.
	Pins map[string]string

	// PinVersions maps a plugin URI to its resolved version, addressing the
	// versioned cache slot.
	PinVersions map[string]string

	// BundleDigests maps a plugin URI to the recorded digest of its cached
	// signature-bundle blob, re-hashed and compared before the resolved bytes
	// are returned. Meaningful only under the lockfile posture.
	BundleDigests map[string]string
}

// PinSource is a construction site's written answer to where its plugin pins
// come from. A surface that drives the compiler states one, so the answer is read
// off the site rather than inferred from which workspace handles that site happens
// to hold.
//
// It exists because the two facts it separates used to be one value. A site whose
// pin source recorded nothing and a site with no pin source at all both produced
// an empty PinMaps under the pinned posture — a construction asserting that a
// recorded identity governs what it resolves while holding nothing to address the
// versioned cache slot with. Those are different facts, and only one of them is a
// state a caller meant. Threading a source makes the second unrepresentable: a
// site states where its pins come from, and a site that states nothing is refused
// where it enters the compiler rather than read as the first.
//
// The value names a SOURCE and never a posture. The posture follows from the
// source, which is what leaves a caller that derives its posture from its own pin
// set free to go on doing so by calling [NewResolver] directly — the publish-time
// linter does exactly that, because naming a pinned posture over an empty set
// would read as a pin nobody has.
//
// The zero value is deliberately not a usable source. A source is obtained from
// [NoPins] or [RecordedPins], so "there are none" is something a caller writes
// rather than something an omission produces.
type PinSource struct {
	posture PinPosture
	pins    PinMaps
}

// NoPins is the written answer "no recorded identity governs what this site
// resolves". It carries the pin-free base posture, under which a reference that
// resolves without a pin has found the state the site declared rather than a gap,
// so no unrecorded-identity advisory is raised for it.
//
// It is the honest source for a surface that has no record to read: one that is
// producing the record rather than consuming it, and a harness that synthesizes
// its documents in memory and runs against no workspace at all.
func NoPins() *PinSource {
	return &PinSource{posture: PinPostureNone}
}

// RecordedPins is the written answer "the identities recorded for this workspace
// govern what this site resolves", carrying the maps already derived from that
// record. It carries the pinned posture, under which every tier's resolved bytes
// verify against the supplied digests and a reference resolving without one is
// worth telling the reader about.
//
// An empty PinMaps is a valid argument and keeps its full meaning: a record that
// pins no plugin yet still declares that pins govern here. That is precisely the
// fact [NoPins] does not state, and keeping the two spellings apart is the whole
// purpose of the type.
func RecordedPins(pins PinMaps) *PinSource {
	return &PinSource{posture: PinPostureLockfile, pins: pins}
}

// Resolver constructs the Resolver this pin source describes, over the tier
// sources the caller supplies.
//
// It is the only way to turn a source into a Resolver, so the posture and the
// maps cannot be drawn from different sources and recombined. It routes through
// [NewResolver], which stays the single construction entry point.
func (s *PinSource) Resolver(rc ResolutionContext) Resolver {
	return NewResolver(rc, s.posture, s.pins)
}

// NewResolver is the single construction entry point for a Resolver. It composes
// the caller-neutral tier sources with an explicitly-named pin posture and the
// already-derived pin maps, so no site hand-populates the struct and the
// field-population logic lives exactly once.
//
// The posture governs whether the pin maps are threaded, not how the cascade
// resolves: under PinPostureNone the maps are ignored so a pin-free site cannot
// carry a pin by accident; under PinPostureLockfile all three maps participate;
// under PinPostureVerifyOnRead the pin and version maps participate while the
// signature-bundle digests — a lockfile-only fact — do not. Resolve reads no
// posture and treats the resulting populated (or empty) Pins map identically.
func NewResolver(rc ResolutionContext, posture PinPosture, pins PinMaps) Resolver {
	r := Resolver{
		colocatedDirs: rc.ColocatedDirs,
		workspaceRoot: rc.WorkspaceRoot,
		cacheDir:      rc.CacheDir,
		override:      rc.Override,
	}
	switch posture {
	case PinPostureLockfile:
		r.pins = pins.Pins
		r.pinVersions = pins.PinVersions
		r.bundleDigests = pins.BundleDigests
		r.expectsRecordedIdentity = true
	case PinPostureVerifyOnRead:
		r.pins = pins.Pins
		r.pinVersions = pins.PinVersions
		r.expectsRecordedIdentity = true
	case PinPostureNone:
		// Pin-free base: no pins participate, whatever the caller passed, and a
		// reference this site resolves without one has not lost anything — so it
		// raises no unrecorded-identity advisory either.
	}
	return r
}
