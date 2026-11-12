// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compiler_test

import (
	stderrors "errors"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
)

// The proofs here drive a real build of three Releases that differ in exactly
// one thing: where an attachment binding a FeatureRecipe that generates once for
// a boundary is declared.
//
// They exist as end-to-end proofs rather than as more rows beside the rule's own
// because the shape they measure is not readable from a hand-built world. Whether
// a component carrying such an attachment renders the same output as the
// boundary-only form is a property of the whole pipeline — the attachment
// cascade, the dedup key, the boundary-scope dispatch and the assembly — and a
// fixture that modelled any of those would be asserting its own model.

// boundaryScopeCatalogPaths are the catalog entries the three Releases share:
// the Feature and FeatureRecipe of a real boundary-scope attachment, plus the
// schema library its values schema references.
var boundaryScopeCatalogPaths = []string{
	"../../../aggrega-hub/aggrega-catalog/feature/core/compose-bridge/1.0.0/feature.yaml",
	"../../../aggrega-hub/aggrega-catalog/feature-recipe/compose/bridge",
	"../../../aggrega-hub/aggrega-catalog/schemalibrary/base-components/1.0.0/library.yaml",
}

// renderBoundaryScopeVariant builds one of the three Releases over the shared
// recipes and catalog, returning the rendered file set or the build's halt.
func renderBoundaryScopeVariant(t *testing.T, releaseFile string) (map[string][]byte, error) {
	t.Helper()
	res, err := tryRunFeatureMatrixVariant(t, featureMatrixVariant{
		name:              "compose-sourced-with-bridge",
		releaseFile:       releaseFile,
		extraCatalogPaths: boundaryScopeCatalogPaths,
	})
	if err != nil {
		return nil, err
	}
	return res.Files, nil
}

// differingRenderedPaths names every path the two file sets disagree on: present
// on one side only, or carrying different bytes.
//
// It names what differs rather than counting, because two sides short by one
// still agree on a count — and a byte-identity claim is only worth what its
// comparison can report.
func differingRenderedPaths(left, right map[string][]byte) []string {
	seen := map[string]bool{}
	for path := range left {
		seen[path] = true
	}
	for path := range right {
		seen[path] = true
	}
	paths := make([]string, 0, len(seen))
	for path := range seen {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	var differing []string
	for _, path := range paths {
		l, inLeft := left[path]
		r, inRight := right[path]
		if !inLeft || !inRight || string(l) != string(r) {
			differing = append(differing, path)
		}
	}
	return differing
}

// TestBoundaryScopeAttachment_ComponentCarriedRendersIdentically is the
// no-verdict-move proof for the shape the rule must not break.
//
// A component restating an attachment its enclosing scope already declares
// renders exactly what the scope-only form renders. That is what separates the
// rule the build now carries — refuse only where NO scope enclosing the component
// declares the attachment — from one refusing a component-carried attachment
// outright, which would reject a Release that builds.
//
// The comparison is shown to be capable of failing before it is trusted: the same
// two sets are compared again with one byte moved, and the proof fails if that
// comparison reports agreement.
func TestBoundaryScopeAttachment_ComponentCarriedRendersIdentically(t *testing.T) {
	t.Parallel()

	scopeOnly, err := renderBoundaryScopeVariant(t, "compose-sourced-with-bridge.yaml")
	if err != nil {
		t.Fatalf("the scope-only Release did not build: %v", joinErrorChain(err))
	}
	alsoOnComponent, err := renderBoundaryScopeVariant(t, "boundary-scope-attachment-inherited.yaml")
	if err != nil {
		t.Fatalf("a component restating an attachment its enclosing scope declares stopped the "+
			"build: %v", joinErrorChain(err))
	}

	if len(scopeOnly) == 0 {
		t.Fatal("the scope-only Release rendered no file; the comparison below would certify nothing")
	}
	if differing := differingRenderedPaths(scopeOnly, alsoOnComponent); len(differing) != 0 {
		t.Errorf("restating the attachment against a component moved %d rendered path(s): %v",
			len(differing), differing)
	}

	// The control on the comparison itself. One byte of one rendered document is
	// moved and the same comparison is run again; a comparison that reported
	// agreement here would have reported agreement above whatever the render did.
	planted := make(map[string][]byte, len(alsoOnComponent))
	var plantedIn string
	for path, content := range alsoOnComponent {
		if plantedIn == "" && len(content) > 0 {
			mutated := append([]byte(nil), content...)
			mutated[0] ^= 0x20
			planted[path] = mutated
			plantedIn = path
			continue
		}
		planted[path] = content
	}
	if plantedIn == "" {
		t.Fatal("no rendered document carried a byte to plant in; the comparison is unproven")
	}
	if differing := differingRenderedPaths(scopeOnly, planted); len(differing) == 0 {
		t.Fatalf("the comparison reported agreement with a byte moved in %q, so its agreement above "+
			"means nothing", plantedIn)
	}
}

// TestBoundaryScopeAttachment_NoDeclaringScopeHalts is the invocation proof at
// the build's own entry point: the Release that declares the attachment only
// against a component halts, and the halt names the fault rather than a
// consequence of it.
//
// The identity is asserted rather than "an error", because this Release
// already halted before the rule existed — on the render reaching for a boundary
// projection and finding none, which names a missing lookup rather than the
// authoring mistake. A proof accepting any error would keep passing if the rule
// were removed.
func TestBoundaryScopeAttachment_NoDeclaringScopeHalts(t *testing.T) {
	t.Parallel()

	files, err := renderBoundaryScopeVariant(t, "boundary-scope-attachment-orphan.yaml")
	if err == nil {
		t.Fatalf("a FeatureRecipe that generates once for a boundary, attached against a component "+
			"no scope declares it at, rendered %d file(s)", len(files))
	}
	if !stderrors.Is(err, renderer.ErrBoundaryScopeAttachmentNotDeclaredAtABoundary()) {
		t.Fatalf("the halt does not carry %v: %s",
			renderer.ErrBoundaryScopeAttachmentNotDeclaredAtABoundary(), joinErrorChain(err))
	}
	for _, want := range []string{"bridge", "cluster/services-net"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the halt does not name %q, so an author cannot act on it: %s",
				want, joinErrorChain(err))
		}
	}

	// The same Release reached ahead of every reading that would otherwise
	// report a consequence: the boundary projection lookup this configuration used
	// to fail at reports the missing projection, not the authoring fault, so a
	// halt still carrying that wording would mean the rule ran too late.
	if strings.Contains(err.Error(), "no BoundaryFeature attachment found") {
		t.Errorf("the halt still names the missing boundary projection, so the rule is reported "+
			"after the reading it replaces: %s", joinErrorChain(err))
	}
}
