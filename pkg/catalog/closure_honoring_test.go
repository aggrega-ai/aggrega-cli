// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// sha256Hex is intentionally an INDEPENDENT re-derivation of the content
// digest, NOT a call to pkg/digest.ContentDigest. The pin-honoring and
// reuse/drift tests seed a PriorPin with this digest, then production recomputes
// via ContentDigest and compares to honor (or detect drift against) the pin.
// Keeping the test computation independent makes that a genuine cross-check: a
// format change in ContentDigest makes production disagree with the seeded pin
// and the test fails. Delegating to the shared helper would move both together
// and blind the check. The fixed-input literal-format guard lives in
// pkg/digest's own test and the fixed-body assertions in closure_lookup_test.go.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// twoVersionPuller models the real OCI puller's version semantics: a non-empty
// floor is fetched verbatim (exact), an empty floor lists tags and floats to the
// highest (here 1.1.0). It serves two versions of one entity so the honor-vs-float
// distinction is observable.
type twoVersionPuller struct{ v100, v110 []byte }

func (p *twoVersionPuller) Pull(context.Context, string, string) (*oci.PullResult, error) {
	return nil, oci.ErrFetchNotFound() // legacy path unused
}

func (p *twoVersionPuller) PullEntity(_ context.Context, _, _, floorVersion string) (*oci.PullResult, string, error) {
	return p.byFloor(floorVersion)
}

// PullLibrary mirrors PullEntity's floor semantics so the same two-version
// fixture exercises a sibling (library) resolver's honor-vs-float distinction.
func (p *twoVersionPuller) PullLibrary(_ context.Context, _, _, floorVersion string) (*oci.PullResult, string, error) {
	return p.byFloor(floorVersion)
}

func (p *twoVersionPuller) byFloor(floorVersion string) (*oci.PullResult, string, error) {
	switch floorVersion {
	case "1.0.0":
		return &oci.PullResult{Bytes: p.v100}, "1.0.0", nil
	case "1.1.0":
		return &oci.PullResult{Bytes: p.v110}, "1.1.0", nil
	case "": // bare reference → list-tags → highest
		return &oci.PullResult{Bytes: p.v110}, "1.1.0", nil
	}
	return nil, "", oci.ErrFetchNotFound()
}

// TestClosureLookup_HonorsPinnedVersion is the load-bearing proof of the
// honoring default: a prior pin makes the pull resolve the pin's EXACT version
// even when the registry serves a higher one; an un-pinned reference floats; and
// a pin whose registry bytes no longer match HALTS (honor != launder).
func TestClosureLookup_HonorsPinnedVersion(t *testing.T) {
	const cacheDir = ".aggrega/cache"
	const uri = "test/two-ver"
	v100 := []byte("component-v1.0.0-bytes")
	v110 := []byte("component-v1.1.0-bytes")
	puller := &twoVersionPuller{v100: v100, v110: v110}

	cat, err := catalog.LoadAll("", t.TempDir()) // empty catalog → Tier-1/Tier-2 miss, Tier-3 puller
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	// 1. HONORED — a pin @1.0.0 resolves 1.0.0 though the registry also has 1.1.0.
	reuse := &catalog.ReuseVerification{PriorPins: map[string]catalog.Pin{uri: {ResolvedVersion: "1.0.0", Digest: sha256Hex(v100)}}}
	res, err := cat.ClosureLookupWithReuse(puller, t.TempDir(), cacheDir, nil, nil, reuse).ResolveCatalogEntity(uri, "component")
	if err != nil {
		t.Fatalf("honored resolve: %v", err)
	}
	if res.ResolvedVersion != "1.0.0" {
		t.Errorf("honored: resolved %q; want 1.0.0 (the pin honored, NOT floated to 1.1.0)", res.ResolvedVersion)
	}
	if res.Digest != sha256Hex(v100) {
		t.Errorf("honored: digest %q; want the v1.0.0 digest", res.Digest)
	}

	// 2. FLOAT (contrast) — no pin → floats to the highest, 1.1.0.
	resF, err := cat.ClosureLookupWithReuse(puller, t.TempDir(), cacheDir, nil, nil, nil).ResolveCatalogEntity(uri, "component")
	if err != nil {
		t.Fatalf("un-pinned resolve: %v", err)
	}
	if resF.ResolvedVersion != "1.1.0" {
		t.Errorf("un-pinned: resolved %q; want 1.1.0 (float to highest)", resF.ResolvedVersion)
	}

	// 3. HALT (honor != launder) — a pin whose pinned-version bytes no longer match
	// the pinned digest halts with ErrFetchIntegrity rather than re-pinning.
	drift := &catalog.ReuseVerification{PriorPins: map[string]catalog.Pin{uri: {ResolvedVersion: "1.0.0", Digest: sha256Hex([]byte("STALE-PINNED-BYTES"))}}}
	_, err = cat.ClosureLookupWithReuse(puller, t.TempDir(), cacheDir, nil, nil, drift).ResolveCatalogEntity(uri, "component")
	if !errors.Is(err, oci.ErrFetchIntegrity()) {
		t.Errorf("drifted pin: err %v; want ErrFetchIntegrity (no launder/re-pin)", err)
	}

	// 4. SIBLING — the same honoring flip governs the library resolver: a pinned
	// SchemaLibrary resolves the pin's exact version though a higher one is served.
	const libURI = "test/two-ver-lib"
	libReuse := &catalog.ReuseVerification{PriorPins: map[string]catalog.Pin{libURI: {ResolvedVersion: "1.0.0", Digest: sha256Hex(v100)}}}
	libRes, err := cat.ClosureLookupWithReuse(puller, t.TempDir(), cacheDir, nil, nil, libReuse).ResolveLibrary(closure.LibraryAxisNamed("SchemaLibraries"), libURI)
	if err != nil {
		t.Fatalf("honored library resolve: %v", err)
	}
	if libRes.Resolution.ResolvedVersion != "1.0.0" {
		t.Errorf("honored library: resolved %q; want 1.0.0 (the pin honored, NOT floated to 1.1.0)", libRes.Resolution.ResolvedVersion)
	}
}
