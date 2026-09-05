// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// writeFeatureNamesFixture lays out a catalog directory carrying two Features,
// a Component, and a file that is not an Aggrega document at all, so the
// enumeration has something to include and three different things to exclude.
func writeFeatureNamesFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	files := map[string]string{
		// Deliberately named so that filename order and metadata.name order
		// DISAGREE: the file "a-second.yaml" carries the name that sorts last.
		// A returned order that matched the walk rather than the names would
		// therefore be visible.
		filepath.Join(dir, "a-second.yaml"): `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: example/zeta
  version: "1.0.0"
spec:
  appliesTo: tier=edge
`,
		filepath.Join(dir, "z-first.yaml"): `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: example/alpha
  version: "1.0.0"
spec: {}
`,
		// A Component, to prove the enumeration is kind-scoped.
		filepath.Join(dir, "component.yaml"): `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: example/not-a-feature
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec: {}
`,
		// A Feature with no metadata.name is not addressable afterwards, so it
		// is not something the enumeration can offer.
		filepath.Join(dir, "nameless.yaml"): `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  version: "1.0.0"
spec: {}
`,
		// Foreign YAML, declaring no kind at all.
		filepath.Join(dir, "values.yaml"): "replicas: 3\nimage: nginx\n",
	}
	for path, body := range files {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return dir
}

// TestFeatureNamesEnumeratesTheCatalogDirectoriesInNameOrder covers what the
// accessor reports and what it leaves out.
//
// The order assertion is the load-bearing one: the fixture names its files so
// that walk order and name order disagree, so a result that merely happened to
// come back sorted because the filesystem returned it that way would fail.
func TestFeatureNamesEnumeratesTheCatalogDirectoriesInNameOrder(t *testing.T) {
	t.Parallel()
	dir := writeFeatureNamesFixture(t)
	l := catalog.NewLazyLookup(dir, "", []string{dir}, nil)

	got := l.FeatureNames()
	want := []string{"example/alpha", "example/zeta"}
	if len(got) != len(want) {
		t.Fatalf("FeatureNames() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("FeatureNames() = %v, want %v", got, want)
		}
	}
}

// TestFeatureNamesReportsNamesTheLookupCanResolve pins the property that makes
// the accessor usable: every name it reports resolves through the per-URI
// lookup, so a caller enumerating and then resolving never has to handle a
// name the enumeration invented.
//
// Without this, an enumeration and a lookup could disagree about which
// Features exist, and a caller joining the two would silently drop entries.
func TestFeatureNamesReportsNamesTheLookupCanResolve(t *testing.T) {
	t.Parallel()
	dir := writeFeatureNamesFixture(t)
	l := catalog.NewLazyLookup(dir, "", []string{dir}, nil)

	names := l.FeatureNames()
	if len(names) == 0 {
		t.Fatal("no names enumerated, so this test proves nothing")
	}
	for _, name := range names {
		feat, ok := l.Feature(name)
		if !ok {
			t.Errorf("FeatureNames() offered %q, which Feature() does not resolve", name)
			continue
		}
		if feat.Metadata.Name != name {
			t.Errorf("Feature(%q) resolved to metadata.name %q", name, feat.Metadata.Name)
		}
	}
}

// TestFeatureNamesOnAnUnconfiguredLookupIsEmpty pins that a lookup with no
// catalog directories enumerates nothing rather than failing, and that the
// empty result is a real answer about a real (empty) search stack.
//
// It is the negative control for the two tests above: without it, a bug that
// returned nothing under every configuration would still pass a test that only
// asserted non-emptiness somewhere else.
func TestFeatureNamesOnAnUnconfiguredLookupIsEmpty(t *testing.T) {
	t.Parallel()
	if got := catalog.NewLazyLookup(t.TempDir(), "", nil, nil).FeatureNames(); len(got) != 0 {
		t.Errorf("FeatureNames() on a lookup with no catalog directories = %v, want empty", got)
	}
}
