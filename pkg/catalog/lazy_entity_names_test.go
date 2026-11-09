// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// writeEntityNamesFixture lays out a catalog directory carrying two Features, two
// Components, a Recipe, a Feature with no name, and a file that is not an Aggrega
// document at all, so each kind's enumeration has something to include and
// several different things to exclude.
func writeEntityNamesFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	files := map[string]string{
		// Deliberately named so that filename order and metadata.name order
		// DISAGREE, per kind: the file sorting first carries the name that sorts
		// last. A returned order that matched the walk rather than the names would
		// therefore be visible.
		filepath.Join(dir, "a-second.yaml"): `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: example/zeta
  version: "1.0.0"
spec:
  appliesTo: tier=edge
`,
		filepath.Join(dir, "z-first.yaml"): `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: example/alpha
  version: "1.0.0"
spec: {}
`,
		filepath.Join(dir, "b-component-second.yaml"): `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: example/omega
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec: {}
`,
		filepath.Join(dir, "y-component-first.yaml"): `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: example/beta
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec: {}
`,
		// A Recipe the directory index holds no index for, so no kind's
		// enumeration may offer it.
		filepath.Join(dir, "recipe.yaml"): `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: example-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: example/beta
`,
		// A Feature with no metadata.name is not addressable afterwards, so it
		// is not something the enumeration can offer.
		filepath.Join(dir, "nameless.yaml"): `apiVersion: aggrega.ai/v1beta1
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

// TestEntityNamesEnumeratesTheCatalogDirectoriesInNameOrder covers what the
// accessor reports for each kind and what it leaves out.
//
// The order assertion is the load-bearing one: the fixture names its files so
// that walk order and name order disagree, so a result that merely happened to
// come back sorted because the filesystem returned it that way would fail. The
// rows naming a kind the directory index holds no index for are the bound: an
// accessor that answered one of them from another enumeration — one that also
// reads the artifact cache — would report the Recipe and fail its row.
func TestEntityNamesEnumeratesTheCatalogDirectoriesInNameOrder(t *testing.T) {
	t.Parallel()
	dir := writeEntityNamesFixture(t)
	l := catalog.NewLazyLookup(dir, "", []string{dir}, nil)

	for _, tc := range []struct {
		name string
		kind string
		want []string
	}{
		{"features, in name order", "Feature", []string{"example/alpha", "example/zeta"}},
		{"components, in name order", "Component", []string{"example/beta", "example/omega"}},
		{"a kind with no directory index reports none", "Recipe", nil},
		{"an empty kind reports none", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := l.EntityNames(tc.kind)
			if !slices.Equal(got, tc.want) {
				t.Errorf("EntityNames(%q) = %q, want %q", tc.kind, got, tc.want)
			}
		})
	}
}

// TestEntityNamesReportsNamesTheLookupCanResolve pins the property that makes
// the accessor usable: every name it reports resolves through the per-URI
// lookup of its kind, so a caller enumerating and then resolving never has to
// handle a name the enumeration invented.
//
// Without this, an enumeration and a lookup could disagree about which
// entities exist, and a caller joining the two would silently drop entries.
func TestEntityNamesReportsNamesTheLookupCanResolve(t *testing.T) {
	t.Parallel()
	dir := writeEntityNamesFixture(t)
	l := catalog.NewLazyLookup(dir, "", []string{dir}, nil)

	for _, tc := range []struct {
		kind    string
		resolve func(string) (string, bool)
	}{
		{"Feature", func(n string) (string, bool) {
			f, ok := l.Feature(n)
			return f.Metadata.Name, ok
		}},
		{"Component", func(n string) (string, bool) {
			c, ok := l.Component(n)
			return c.Metadata.Name, ok
		}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			names := l.EntityNames(tc.kind)
			if len(names) == 0 {
				t.Fatalf("no %s names enumerated, so this row proves nothing", tc.kind)
			}
			for _, name := range names {
				resolved, ok := tc.resolve(name)
				if !ok {
					t.Errorf("EntityNames(%q) offered %q, which the %s lookup does not resolve", tc.kind, name, tc.kind)
					continue
				}
				if resolved != name {
					t.Errorf("%s(%q) resolved to metadata.name %q", tc.kind, name, resolved)
				}
			}
		})
	}
}

// TestEntityNamesOnAnUnconfiguredLookupIsEmpty pins that a lookup with no
// catalog directories enumerates nothing rather than failing, for every kind the
// directory index holds, and that the empty result is a real answer about a real
// (empty) search stack.
//
// It is the negative control for the two tests above: without it, a bug that
// returned nothing under every configuration would still pass a test that only
// asserted non-emptiness somewhere else.
func TestEntityNamesOnAnUnconfiguredLookupIsEmpty(t *testing.T) {
	t.Parallel()
	l := catalog.NewLazyLookup(t.TempDir(), "", nil, nil)
	for _, kind := range []string{"Component", "Feature"} {
		if got := l.EntityNames(kind); len(got) != 0 {
			t.Errorf("EntityNames(%q) on a lookup with no catalog directories = %v, want empty", kind, got)
		}
	}
}
