// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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
// reuse/drift tests seed a PriorPin with it — directly for a library, whose row
// records its bytes, and through documentRowDigest for an entity — then
// production recomputes the row and compares to honor (or detect drift against)
// the pin. Keeping the test computation independent makes that a genuine
// cross-check: a format change in the production digest makes production
// disagree with the seeded pin and the test fails. Delegating to the shared
// helper would move both together and blind the check. The fixed-input
// literal-format guard lives in pkg/digest's own test and the fixed-body
// assertions in closure_lookup_test.go.
func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// documentRowDigest is, independently of production for the reason sha256Hex
// gives, the digest an entity row records for an entity document published
// alone: the SHA-256 of its one-entry listing — the entry's mode (644, the one
// mode every entry records), the SHA-256 of its bytes and its length-prefixed path,
// under the name a consumer lays a lone document out as.
func documentRowDigest(document []byte) string {
	sum := sha256.Sum256(document)
	return sha256Hex([]byte("644 " + hex.EncodeToString(sum[:]) + " 11:entity.yaml\n"))
}

// twoVersionComponent is a valid Component document at the given version — the
// body a real registry serves for the entity axis. The description tells the two
// versions' bytes apart so their digests differ, which is what the honor/drift
// assertions turn on. It is a real entity document because the registry tier now
// puts a fetched body through its kind's schema before caching it.
func twoVersionComponent(version string) []byte {
	return []byte(`apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: test/two-ver
  version: "` + version + `"
  description: two-ver at ` + version + `
  labels:
    component.aggrega.ai/type: server
spec: {}
`)
}

// twoVersionSchemaLibrary is the library-axis counterpart: a valid SchemaLibrary
// at the given version, since the library tier validates a fetched body against
// the SchemaLibrary schema too — whose library name is PascalCase.
func twoVersionSchemaLibrary(version string) []byte {
	return []byte(`apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: TwoVerLib
  version: "` + version + `"
spec:
  schemas:
    Storage:
      type: object
`)
}

// twoVersionPuller models the real OCI puller's version semantics: a non-empty
// floor is fetched verbatim (exact), an empty floor lists tags and floats to the
// highest (here 1.1.0). It serves two versions on each axis so the honor-vs-float
// distinction is observable, and each axis serves the kind that axis validates.
type twoVersionPuller struct{ ev100, ev110, lv100, lv110 []byte }

func (p *twoVersionPuller) Pull(context.Context, string, string) (*oci.PullResult, error) {
	return nil, oci.ErrFetchNotFound() // legacy path unused
}

func (p *twoVersionPuller) PullEntity(_ context.Context, _, _, floorVersion string) (*oci.PullResult, string, error) {
	return byFloor(floorVersion, p.ev100, p.ev110, oci.MediaTypeComponent)
}

// PullLibrary mirrors PullEntity's floor semantics so the same fixture exercises a
// sibling (library) resolver's honor-vs-float distinction, with library bodies.
func (p *twoVersionPuller) PullLibrary(_ context.Context, _, _, floorVersion string) (*oci.PullResult, string, error) {
	mt, _ := oci.MediaTypeFor("SchemaLibrary", false)
	return byFloor(floorVersion, p.lv100, p.lv110, mt)
}

func byFloor(floorVersion string, v100, v110 []byte, mt string) (*oci.PullResult, string, error) {
	switch floorVersion {
	case "1.0.0":
		return &oci.PullResult{Bytes: v100, MediaType: mt}, "1.0.0", nil
	case "1.1.0":
		return &oci.PullResult{Bytes: v110, MediaType: mt}, "1.1.0", nil
	case "": // bare reference → list-tags → highest
		return &oci.PullResult{Bytes: v110, MediaType: mt}, "1.1.0", nil
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
	v100 := twoVersionComponent("1.0.0")
	v110 := twoVersionComponent("1.1.0")
	lib100 := twoVersionSchemaLibrary("1.0.0")
	lib110 := twoVersionSchemaLibrary("1.1.0")
	puller := &twoVersionPuller{ev100: v100, ev110: v110, lv100: lib100, lv110: lib110}

	cat, err := catalog.LoadAll("", t.TempDir()) // empty catalog → Tier-1/Tier-2 miss, Tier-3 puller
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}

	// 1. HONORED — a pin @1.0.0 resolves 1.0.0 though the registry also has 1.1.0.
	reuse := &catalog.ReuseVerification{PriorPins: catalog.Pins{{Kind: "Component", URI: uri}: {ResolvedVersion: "1.0.0", Digest: documentRowDigest(v100)}}}
	res, err := cat.ClosureLookupWithReuse(puller, t.TempDir(), cacheDir, nil, nil, reuse).ResolveCatalogEntity(context.Background(), uri, "component")
	if err != nil {
		t.Fatalf("honored resolve: %v", err)
	}
	if res.ResolvedVersion != "1.0.0" {
		t.Errorf("honored: resolved %q; want 1.0.0 (the pin honored, NOT floated to 1.1.0)", res.ResolvedVersion)
	}
	if res.Digest != documentRowDigest(v100) {
		t.Errorf("honored: digest %q; want the v1.0.0 digest", res.Digest)
	}

	// 2. FLOAT (contrast) — no pin → floats to the highest, 1.1.0.
	resF, err := cat.ClosureLookupWithReuse(puller, t.TempDir(), cacheDir, nil, nil, nil).ResolveCatalogEntity(context.Background(), uri, "component")
	if err != nil {
		t.Fatalf("un-pinned resolve: %v", err)
	}
	if resF.ResolvedVersion != "1.1.0" {
		t.Errorf("un-pinned: resolved %q; want 1.1.0 (float to highest)", resF.ResolvedVersion)
	}

	// 3. HALT (honor != launder) — a pin whose pinned-version bytes no longer match
	// the pinned digest halts with ErrFetchIntegrity rather than re-pinning.
	drift := &catalog.ReuseVerification{PriorPins: catalog.Pins{{Kind: "Component", URI: uri}: {ResolvedVersion: "1.0.0", Digest: documentRowDigest([]byte("STALE-PINNED-BYTES"))}}}
	_, err = cat.ClosureLookupWithReuse(puller, t.TempDir(), cacheDir, nil, nil, drift).ResolveCatalogEntity(context.Background(), uri, "component")
	if !errors.Is(err, oci.ErrFetchIntegrity()) {
		t.Errorf("drifted pin: err %v; want ErrFetchIntegrity (no launder/re-pin)", err)
	}

	// 4. SIBLING — the same honoring flip governs the library resolver: a pinned
	// SchemaLibrary resolves the pin's exact version though a higher one is served.
	const libURI = "test/two-ver-lib"
	libReuse := &catalog.ReuseVerification{PriorPins: catalog.Pins{{Kind: "SchemaLibrary", URI: libURI}: {ResolvedVersion: "1.0.0", Digest: sha256Hex(lib100)}}}
	libRes, err := cat.ClosureLookupWithReuse(puller, t.TempDir(), cacheDir, nil, nil, libReuse).ResolveLibrary(context.Background(), closure.LibraryAxisNamed("SchemaLibraries"), libURI)
	if err != nil {
		t.Fatalf("honored library resolve: %v", err)
	}
	if libRes.Resolution.ResolvedVersion != "1.0.0" {
		t.Errorf("honored library: resolved %q; want 1.0.0 (the pin honored, NOT floated to 1.1.0)", libRes.Resolution.ResolvedVersion)
	}
}
