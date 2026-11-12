// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package pluginfetchverify is the single home every plugin fetch routes through
// for verification: registry signature verification against the resolving
// registry's trust policy, and host-function stamp re-derivation from the
// content-verified .wasm bytes.
//
// It holds the verification and nothing else. It fetches nothing, caches
// nothing, and links no registry client: a caller resolves a plugin through
// whatever cascade its own tier uses, then hands the resulting identity — and,
// where it has them, the entity stamp and the .wasm bytes — to Verify.
//
// Which checks run, and whether they run at all, is this package's own
// responsibility rather than a caller's. Verify composes the signature pass and
// the host-function pass itself and both are declared here, so no caller supplies
// a check and none can supply one that does nothing. What a caller does supply is
// the registry-location factory the signature pass discovers referrers through,
// and the writer its notices go to — inputs to a check rather than the check.
//
// A fetch tier therefore either routes through Verify or carries no verification
// at all, and the second case is what the command package's source guard fails on.
//
// The shape that follows is deliberate: each fetch tier depends on this package
// and none of them on each other. A tier that wanted signature checking would
// otherwise have to reach through a neighbouring tier to get at it, which is a
// dependency on where the code sits rather than on what the tier needs.
package pluginfetchverify
