// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog_test

import (
	"context"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// TestClosureLookup_TargetedUpgradeFloatsOnlyDropped is the mechanism proof for
// a targeted upgrade (`aggrega pull --upgrade <uri>`). A targeted upgrade drops
// ONLY the named entity from the prior-lockfile pin set and holds every other
// pin. This models that reduction directly against the real reuse-verifying
// lookup: two entities are pinned at 1.0.0; removing entity A's pin (the
// reduction the pull command performs for a named target) floats A to the
// registry's highest (1.1.0) while entity B, still pinned, is honored at 1.0.0.
// The honor path — the registry (Tier-3) — is the only tier where a pin governs
// version selection; local (Tier-1) catalog entities always float to the highest
// local version, so honor-vs-float is only observable here.
func TestClosureLookup_TargetedUpgradeFloatsOnlyDropped(t *testing.T) {
	const cacheDir = ".aggrega/cache"
	const uriA = "core/database"
	const uriB = "core/cache"
	v100 := twoVersionComponent("1.0.0")
	v110 := twoVersionComponent("1.1.0")
	puller := &twoVersionPuller{ev100: v100, ev110: v110, lv100: v100, lv110: v110}

	cat, err := catalog.LoadAll("", t.TempDir()) // empty catalog → Tier-3 puller
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	// The full prior-lockfile pin set: both entities pinned at 1.0.0.
	fullPins := map[string]catalog.Pin{
		uriA: {ResolvedVersion: "1.0.0", Digest: documentRowDigest(v100)},
		uriB: {ResolvedVersion: "1.0.0", Digest: documentRowDigest(v100)},
	}

	// Targeted upgrade of entity A == drop A from the pin set fed to the reuse
	// lookup (the reduction PinReuseFromLockfile performs for a named target);
	// B stays.
	reduced := map[string]catalog.Pin{uriB: fullPins[uriB]}
	reuse := &catalog.ReuseVerification{PriorPins: reduced}
	lookup := cat.ClosureLookupWithReuse(puller, t.TempDir(), cacheDir, nil, nil, reuse)

	resA, err := lookup.ResolveCatalogEntity(context.Background(), uriA, "component")
	if err != nil {
		t.Fatalf("resolve dropped target A: %v", err)
	}
	if resA.ResolvedVersion != "1.1.0" {
		t.Errorf("targeted entity A: resolved %q; want 1.1.0 (dropped pin floats to highest)", resA.ResolvedVersion)
	}

	resB, err := lookup.ResolveCatalogEntity(context.Background(), uriB, "component")
	if err != nil {
		t.Fatalf("resolve held entity B: %v", err)
	}
	if resB.ResolvedVersion != "1.0.0" {
		t.Errorf("held entity B: resolved %q; want 1.0.0 (pin still honored, not floated)", resB.ResolvedVersion)
	}
	if resB.Digest != documentRowDigest(v100) {
		t.Errorf("held entity B: digest %q; want the v1.0.0 digest", resB.Digest)
	}

	// Exact-form target: a reference carrying an exact version constraint
	// re-resolves to that literal even with no pin (dropped) — a no-op upgrade
	// that neither errors nor moves.
	resExact, err := lookup.ResolveCatalogEntity(context.Background(), uriA+":1.0.0", "component")
	if err != nil {
		t.Fatalf("resolve exact-form dropped target: %v", err)
	}
	if resExact.ResolvedVersion != "1.0.0" {
		t.Errorf("exact-form target: resolved %q; want 1.0.0 (exact constraint holds, no float)", resExact.ResolvedVersion)
	}
}
