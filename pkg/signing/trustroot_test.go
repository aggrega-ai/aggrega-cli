// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package signing_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// TestTrustedMaterialFromRootPathLoadsPinnedRoot proves a pinned Sigstore
// trust-root bundle loads from a file with no network — the air-gapped
// anchor seam. The fixture is the public-good trust root; loading it
// offline returns usable trust material.
func TestTrustedMaterialFromRootPathLoadsPinnedRoot(t *testing.T) {
	t.Parallel()
	tm, err := signing.TrustedMaterialFromRootPath(filepath.Join("testdata", "public-good-root.json"))
	if err != nil {
		t.Fatalf("TrustedMaterialFromRootPath: %v", err)
	}
	if tm == nil {
		t.Fatal("loaded trust material is nil")
	}
	// The loaded anchor carries the public-good Fulcio authorities, so it
	// is a usable keyless trust anchor (the verification logic against an
	// anchor is proven in the virtual-Sigstore keyless tests).
	if len(tm.FulcioCertificateAuthorities()) == 0 {
		t.Error("pinned root carries no Fulcio certificate authorities")
	}
}

// TestTrustedMaterialFromRootPathRejectsMissingFile proves a missing or
// unreadable pinned root surfaces ErrTrustRootLoad rather than silently
// falling back to a network fetch.
func TestTrustedMaterialFromRootPathRejectsMissingFile(t *testing.T) {
	t.Parallel()
	_, err := signing.TrustedMaterialFromRootPath(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if !errors.Is(err, signing.ErrTrustRootLoad) {
		t.Fatalf("want ErrTrustRootLoad, got %v", err)
	}
}

// TestTrustedMaterialFromRootPathRejectsGarbage proves a malformed root
// bundle is rejected with ErrTrustRootLoad.
func TestTrustedMaterialFromRootPathRejectsGarbage(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "garbage.json")
	if err := os.WriteFile(p, []byte("{not a trust root}"), 0o644); err != nil {
		t.Fatalf("write garbage: %v", err)
	}
	_, err := signing.TrustedMaterialFromRootPath(p)
	if !errors.Is(err, signing.ErrTrustRootLoad) {
		t.Fatalf("want ErrTrustRootLoad, got %v", err)
	}
}
