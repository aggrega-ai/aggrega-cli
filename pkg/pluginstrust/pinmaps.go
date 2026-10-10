// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginstrust

import "github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"

// PinMaps projects every record into the per-plugin pin maps a resolution site
// threads into a plugin resolver, so a resolution can be checked against the
// recorded identity without any resolver learning that this file exists.
//
// It is the single home of the record-to-pin conversion, and it needs no lookup
// to perform one: a record is addressed by the plugin's identity coordinate, and
// a resolver's pin maps are keyed by the artifact reference a plugin resolves
// to — and those are the same string, because a Plugin entity's artifact
// reference IS its identity coordinate (types.Plugin.ArtifactReference returns
// types.Plugin.Coordinate, which derives `<pluginType>/<name>` through the single
// types.PluginCoordinate rule). So converting a record is re-keying it, with no
// entity read, no version derivation and no second copy of the address rule.
//
// That identity is invisible at this call site — nothing here mentions a Plugin
// entity — so it is asserted by a guard test rather than left to a reader to
// notice. Were the derivation ever to change, the conversion would silently pin
// nothing; the guard is what makes it fail loudly instead.
//
// Both digest maps carry the recorded digest. Pins is the digest the resolved
// bytes are verified against. PinVersions addresses the cache slot, whose second
// segment for a plugin artifact is that same content digest — the recorded version
// and the recorded digest name the same content for a `.wasm` — so the pinned slot
// and the recorded one are the same slot. BundleDigests carries, for a record
// whose Signature names a cached signature bundle, that bundle's digest, so the
// resolution that loads the plugin re-hashes the bundle before returning its bytes
// — the same re-hash the build's check of recorded signatures runs over the record
// before anything renders, on every surface that loads the plugin. A record
// carrying no signature contributes no bundle digest.
//
// A record whose coordinate is empty addresses no plugin and is skipped, for the
// same reason Upsert refuses to write one: every empty address would collide
// with every other.
//
// # That skip is the guard on the artifact-direct escape hatch
//
// A resolver handed an explicit artifact path short-circuits its tier cascade and
// checks the bytes it read against the pin held under the EMPTY reference, because
// the escape hatch names a path rather than a plugin identity. So a pin written
// under an empty key would govern an escape hatch whose entire purpose is to
// bypass the entity model — and it would do so silently, since a resolver
// consulting that key has nothing to refuse and no way to tell an intended pin
// from an accidental one.
//
// The skip above is therefore the guard, and this conversion is where the guard
// lives — not in the resolver. Nothing reaches it today: Upsert refuses to write a
// record with an empty coordinate, and the skip here refuses to convert one, both
// held by their own tests. A second source of these records that omitted the skip
// would pin the hatch, so a maintainer removing it as a redundant check is the
// case this paragraph exists to stop.
//
// A nil receiver holds no records and yields empty maps, so a caller that has
// loaded no file need not branch before asking — the same contract Find offers.
func (t *Trust) PinMaps() pluginpath.PinMaps {
	pins := make(map[string]string)
	versions := make(map[string]string)
	bundles := make(map[string]string)
	if t != nil {
		for _, p := range t.Plugins {
			if p.Plugin == "" {
				continue
			}
			pins[p.Plugin] = p.Digest
			versions[p.Plugin] = p.Digest
			if p.Signature != nil && p.Signature.BundleDigest != "" {
				bundles[p.Plugin] = p.Signature.BundleDigest
			}
		}
	}
	return pluginpath.PinMaps{Pins: pins, PinVersions: versions, BundleDigests: bundles}
}
