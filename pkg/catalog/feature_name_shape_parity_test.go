// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"regexp"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestFeatureNameShapeParity holds the two spellings of the Feature-name
// pattern byte-identical.
//
// Two spellings exist because neither can be folded into the other. The
// spelling in this package is read as SOURCE TEXT by the publishing linter's
// cross-runtime check, which pins both this file's path and the literal form
// of the declaration — so moving it, or turning it into a reference to another
// package's constant, breaks that pin silently. The spelling in pkg/types is
// the one the compiler consults at build time, and it has to live there
// because this package sits ABOVE the compiler in the import graph: a
// compiler pass importing it would make an earlier pipeline stage depend on a
// later one.
//
// This test is what keeps the pair honest. Both patterns are additionally run
// against a shared fixture set, so adding a case on either side without the
// other cannot pass unnoticed — a divergence at the edges (the malformed
// inputs a shape check exists to catch) is exactly the failure two agreeing
// copies would otherwise hide. This package declares its patterns as strings
// only, so the fixture run compiles its side here; pkg/types keeps a compiled
// form because the compiler matches against it on every build.
func TestFeatureNameShapeParity(t *testing.T) {
	t.Parallel()

	if types.FeatureNameShape != FeatureNameShape {
		t.Fatalf("Feature-name pattern diverged:\n  pkg/types:   %q\n  pkg/catalog: %q",
			types.FeatureNameShape, FeatureNameShape)
	}

	catalogRe := regexp.MustCompile(FeatureNameShape)

	fixtures := []struct {
		in    string
		match bool
	}{
		{"core/prometheus-exporter", true},
		{"example/db-binding", true},
		{"core/gcp-workload-identity", true},
		{"a/b", true},
		{"", false},
		{"core", false},
		{"core/", false},
		{"/prometheus-exporter", false},
		{"core/datastore/mariadb", false},
		{"Core/Prometheus", false},
		{"core/prometheus-exporter:1.0.0", false},
		{"1core/exporter", false},
		{"core/exporter ", false},
		{"core/exporter\n", false},
		{"component.aggrega.ai/type=server", false},
	}
	for _, f := range fixtures {
		gotTypes := types.FeatureNameShapeRe.MatchString(f.in)
		gotCatalog := catalogRe.MatchString(f.in)
		if gotTypes != f.match {
			t.Errorf("pkg/types match(%q) = %v; want %v", f.in, gotTypes, f.match)
		}
		if gotCatalog != gotTypes {
			t.Errorf("match(%q) diverged: pkg/types %v, pkg/catalog %v", f.in, gotTypes, gotCatalog)
		}
	}
}
