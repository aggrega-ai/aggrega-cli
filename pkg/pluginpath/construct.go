// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginpath

// PinPosture names how a construction site treats digest pins, so the posture is
// legible at every call rather than inferred from which maps happen to be
// populated. It is a construction-time legibility axis, not a resolution switch:
// it governs only whether the pin maps are threaded into the Resolver, never how
// the cascade behaves. Resolve reads no posture — it treats any resulting
// populated Pins map identically.
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
	// per-URI pins synthesized from the cached manifest rather than from a
	// lockfile, so a guest cached at pull but never pinned still resolves with
	// its bytes digest-checked on read. Signature-bundle digests are a
	// lockfile-only fact and do not participate under this posture.
	PinPostureVerifyOnRead
)

// ResolutionContext is the caller-neutral set of tier SOURCES a Resolver draws
// from, independent of any pin posture. Each caller fills the fields its socket
// has from its own workspace handles; a zero field disables the corresponding
// tier or option exactly as it does on the Resolver. The value carries no
// lockfile, pipeline, or command type, so it stays package-neutral and every
// caller can construct it from its own context.
type ResolutionContext struct {
	// SidecarDir is the operator's on-disk plugin directory (Tier 0b), already
	// resolved to an absolute path. Empty disables the sidecar tier.
	SidecarDir string

	// ColocatedDirs maps a plugin's artifact reference to the catalog directory
	// holding its co-located .wasm (Tier 0c). nil or empty disables the tier.
	ColocatedDirs map[string]string

	// WorkspaceRoot and CacheDir address the workspace content cache (Tier 2).
	// Both empty disables the cache tier.
	WorkspaceRoot string
	CacheDir      string

	// Override is an explicit filesystem path that wins over the cascade
	// entirely (the linter's --plugin flag). Empty leaves the cascade intact.
	Override string

	// OverrideLog receives the loud notice when a sidecar artifact overrides an
	// embedded one of the same URI. nil discards the notice.
	OverrideLog func(string)

	// Puller performs the Tier-3 OCI pull. nil disables Tier 3 — the hermetic
	// build and linter paths, where a cache miss is a loud halt.
	Puller Puller
}

// PinMaps groups the already-derived per-URI pin maps a pinned construction site
// threads into a Resolver. Each map is keyed by the plugin URI the cascade
// resolves by. The caller derives these from its own pin source — the build
// reads them from the lockfile; the cache-not-pin read synthesizes them from the
// cached manifest — so this package stays unaware of where pins originate. Every
// field is optional: a nil or empty map disables the corresponding check exactly
// as it does on the Resolver.
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
		sidecarDir:    rc.SidecarDir,
		colocatedDirs: rc.ColocatedDirs,
		workspaceRoot: rc.WorkspaceRoot,
		cacheDir:      rc.CacheDir,
		override:      rc.Override,
		overrideLog:   rc.OverrideLog,
		puller:        rc.Puller,
	}
	switch posture {
	case PinPostureLockfile:
		r.pins = pins.Pins
		r.pinVersions = pins.PinVersions
		r.bundleDigests = pins.BundleDigests
	case PinPostureVerifyOnRead:
		r.pins = pins.Pins
		r.pinVersions = pins.PinVersions
	case PinPostureNone:
		// Pin-free base: no pins participate, whatever the caller passed.
	}
	return r
}
