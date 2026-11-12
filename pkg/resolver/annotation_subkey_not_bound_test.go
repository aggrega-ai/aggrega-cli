// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestAuthoredAnnotationRules_ASubkeyItsBindingDoesNotRouteIsRefused drives the
// rules over an authored annotation through the entry point the pipeline calls,
// over structured values written under a key the Manifest binds, and holds the
// unbound-subkey condition to the subkeys it names.
//
// Each row carries one fault, asserted against the condition that fault pins. A
// subkey the binding does not route is refused on either side, the finding naming
// the subkey, the key, every path the binding does route and the scope it was
// written at. A routed subkey whose value the target Feature refuses keeps the
// conformance condition and never this one. A key the Manifest binds nothing to
// is passed over, and so are subkeys the binding routes — the controls.
func TestAuthoredAnnotationRules_ASubkeyItsBindingDoesNotRouteIsRefused(t *testing.T) {
	t.Parallel()
	declaredPaths := []string{"pool_size", "max_connections", "db_name", "read_replicas"}
	cases := []struct {
		name      string
		release   string
		aggregate string
		// want is nil for a row that must resolve.
		want       error
		names      []string
		scope      string
		category   cerrors.Category
		notUnbound bool
	}{
		{
			name:     "a Release subkey the binding does not route",
			release:  "feature.myco/db-pool:\n  nosuch: 1\n",
			want:     ErrAnnotationSubkeyNotBound(),
			names:    append([]string{"nosuch", "feature.myco/db-pool", "Release test"}, declaredPaths...),
			scope:    ".spec.boundaries.prod.components.api.annotations",
			category: cerrors.OpsError,
		},
		{
			name:      "an Aggregate subkey the binding does not route",
			aggregate: "feature.myco/db-pool:\n  dbname: \"orders\"\n",
			want:      ErrAnnotationSubkeyNotBound(),
			names:     append([]string{"dbname", "feature.myco/db-pool", "Aggregate test"}, declaredPaths...),
			scope:     ".spec.components.api.annotations",
			category:  cerrors.DeveloperError,
		},
		{
			name:       "a routed subkey whose value the Feature refuses keeps the conformance condition",
			release:    "feature.myco/db-pool:\n  pool_size: \"many\"\n",
			want:       ErrAnnotationSubkeyNotConforming(),
			names:      []string{"pool_size"},
			notUnbound: true,
		},
		{
			name:    "a key the Manifest binds nothing to is passed over",
			release: "feature.other/unbound:\n  anything: 1\n",
		},
		{
			name:      "subkeys the binding routes are passed",
			release:   "feature.myco/db-pool:\n  pool_size: 4\n",
			aggregate: "feature.myco/db-pool:\n  db_name: \"orders\"\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rr := &ResolveResult{
				Manifest:      structuredManifest(t),
				AggregatePath: "aggregate.yaml",
				ReleasePath:   "release.yaml",
			}
			if tc.release != "" {
				rr.Release = releaseAnnotating(t, tc.release)
			}
			if tc.aggregate != "" {
				rr.Aggregate = aggregateAnnotating(t, tc.aggregate)
			}
			err := ValidateAuthoredAnnotations(nil, rr, structuredFeatureCatalog(t))
			if tc.want == nil {
				if err != nil {
					t.Fatalf("the control was refused: %s", cerrors.CauseChain(err))
				}
				return
			}
			if err == nil {
				t.Fatalf("expected the annotation to be refused under %v; got nil", tc.want)
			}
			if !errors.Is(err, tc.want) {
				t.Fatalf("refusal is not the condition this row pins (%v): %s", tc.want, cerrors.CauseChain(err))
			}
			if tc.notUnbound && errors.Is(err, ErrAnnotationSubkeyNotBound()) {
				t.Errorf("a subkey the binding routes was refused as unrouted: %s", cerrors.CauseChain(err))
			}
			chain := cerrors.CauseChain(err)
			for _, want := range tc.names {
				if !strings.Contains(chain, want) {
					t.Errorf("the refusal does not name %q: %s", want, chain)
				}
			}
			if tc.scope == "" {
				return
			}
			if !strings.Contains(chain, tc.scope) {
				t.Errorf("the refusal is not positioned at the authored scope %q: %s", tc.scope, chain)
			}
			var finding *ResolverError
			if !errors.As(err, &finding) {
				t.Fatalf("the refusal carries no finding: %s", chain)
			}
			if got := finding.ToCompilerError().Category(); got != tc.category {
				t.Errorf("the finding is filed under %q, want %q", got, tc.category)
			}
		})
	}
}

// TestAnnotationBindingDeclaredRoutes_AnswersTheBindingWithoutADocument holds the
// fact the unbound-subkey rule reads to the presentation it is answered from: the
// routes of a declared binding, infra before values and each in the order the
// binding declares them; whether the key is bound at all; and the same routes the
// document-keyed fact answers, so the two read the binding one way.
func TestAnnotationBindingDeclaredRoutes_AnswersTheBindingWithoutADocument(t *testing.T) {
	t.Parallel()
	const document = "manifest.yaml"
	view := newManifestDocumentView(document, structuredManifest(t))
	want := []constraint.AnnotationBindingRoute{
		{Pillar: "infra", Path: "pool_size"},
		{Pillar: "infra", Path: "max_connections"},
		{Pillar: "values", Path: "db_name"},
		{Pillar: "values", Path: "read_replicas"},
	}

	routes, bound := view.AnnotationBindingDeclaredRoutes("feature.myco/db-pool")
	if !bound {
		t.Fatal("a key the Manifest binds is answered as unbound")
	}
	if !reflect.DeepEqual(routes, want) {
		t.Errorf("routes = %+v, want %+v", routes, want)
	}
	if keyed := view.ManifestAnnotationBindingRoutes(document, "feature.myco/db-pool"); !reflect.DeepEqual(keyed, routes) {
		t.Errorf("the document-keyed routes %+v differ from the binding's own %+v", keyed, routes)
	}

	routes, bound = view.AnnotationBindingDeclaredRoutes("feature.other/unbound")
	if bound || routes != nil {
		t.Errorf("a key nobody binds is answered as (%+v, %v), want (nil, false)", routes, bound)
	}
}
