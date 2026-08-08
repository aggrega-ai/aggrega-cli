// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

func tierWith(typeEntries, nameEntries map[string]string, order []string) *types.MappingTier {
	t := &types.MappingTier{}
	for _, k := range order {
		if v, ok := typeEntries[k]; ok {
			t.Type.Set(k, v)
		}
		if v, ok := nameEntries[k]; ok {
			t.Name.Set(k, v)
		}
	}
	return t
}

// TestManifestRecipeReferenceFaults_AcceptsEveryShapeTheGrammarAllows pins the
// negative direction. Reporting a well-shaped reference would halt authoring the
// grammar explicitly accepts, which is worse than the misreport this reading
// exists to remove.
func TestManifestRecipeReferenceFaults_AcceptsEveryShapeTheGrammarAllows(t *testing.T) {
	t.Parallel()
	order := []string{"a", "b", "c", "d"}
	m := &types.Manifest{}
	m.Spec.Mappings.Components = tierWith(map[string]string{
		"a": "mariadb",
		"b": "mariadb:1.0.0",
		"c": "helm/mariadb",
		"d": "helm/mariadb:1.0.0",
	}, nil, order)

	if faults := ManifestRecipeReferenceFaults(m); len(faults) != 0 {
		t.Fatalf("well-shaped references reported %d fault(s): %v", len(faults), faults)
	}
}

// TestManifestRecipeReferenceFaults_ReportsEveryMisShapedValue asserts the
// enumeration: one fault per offending value, over both tiers of both flat
// sections and the per-provider shape, so a Manifest authoring several of them
// does not have all but one hidden.
func TestManifestRecipeReferenceFaults_ReportsEveryMisShapedValue(t *testing.T) {
	t.Parallel()
	m := &types.Manifest{}
	m.Spec.Mappings.Components = tierWith(
		map[string]string{"core/compute/api": "core/helm/api-server:1.0.0"},
		map[string]string{"web": "a/b/c"},
		[]string{"core/compute/api", "web"},
	)
	m.Spec.Mappings.Features = tierWith(
		map[string]string{"core/dns": "x/y/z:2.0.0"}, nil, []string{"core/dns"},
	)
	legacy := tierWith(map[string]string{"core/db": "p/q/r"}, nil, []string{"core/db"})
	m.Spec.Mappings.Legacy.Set("helm", types.ProviderMappings{Components: legacy})

	faults := ManifestRecipeReferenceFaults(m)
	if len(faults) != 4 {
		t.Fatalf("reported %d fault(s), want 4 (one per mis-shaped value): %v", len(faults), faults)
	}
	for _, f := range faults {
		if !errors.Is(f, ErrMalformedRecipeReference()) {
			t.Errorf("fault does not wrap the shape sentinel: %v", f)
		}
	}
	// Each fault names its own coordinate, so an author with several of them can
	// tell which mapping entry each one is about.
	for _, want := range []string{
		"spec.mappings.components.type.core/compute/api",
		"spec.mappings.components.name.web",
		"spec.mappings.features.type.core/dns",
		"spec.mappings.helm.components.type.core/db",
	} {
		found := false
		for _, f := range faults {
			if strings.Contains(f.Error(), want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no fault names the coordinate %q; got %v", want, faults)
		}
	}
}

// TestManifestRecipeReferenceFaults_QuotesTheGrammarsSingleHome is the
// single-source guard. The accepted-shape statement and the segment ceiling have
// one home; a reading that restated either would go quietly false the day a
// publisher position enters the grammar. Asserting the message carries the
// constant's own text is what makes that impossible to miss.
func TestManifestRecipeReferenceFaults_QuotesTheGrammarsSingleHome(t *testing.T) {
	t.Parallel()
	m := &types.Manifest{}
	m.Spec.Mappings.Components = tierWith(
		map[string]string{"core/compute/api": "a/b/c/d"}, nil, []string{"core/compute/api"},
	)
	faults := ManifestRecipeReferenceFaults(m)
	if len(faults) != 1 {
		t.Fatalf("want exactly one fault, got %d: %v", len(faults), faults)
	}
	msg := faults[0].Error()
	if !strings.Contains(msg, acceptedRecipeReferenceShapes) {
		t.Errorf("fault does not quote the accepted-shape statement verbatim: %q", msg)
	}
	if !strings.Contains(msg, "4 slash-separated segments") {
		t.Errorf("fault does not report the observed segment count: %q", msg)
	}
}

// TestManifestRecipeReferenceFaults_NilInputs covers the two empty shapes a
// caller can hand it: no Manifest at all, and a Manifest declaring no mappings.
func TestManifestRecipeReferenceFaults_NilInputs(t *testing.T) {
	t.Parallel()
	if faults := ManifestRecipeReferenceFaults(nil); faults != nil {
		t.Errorf("nil Manifest reported %v", faults)
	}
	if faults := ManifestRecipeReferenceFaults(&types.Manifest{}); faults != nil {
		t.Errorf("empty Manifest reported %v", faults)
	}
}
