// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package closure_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The Release below attaches a Feature at all four positions a boundary tree
// offers, so one fixture covers every attachment site the walk must reach:
//
//   - per-component in a top-level boundary  (control: reached by the walk already)
//   - at the boundary itself, top-level
//   - per-component inside a nested boundary
//   - at a nested boundary itself
//
// Every type URI is distinct and none of them appears as a Manifest
// features-mapping key. That is deliberate: the Manifest mapping key is itself
// recorded as an entity, so a type string that collided with one would be pinned
// through the Manifest axis whatever the boundary walk did, and the assertion
// would prove nothing about the walk.
const boundaryAttachmentRelease = `
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: prod
  environment: prod
  version: 1.0.0
spec:
  aggregate: hub.aggrega.ai/aggregate/example/app:1.0.0
  manifest: hub.aggrega.ai/manifest/example/app:1.0.0
  boundaries:
    cluster:
      features:
        audit:
          type: hub.aggrega.ai/feature/core/audit:1.0.0
      components:
        api:
          features:
            ingress:
              type: hub.aggrega.ai/feature/core/ingress:1.0.0
      boundaries:
        team-a:
          features:
            quota:
              type: hub.aggrega.ai/feature/core/quota:1.0.0
          components:
            worker:
              features:
                mesh:
                  type: hub.aggrega.ai/feature/core/mesh:1.0.0
`

// boundaryAttachmentLookup resolves every Feature the fixture attaches, so an
// entity missing from the closure means the walk never collected it rather than
// that the lookup failed to find it.
func boundaryAttachmentLookup() *fakeLookup {
	return &fakeLookup{
		entities: map[string]closure.Resolution{
			"hub.aggrega.ai/aggregate/example/app:1.0.0": resolutionOK("1.0.0", "sha256:aaa", "tier3-registry"),
			"hub.aggrega.ai/manifest/example/app:1.0.0":  resolutionOK("1.0.0", "sha256:bbb", "tier3-registry"),
			"hub.aggrega.ai/feature/core/ingress:1.0.0":  resolutionOK("1.0.0", "sha256:c01", "tier3-registry"),
			"hub.aggrega.ai/feature/core/audit:1.0.0":    resolutionOK("1.0.0", "sha256:c02", "tier3-registry"),
			"hub.aggrega.ai/feature/core/quota:1.0.0":    resolutionOK("1.0.0", "sha256:c03", "tier3-registry"),
			"hub.aggrega.ai/feature/core/mesh:1.0.0":     resolutionOK("1.0.0", "sha256:c04", "tier3-registry"),
		},
		plugins:     map[string]closure.Resolution{},
		schemaLibs:  map[string]closure.LibraryResolution{},
		mappingLibs: map[string]closure.LibraryResolution{},
	}
}

// TestBoundaryFeatureAttachmentsAreAllPinned asserts that a Feature attached
// anywhere in a Release's boundary tree lands in the pinned entity set.
//
// The pinned entity set is what the lockfile's entity rows are built from: the
// pull path appends one row per closure entity with no filtering, so an entity the
// walk does not collect has no row, and a later build resolves that Feature afresh
// instead of against a pin.
//
// Each position is its own subtest so a gap names the position that has it.
func TestBoundaryFeatureAttachmentsAreAllPinned(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		uri  string
	}{
		{"per-component in a top-level boundary", "hub.aggrega.ai/feature/core/ingress:1.0.0"},
		{"on a top-level boundary itself", "hub.aggrega.ai/feature/core/audit:1.0.0"},
		{"on a nested boundary itself", "hub.aggrega.ai/feature/core/quota:1.0.0"},
		{"per-component inside a nested boundary", "hub.aggrega.ai/feature/core/mesh:1.0.0"},
	}

	rel := loadRelease(t, boundaryAttachmentRelease)
	c, err := closure.ComputeClosure(rel, nil, types.NormalizedAggregate{}, workspaceconfig.Default(), boundaryAttachmentLookup())
	if err != nil {
		t.Fatalf("ComputeClosure: %v", err)
	}

	pinned := map[string]bool{}
	for _, e := range c.Entities {
		pinned[e.URI] = true
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !pinned[tc.uri] {
				t.Errorf("Feature %q attached %s is not pinned.\n  pinned set: %v", tc.uri, tc.name, pinnedURIs(c))
			}
		})
	}
}

// TestBoundaryFeatureReferencePathsRecordTheAttachmentSite asserts each pinned
// Feature records the authoring path it was attached at, so a reference in the
// lockfile stays attributable to the position in the Release that produced it.
func TestBoundaryFeatureReferencePathsRecordTheAttachmentSite(t *testing.T) {
	t.Parallel()

	rel := loadRelease(t, boundaryAttachmentRelease)
	c, err := closure.ComputeClosure(rel, nil, types.NormalizedAggregate{}, workspaceconfig.Default(), boundaryAttachmentLookup())
	if err != nil {
		t.Fatalf("ComputeClosure: %v", err)
	}

	want := map[string]string{
		"hub.aggrega.ai/feature/core/ingress:1.0.0": "release.spec.boundaries.cluster.components.api.features.ingress.type",
		"hub.aggrega.ai/feature/core/audit:1.0.0":   "release.spec.boundaries.cluster.features.audit.type",
		"hub.aggrega.ai/feature/core/quota:1.0.0":   "release.spec.boundaries.cluster.boundaries.team-a.features.quota.type",
		"hub.aggrega.ai/feature/core/mesh:1.0.0":    "release.spec.boundaries.cluster.boundaries.team-a.components.worker.features.mesh.type",
	}

	got := map[string][]string{}
	for _, e := range c.Entities {
		got[e.URI] = e.ReferencedFrom
	}

	// Presence is asserted per URI rather than by filtering the closure: iterating
	// only what the closure happens to hold would let an uncollected Feature pass
	// this test by never being examined.
	for uri, wantPath := range want {
		paths, pinned := got[uri]
		if !pinned {
			t.Errorf("entity %q is not pinned, so its reference path cannot be checked", uri)
			continue
		}
		if len(paths) != 1 || paths[0] != wantPath {
			t.Errorf("entity %q reference paths:\n  got:  %v\n  want: [%s]", uri, paths, wantPath)
		}
	}
}

// TestNestedBoundaryFeatureKindHintIsRecorded asserts a Feature reached through a
// nested boundary carries the same catalog kind as one reached at the top level, so
// a bare-identity pull binds it to the feature registry location either way.
func TestNestedBoundaryFeatureKindHintIsRecorded(t *testing.T) {
	t.Parallel()

	rel := loadRelease(t, boundaryAttachmentRelease)
	c, err := closure.ComputeClosure(rel, nil, types.NormalizedAggregate{}, workspaceconfig.Default(), boundaryAttachmentLookup())
	if err != nil {
		t.Fatalf("ComputeClosure: %v", err)
	}

	hints := map[string]string{}
	pinned := map[string]bool{}
	for _, e := range c.Entities {
		hints[e.URI] = e.KindHint
		pinned[e.URI] = true
	}

	for _, uri := range []string{
		"hub.aggrega.ai/feature/core/audit:1.0.0",
		"hub.aggrega.ai/feature/core/quota:1.0.0",
		"hub.aggrega.ai/feature/core/mesh:1.0.0",
	} {
		if !pinned[uri] {
			t.Errorf("entity %q is not pinned, so its catalog kind cannot be checked", uri)
			continue
		}
		if hints[uri] != "feature" {
			t.Errorf("entity %q kind hint: got %q; want %q", uri, hints[uri], "feature")
		}
	}
}

// TestDeeplyNestedBoundaryFeatureIsPinned exercises the descent well past any
// depth a hand-authored Release would reach, so the traversal's only bound is the
// document's own nesting.
//
// The descent carries no depth cap, and this is what stands in place of one. A
// boundary holds the boundaries nested inside it by value, so a boundary tree that
// has been decoded is finite and acyclic: each nested boundary is a distinct value
// rather than a reference that could lead back to an ancestor. Depth is therefore
// bounded by the size of the document that was read, and a cap would only decide,
// arbitrarily, a depth past which Features silently stop being pinned.
func TestDeeplyNestedBoundaryFeatureIsPinned(t *testing.T) {
	t.Parallel()

	const depth = 64
	var sb strings.Builder
	sb.WriteString("apiVersion: aggrega.ai/v1alpha1\nkind: Release\nmetadata:\n  name: prod\n  environment: prod\n  version: 1.0.0\nspec:\n  boundaries:\n")
	indent := "    "
	wantPath := "release.spec"
	for i := 0; i < depth; i++ {
		name := fmt.Sprintf("b%d", i)
		sb.WriteString(indent + name + ":\n")
		indent += "  "
		wantPath += ".boundaries." + name
		if i < depth-1 {
			sb.WriteString(indent + "boundaries:\n")
			indent += "  "
		}
	}
	sb.WriteString(indent + "features:\n")
	sb.WriteString(indent + "  deep:\n")
	sb.WriteString(indent + "    type: hub.aggrega.ai/feature/core/deep:1.0.0\n")
	wantPath += ".features.deep.type"

	lookup := &fakeLookup{
		entities: map[string]closure.Resolution{
			"hub.aggrega.ai/feature/core/deep:1.0.0": resolutionOK("1.0.0", "sha256:deep", "tier3-registry"),
		},
	}

	rel := loadRelease(t, sb.String())
	c, err := closure.ComputeClosure(rel, nil, types.NormalizedAggregate{}, workspaceconfig.Default(), lookup)
	if err != nil {
		t.Fatalf("ComputeClosure: %v", err)
	}
	if len(c.Entities) != 1 {
		t.Fatalf("entities: got %d (%v); want 1", len(c.Entities), pinnedURIs(c))
	}
	if c.Entities[0].URI != "hub.aggrega.ai/feature/core/deep:1.0.0" {
		t.Errorf("entity URI: got %q", c.Entities[0].URI)
	}
	if len(c.Entities[0].ReferencedFrom) != 1 || c.Entities[0].ReferencedFrom[0] != wantPath {
		t.Errorf("reference path:\n  got:  %v\n  want: [%s]", c.Entities[0].ReferencedFrom, wantPath)
	}
}

// pinnedURIs lists the closure's pinned entity URIs for a failure message.
func pinnedURIs(c closure.Closure) []string {
	out := make([]string, 0, len(c.Entities))
	for _, e := range c.Entities {
		out = append(out, e.URI)
	}
	return out
}
