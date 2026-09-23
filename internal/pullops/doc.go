// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package pullops is the shared dependency-resolution layer that front ends
// drive to resolve a Release's catalog and source dependencies into the cache
// and write the per-Release lockfile. It single-sources the operation so every
// front end runs one implementation rather than its own copy.
//
// The layer composes everything the resolution needs: the artifact-store client
// that fetches from a registry, the network-egress gate every fetch crosses, and
// the trust surface that verifies a fetched artifact's signature, re-derives its
// host-function stamp, discovers a withdrawal marker attached to it and holds
// each verdict attachment to the plugin identity recorded as trusted for it.
// Those belong to the operation and travel with it, because a surface that
// resolved dependencies without them would be resolving something weaker under
// the same name.
//
// # It runs only when it is asked, on every surface
//
// Resolving dependencies reaches registries and writes: it fills the per-Release
// lockfile, populates the artifact cache and, where asked to pin, records pins
// beside each Recipe and FeatureRecipe it builds from a catalog directory. Every
// one of those is intended, and none may happen because somebody was typing.
//
// The rule is a property of the invocation path rather than of any one caller: a
// registry-reaching or state-writing operation runs on EXPLICIT INVOCATION and
// never as a side effect of validating, inspecting or answering a question about
// a workspace. It is stated here once, over the whole layer, rather than per
// operation, because an enumeration of the operations it covers is what goes
// stale. What makes a write acceptable is that somebody asked for it, not which
// surface asked — a caller invoking this has asked, exactly as a caller invoking
// a build has asked for artifacts.
//
// # It holds no terminal, and never prompts
//
// A caller's progress surface and its report of what was resolved arrive as
// optional callbacks on Reporter, so a surface with no terminal supplies none of
// them and the layer stays silent rather than writing to a stream nobody reads.
//
// The same rule governs the one fork this operation cannot settle from its
// inputs. Where a person would have been asked to choose, a caller with nobody
// watching supplies no prompt and the fork HALTS, loudly, naming what to supply —
// it never blocks waiting for an answer that will never come. Identity is
// resolved the same way: from a supplied or ambient credential source, never by
// asking. A caller that cannot answer a question is one this layer must not ask,
// and a hang is the observable failure that posture exists to prevent.
//
// # It holds no provider-specific or cloud-specific logic
//
// It orchestrates, it fetches and it writes files; the domain intelligence stays
// in the plugin guests.
package pullops
