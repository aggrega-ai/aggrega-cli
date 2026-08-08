// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"
)

const dupBoundaryRelease = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: prod
  environment: production
  version: "1.0.0"
spec:
  aggregate: test-app
  manifest: test-manifest
  boundaries:
    outer:
      directory: ./outer
      components:
        api:
          infra:
            replicas: 1
      boundaries:
        inner:
          directory: ./inner
          components:
            api:
              uses:
                databases:
                  main:
                    infra:
                      pool_size: 9
`

// TestLoadRelease_DuplicateBoundaryComponentHalts pins the load-time rejection:
// one component declared in two nested boundaries halts with the sentinel, and
// the diagnostic names BOTH declaration sites plus the component.
func TestLoadRelease_DuplicateBoundaryComponentHalts(t *testing.T) {
	t.Parallel()
	_, err := LoadReleaseFromBytes([]byte(dupBoundaryRelease), "release.yaml")
	if !errors.Is(err, ErrDuplicateBoundaryComponent()) {
		t.Fatalf("want ErrDuplicateBoundaryComponent; got %v", err)
	}
	for _, want := range []string{`"api"`, `"outer"`, `"outer/inner"`, "release.yaml"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("diagnostic must name %s; got %q", want, err.Error())
		}
	}
	// The locator points at the SECOND declaration — the one the author can
	// delete — while the message carries both.
	var re *ResolverError
	if !errors.As(err, &re) {
		t.Fatalf("want a *ResolverError; got %T", err)
	}
	if re.Field != `.spec.boundaries["outer/inner"].components["api"]` {
		t.Errorf("locator must name the second declaration site; got %q", re.Field)
	}
}

// TestLoadRelease_DuplicateBoundaryComponentSiblingBoundariesHalt pins that the
// rule is about the boundary TREE, not just an ancestor chain: two sibling
// boundaries declaring one name diverge the same way and are rejected too.
func TestLoadRelease_DuplicateBoundaryComponentSiblingBoundariesHalt(t *testing.T) {
	t.Parallel()
	siblings := `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: prod
  environment: production
  version: "1.0.0"
spec:
  aggregate: test-app
  manifest: test-manifest
  boundaries:
    left:
      components:
        api: {}
    right:
      components:
        api: {}
`
	_, err := LoadReleaseFromBytes([]byte(siblings), "release.yaml")
	if !errors.Is(err, ErrDuplicateBoundaryComponent()) {
		t.Fatalf("want ErrDuplicateBoundaryComponent for sibling boundaries; got %v", err)
	}
	for _, want := range []string{`"left"`, `"right"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("diagnostic must name %s; got %q", want, err.Error())
		}
	}
}

// TestLoadRelease_DistinctComponentsPerBoundaryAccepted is the counter-case: the
// rule rejects a duplicated NAME, not nesting itself. Distinct names across
// nested boundaries, and one name declared once, both load clean.
func TestLoadRelease_DistinctComponentsPerBoundaryAccepted(t *testing.T) {
	t.Parallel()
	ok := `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: prod
  environment: production
  version: "1.0.0"
spec:
  aggregate: test-app
  manifest: test-manifest
  boundaries:
    outer:
      components:
        api: {}
      boundaries:
        inner:
          components:
            worker: {}
`
	rel, err := LoadReleaseFromBytes([]byte(ok), "release.yaml")
	if err != nil {
		t.Fatalf("distinct names per boundary must load clean; got %v", err)
	}
	if rel == nil {
		t.Fatal("want a loaded Release")
	}
}

// TestLoadRelease_SynthesizedDefaultIsNotADuplicate pins that the folded
// top-level components surface does not trip the rule: normalization puts them
// in one synthesized boundary, so each name is declared exactly once.
func TestLoadRelease_SynthesizedDefaultIsNotADuplicate(t *testing.T) {
	t.Parallel()
	rootOnly := `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: prod
  environment: production
  version: "1.0.0"
spec:
  aggregate: test-app
  manifest: test-manifest
  components:
    api: {}
    worker: {}
`
	rel, err := LoadReleaseFromBytes([]byte(rootOnly), "release.yaml")
	if err != nil {
		t.Fatalf("a root-only Release must load clean; got %v", err)
	}
	def, found := rel.Spec.Boundaries.Get("default")
	if !found || !def.Synthesized {
		t.Fatalf("want the synthesized default boundary; found=%v synthesized=%v", found, def.Synthesized)
	}
}
