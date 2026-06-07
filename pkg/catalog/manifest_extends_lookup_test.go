// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// TestThreeTierLookup_OnComposedManifest proves the recipe cascade operates on
// the extends-composed Manifest: a child overlay that overrides one parent
// mapping and adds its own resolves through RecipeLookupWithManifest against the
// composed tiers — the inherited parent key, the child override, and a child-only
// key all dispatch correctly, and the name tier inherited from the parent wins
// over the type tier on the instance it names.
func TestThreeTierLookup_OnComposedManifest(t *testing.T) {
	t.Parallel()
	c := fakeCatalog(
		// parent-mapped recipes
		fakeRecipe("api-server-helm", "core/api-server"),
		fakeRecipe("postgres-prod", "core/postgres"),
		fakeRecipe("legacy-worker", "core/postgres"),
		// child-mapped recipes
		fakeRecipe("postgres-local", "core/postgres"),
		fakeRecipe("redis-local", "core/cache"),
	)

	dir := t.TempDir()
	writeManifestFixture(t, dir, "prod.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: prod
spec:
  mappings:
    components:
      type:
        core/api-server: api-server-helm
        core/postgres: postgres-prod
      name:
        billing-db: legacy-worker
`)
	writeManifestFixture(t, dir, "local.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: local
spec:
  extends: ./prod.yaml
  mappings:
    components:
      type:
        core/postgres: postgres-local
        core/cache: redis-local
`)

	child, err := resolver.ResolveManifest(dir, "./local.yaml")
	if err != nil {
		t.Fatalf("resolve child: %v", err)
	}
	composed, err := resolver.ResolveManifestExtends(dir, child.Manifest)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	lookup := c.RecipeLookupWithManifest(composed)

	assertRecipe := func(label, instance, compType, want string) {
		t.Helper()
		r, ok := lookup.Recipe(instance, compType)
		if !ok {
			t.Errorf("%s: lookup miss for %q; want %q", label, compType, want)
			return
		}
		if r.Metadata.Name != want {
			t.Errorf("%s: resolved %q; want %q", label, r.Metadata.Name, want)
		}
	}

	// inherited parent type-tier key
	assertRecipe("inherited type", "api", "core/api-server", "api-server-helm")
	// child override on a shared key
	assertRecipe("child override", "db", "core/postgres", "postgres-local")
	// child-only key
	assertRecipe("child-only", "cache", "core/cache", "redis-local")
	// inherited parent name-tier override wins over the type tier for the
	// instance it names
	assertRecipe("inherited name tier", "billing-db", "core/postgres", "legacy-worker")
}

func writeManifestFixture(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}
