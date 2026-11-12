// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package sourcekind

import (
	"slices"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestServedAnswersTheDeclaredSetExactly holds Served to the four declared kinds
// and to nothing near them: the reserved kind no adapter ships for, a kind in
// another case, a pinned spelling and the empty string are not served.
func TestServedAnswersTheDeclaredSetExactly(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{HelmOCIChart, HelmHTTPSChart, ComposeHTTPSFragment, GitRepository} {
		if !Served(kind) {
			t.Errorf("Served(%q) = false; want true", kind)
		}
	}
	for _, kind := range []string{"terraform-module-oci", "Helm-OCI-Chart", "helm-oci-chart:1", "", "nosuch"} {
		if Served(kind) {
			t.Errorf("Served(%q) = true; want false", kind)
		}
	}
}

// TestServedKindsIsSortedAndACopy pins that the listing is sorted — the advice
// naming it is read by people, so it reads the same every run — and that a caller
// reordering it changes nothing Served answers.
func TestServedKindsIsSortedAndACopy(t *testing.T) {
	t.Parallel()
	got := ServedKinds()
	if !slices.IsSorted(got) || len(got) != 4 {
		t.Fatalf("ServedKinds() = %v; want the four served kinds, sorted", got)
	}
	got[0] = "tampered"
	if !Served(ComposeHTTPSFragment) || Served("tampered") {
		t.Errorf("reordering the listing changed what Served answers")
	}
}

// TestErrUnknownKindCarriesItsRegisteredIdentity pins the condition the document
// check raises: its short code is the one the published reference names, and it
// matches itself through the chain.
func TestErrUnknownKindCarriesItsRegisteredIdentity(t *testing.T) {
	t.Parallel()
	if code, ok := cerrors.ShortCodeOf(ErrUnknownKind()); !ok || code != "E0186" {
		t.Errorf("ShortCodeOf(ErrUnknownKind()) = %q, %v; want E0186", code, ok)
	}
	if code, ok := cerrors.CodeOf(ErrUnknownKind()); !ok || code != "unknown-source-kind" {
		t.Errorf("CodeOf(ErrUnknownKind()) = %q, %v; want unknown-source-kind", code, ok)
	}
}
