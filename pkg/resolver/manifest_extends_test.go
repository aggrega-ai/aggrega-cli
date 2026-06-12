// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// writeManifestFile writes a Manifest YAML fixture into dir.
func writeManifestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

const prodManifest = `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: prod
  version: 1.0.0
spec:
  mappings:
    components:
      type:
        core/api-server: api-server-helm:1.0.0
        core/postgres: postgres-helm:1.0.0
      name:
        legacy-billing: legacy-worker:3.0.0
    features:
      type:
        core/aws-secrets-manager: aws-secrets-fr:1.0.0
        core/otel: otel-fr:1.0.0
`

// TestResolveManifestExtends_NoExtends_Unchanged — a Manifest with no extends
// reference resolves exactly as authored (non-regression for the common case).
func TestResolveManifestExtends_NoExtends_Unchanged(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifestFile(t, dir, "prod.yaml", prodManifest)
	base, err := ResolveManifest(dir, "./prod.yaml")
	if err != nil {
		t.Fatalf("resolve base: %v", err)
	}
	got, err := ResolveManifestExtends(dir, base.Manifest)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if got != base.Manifest {
		t.Errorf("a Manifest with no extends must be returned unchanged (same pointer)")
	}
	uri, ok := got.Spec.Mappings.Components.Type.Get("core/api-server")
	if !ok || uri != "api-server-helm:1.0.0" {
		t.Errorf("base mapping = %q,%v; want api-server-helm:1.0.0", uri, ok)
	}
}

// TestResolveManifestExtends_ShallowKeyReplacement — a child overrides one
// parent key within a tier; sibling parent keys survive; child-only keys are
// added; and a separate tier (name) inherits untouched.
func TestResolveManifestExtends_ShallowKeyReplacement(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifestFile(t, dir, "prod.yaml", prodManifest)
	// local overlay: swap the one credential FeatureRecipe and the postgres
	// component recipe; add a child-only component key; leave everything else.
	writeManifestFile(t, dir, "local.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: local
  version: 1.0.0
spec:
  extends: ./prod.yaml
  mappings:
    components:
      type:
        core/postgres: postgres-local:9.9.9
        core/cache: redis-local:1.0.0
    features:
      type:
        core/aws-secrets-manager: host-env-injector:1.0.0
`)
	child, err := ResolveManifest(dir, "./local.yaml")
	if err != nil {
		t.Fatalf("resolve child: %v", err)
	}
	got, err := ResolveManifestExtends(dir, child.Manifest)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	comps := got.Spec.Mappings.Components.Type
	// child overrides parent key
	if uri, _ := comps.Get("core/postgres"); uri != "postgres-local:9.9.9" {
		t.Errorf("core/postgres = %q; want child override postgres-local:9.9.9", uri)
	}
	// sibling parent key survives
	if uri, _ := comps.Get("core/api-server"); uri != "api-server-helm:1.0.0" {
		t.Errorf("core/api-server = %q; want inherited api-server-helm:1.0.0", uri)
	}
	// child-only key added
	if uri, _ := comps.Get("core/cache"); uri != "redis-local:1.0.0" {
		t.Errorf("core/cache = %q; want child-only redis-local:1.0.0", uri)
	}
	// feature tier: child overrides one key
	feats := got.Spec.Mappings.Features.Type
	if uri, _ := feats.Get("core/aws-secrets-manager"); uri != "host-env-injector:1.0.0" {
		t.Errorf("core/aws-secrets-manager = %q; want child override host-env-injector:1.0.0", uri)
	}
	// feature sibling parent key survives
	if uri, _ := feats.Get("core/otel"); uri != "otel-fr:1.0.0" {
		t.Errorf("core/otel = %q; want inherited otel-fr:1.0.0", uri)
	}
	// name tier inherited untouched (child declared none)
	if uri, _ := got.Spec.Mappings.Components.Name.Get("legacy-billing"); uri != "legacy-worker:3.0.0" {
		t.Errorf("name-tier legacy-billing = %q; want inherited legacy-worker:3.0.0", uri)
	}
	// the composed Manifest is structurally standalone — extends is cleared
	if got.Spec.Extends != "" {
		t.Errorf("composed Manifest still carries extends %q; want empty", got.Spec.Extends)
	}
	// identity comes from the child
	if got.Metadata.Name != "local" {
		t.Errorf("composed identity = %q; want child name local", got.Metadata.Name)
	}
}

// TestResolveManifestExtends_MultiLevelChildWins — a three-deep chain folds
// root → middle → child so the most-derived value wins on a key all three set.
func TestResolveManifestExtends_MultiLevelChildWins(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifestFile(t, dir, "root.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: root
  version: 1.0.0
spec:
  mappings:
    components:
      type:
        core/db: db-root:1.0.0
        core/only-root: only-root:1.0.0
`)
	writeManifestFile(t, dir, "mid.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: mid
  version: 1.0.0
spec:
  extends: ./root.yaml
  mappings:
    components:
      type:
        core/db: db-mid:2.0.0
        core/only-mid: only-mid:1.0.0
`)
	writeManifestFile(t, dir, "leaf.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: leaf
  version: 1.0.0
spec:
  extends: ./mid.yaml
  mappings:
    components:
      type:
        core/db: db-leaf:3.0.0
`)
	child, err := ResolveManifest(dir, "./leaf.yaml")
	if err != nil {
		t.Fatalf("resolve leaf: %v", err)
	}
	got, err := ResolveManifestExtends(dir, child.Manifest)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	comps := got.Spec.Mappings.Components.Type
	if uri, _ := comps.Get("core/db"); uri != "db-leaf:3.0.0" {
		t.Errorf("core/db = %q; want leaf wins db-leaf:3.0.0", uri)
	}
	if uri, _ := comps.Get("core/only-root"); uri != "only-root:1.0.0" {
		t.Errorf("core/only-root = %q; want inherited only-root:1.0.0", uri)
	}
	if uri, _ := comps.Get("core/only-mid"); uri != "only-mid:1.0.0" {
		t.Errorf("core/only-mid = %q; want inherited only-mid:1.0.0", uri)
	}
}

// TestResolveManifestExtends_Cycle — a chain that revisits a Manifest halts
// with ErrCyclicExtension rather than looping.
func TestResolveManifestExtends_Cycle(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifestFile(t, dir, "a.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: a
  version: 1.0.0
spec:
  extends: ./b.yaml
  mappings:
    components:
      type:
        core/x: x:1.0.0
`)
	writeManifestFile(t, dir, "b.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: b
  version: 1.0.0
spec:
  extends: ./a.yaml
  mappings:
    components:
      type:
        core/y: y:1.0.0
`)
	child, err := ResolveManifest(dir, "./a.yaml")
	if err != nil {
		t.Fatalf("resolve a: %v", err)
	}
	_, err = ResolveManifestExtends(dir, child.Manifest)
	if err == nil {
		t.Fatalf("cycle: expected error, got nil")
	}
	if !errors.Is(err, validator.ErrCyclicExtension) {
		t.Errorf("cycle error = %v; want wrapped ErrCyclicExtension", err)
	}
}

// TestResolveManifestExtends_UnresolvableParent — an extends pointing at a
// missing Manifest halts with a merge error rather than silently dropping
// inheritance.
func TestResolveManifestExtends_UnresolvableParent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifestFile(t, dir, "child.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: child
  version: 1.0.0
spec:
  extends: ./does-not-exist.yaml
  mappings:
    components:
      type:
        core/x: x:1.0.0
`)
	child, err := ResolveManifest(dir, "./child.yaml")
	if err != nil {
		t.Fatalf("resolve child: %v", err)
	}
	_, err = ResolveManifestExtends(dir, child.Manifest)
	if err == nil {
		t.Fatalf("missing parent: expected error, got nil")
	}
	if !errors.Is(err, validator.ErrInheritanceMerge) {
		t.Errorf("missing-parent error = %v; want wrapped ErrInheritanceMerge", err)
	}
}
