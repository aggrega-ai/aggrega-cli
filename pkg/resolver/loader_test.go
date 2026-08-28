// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

func TestLoadAggregate_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "aggregate.yaml")
	yaml := `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: test-app
  version: "1.0.0"
spec:
  components:
    web:
      type: core/server:1.0.0
`
	os.WriteFile(f, []byte(yaml), 0644)

	agg, err := LoadAggregate(f)
	if err != nil {
		t.Fatal(err)
	}
	if agg.Metadata.Name != "test-app" {
		t.Errorf("expected name test-app, got %s", agg.Metadata.Name)
	}
	if agg.Metadata.Version != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %s", agg.Metadata.Version)
	}
	// Load normalizes: a top-level components surface folds into the
	// synthesized `default` subsystem (root cleared).
	if agg.Spec.Components.Len() != 0 {
		t.Errorf("top-level components must fold into the default subsystem; got %d", agg.Spec.Components.Len())
	}
	def, ok := agg.Spec.Subsystems.Get("default")
	if !ok {
		t.Fatal("expected a synthesized 'default' subsystem holding the top-level components")
	}
	if !def.Synthesized {
		t.Error("the synthesized default subsystem must be marked Synthesized")
	}
	if _, ok := def.Components.Get("web"); !ok {
		t.Error("expected component 'web' in the synthesized default subsystem")
	}
}

func TestLoadAggregate_FileNotFound(t *testing.T) {
	_, err := LoadAggregate("/nonexistent/aggregate.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !errors.Is(err, ErrYAMLLoad()) {
		t.Errorf("expected ErrYAMLLoad, got %v", err)
	}
}

func TestLoadAggregate_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "bad.yaml")
	// YAML v3 is lenient with many inputs; use a truly malformed document
	os.WriteFile(f, []byte("apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n\t- mixed tabs and spaces"), 0644)

	_, err := LoadAggregate(f)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
	// A malformed document is caught by the envelope gate, which runs before the
	// typed unmarshal and reports every failure under its own sentinel. The
	// assertion pairs the sentinel with the message so a future change cannot
	// keep the sentinel while losing the parser's account of what is wrong.
	if !errors.Is(err, envelope.ErrEntityEnvelope()) {
		t.Errorf("expected ErrEntityEnvelope, got %v", err)
	}
	if !strings.Contains(err.Error(), "found character that cannot start any token") {
		t.Errorf("error should carry the parser's account of the fault; got %v", err)
	}
}

func TestLoadRelease_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "release.yaml")
	yaml := `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: prod
  environment: production
  version: "1.0.0"
spec:
  aggregate: test-app
  manifest: test-manifest
  boundaries:
    aws-prod:
      directory: ./output
      components:
        web:
          infra:
            replicas: 3
`
	os.WriteFile(f, []byte(yaml), 0644)

	rel, err := LoadRelease(f)
	if err != nil {
		t.Fatal(err)
	}
	if rel.Metadata.Name != "prod" {
		t.Errorf("expected name prod, got %s", rel.Metadata.Name)
	}
	if rel.Metadata.Environment != "production" {
		t.Errorf("expected environment production, got %s", rel.Metadata.Environment)
	}
	if _, ok := rel.Spec.Boundaries.Get("aws-prod"); !ok {
		t.Error("expected boundary 'aws-prod'")
	}
}

func TestLoadManifest_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "manifest.yaml")
	yaml := `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        "core/server": "recipes/helm-server:1.0.0"
`
	os.WriteFile(f, []byte(yaml), 0644)

	man, err := LoadManifest(f)
	if err != nil {
		t.Fatal(err)
	}
	if man.Metadata.Name != "test-manifest" {
		t.Errorf("expected name test-manifest, got %s", man.Metadata.Name)
	}
	if man.Spec.Mappings.Components == nil {
		t.Fatal("expected components mappings to be populated")
	}
	if got, ok := man.Spec.Mappings.Components.Type.Get("core/server"); !ok || got != "recipes/helm-server:1.0.0" {
		t.Errorf("components.type[core/server] = %q, %v; want recipes/helm-server:1.0.0, true", got, ok)
	}
}

// TestLoadManifest_RejectsUnknownMappingKey pins the LOADER-path refusal of a
// mapping key that is neither components nor features: the document is
// schema-validated before it is decoded, so the refusal an author meets comes
// from the envelope gate. The diagnostic is asserted to name both the offending
// key and the accepted ones, because a refusal that only says "no" leaves the
// author to guess what to write instead.
func TestLoadManifest_RejectsUnknownMappingKey(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "manifest.yaml")
	yaml := `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test-manifest
  version: "1.0.0"
spec:
  mappings:
    helm:
      components:
        type:
          "core/server": "recipes/helm-server:1.0.0"
`
	os.WriteFile(f, []byte(yaml), 0644)

	_, err := LoadManifest(f)
	if err == nil {
		t.Fatal("expected a refusal for an unknown mappings key")
	}
	if !errors.Is(err, envelope.ErrEntityEnvelope()) {
		t.Fatalf("refusal must carry the envelope sentinel, which is what the loader gates on; got %v", err)
	}
	got := err.Error()
	for _, want := range []string{`"helm"`, "components", "features"} {
		if !strings.Contains(got, want) {
			t.Errorf("refusal does not name %s; got %q", want, got)
		}
	}
}

func TestLoadManifest_RejectsReservedPrefix_FlatShape(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "manifest.yaml")
	yaml := `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test-manifest
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations:
        aggrega.ai/forbidden-binding:
          feature: core/forbidden:1.0.0
          maps:
            infra:
              - target
`
	os.WriteFile(f, []byte(yaml), 0644)

	_, err := LoadManifest(f)
	if err == nil {
		t.Fatal("expected a refusal on the reserved-prefix binding; got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "aggrega.ai/forbidden-binding") {
		t.Errorf("error message should name the offending key; got: %s", msg)
	}
	if !strings.Contains(msg, "reserved prefix") {
		t.Errorf("error message should explain the reservation; got: %s", msg)
	}
	if !strings.Contains(msg, "feature.<your-domain>/") {
		t.Errorf("error message should name the compliant shape the author writes instead; got: %s", msg)
	}
	if !errors.Is(err, ErrManifestReservedPrefix()) {
		t.Errorf("error should wrap ErrManifestReservedPrefix (not ErrYAMLLoad); got chain: %v", err)
	}
	if errors.Is(err, ErrYAMLLoad()) {
		t.Errorf("error should NOT wrap ErrYAMLLoad — the YAML parsed successfully; the failure is a semantic reservation check")
	}
}

// The refusal is attributed to the catalog-authoring domain, and it is attributed
// there structurally rather than by a category word typed into the message.
//
// The category is derived from the entity reference's leading kind word, so this
// asserts the routing rather than the spelling: a finding whose entity reference
// stopped naming the Manifest would silently be reported at the developer's
// authoring surface, which is a file the author of the offending binding did not
// write in.
//
// It also pins that the reference still carries the document the binding was
// authored in. The refusal's locator names a key path inside that document rather
// than the file, so a reader who lost the file from the reference would be handed
// a path with no file to open it in.
func TestLoadManifest_ReservedPrefixRefusalIsCatalogDomain(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "manifest.yaml")
	os.WriteFile(f, reservedPrefixManifestYAML("aggrega.ai/forbidden-binding"), 0644)

	_, err := LoadManifest(f)
	if err == nil {
		t.Fatal("expected a refusal on the reserved-prefix binding; got nil")
	}
	var finding *ResolverError
	if !errors.As(err, &finding) {
		t.Fatalf("refusal should carry a *ResolverError so the boundary can route it; got %T", err)
	}
	if got, want := finding.ToCompilerError().Category, cerrors.CatalogError; got != want {
		t.Errorf("category: got %q, want %q — a Manifest binding is catalog-authored content", got, want)
	}
	if !strings.HasPrefix(finding.Entity, "Manifest ") {
		t.Errorf("entity reference %q should lead with the Manifest kind, which is what the "+
			"category is derived from", finding.Entity)
	}
	if !strings.Contains(finding.Entity, f) {
		t.Errorf("entity reference %q should name the document the binding was authored in, "+
			"because the locator names a key path rather than a file", finding.Entity)
	}
	if got, want := finding.Field, ".spec.mappings.features.annotations"; got != want {
		t.Errorf("locator: got %q, want %q — the registry's own authored site", got, want)
	}
}

// Every offending binding is reported, not only the first.
//
// The rule is declared over the annotation-binding coordinate, so the framework
// invokes it once per declared binding and accumulates what each invocation
// raised. An author fixing a Manifest with three offending keys sees all three in
// one build rather than one per build.
//
// The clean binding in the fixture is what makes the count a verdict rather than
// a constant: a rule raising on every binding regardless would report four.
func TestLoadManifest_ReservedPrefixNamesEveryOffendingBinding(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "manifest.yaml")
	os.WriteFile(f, reservedPrefixManifestYAML(
		"aggrega.ai/first-forbidden",
		"feature.example/permitted",
		"aggrega.ai/second-forbidden",
		"aggrega.ai/third-forbidden",
	), 0644)

	_, err := LoadManifest(f)
	if err == nil {
		t.Fatal("expected a refusal on the reserved-prefix bindings; got nil")
	}

	var joined interface{ Unwrap() []error }
	if !errors.As(err, &joined) {
		t.Fatalf("three offending bindings should compose into a joined refusal; got %T: %v", err, err)
	}
	if got, want := len(joined.Unwrap()), 3; got != want {
		t.Fatalf("findings: got %d, want %d — one per offending binding and none for the clean one",
			got, want)
	}

	msg := err.Error()
	for _, key := range []string{
		"aggrega.ai/first-forbidden",
		"aggrega.ai/second-forbidden",
		"aggrega.ai/third-forbidden",
	} {
		if !strings.Contains(msg, key) {
			t.Errorf("refusal does not name the offending binding %q; got: %s", key, msg)
		}
	}
	if strings.Contains(msg, "feature.example/permitted") {
		t.Errorf("refusal names the permitted binding, so the rule is raising on every key "+
			"rather than on the reserved ones; got: %s", msg)
	}
	if !errors.Is(err, ErrManifestReservedPrefix()) {
		t.Errorf("the joined refusal should still match the sentinel by identity; got chain: %v", err)
	}
}

// reservedPrefixManifestYAML builds a Manifest declaring one annotation binding
// per key, in the order given, so a test fixing the offending set writes the keys
// and nothing else.
//
// The author order is carried into the registry and out again as the order the
// rule is invoked in, so the order written here is the order findings appear in.
func reservedPrefixManifestYAML(keys ...string) []byte {
	var b strings.Builder
	b.WriteString(`apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test-manifest
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations:
`)
	for _, key := range keys {
		b.WriteString("        " + key + `:
          feature: core/forbidden:1.0.0
          maps:
            infra:
              - target
`)
	}
	return []byte(b.String())
}

func TestLoadManifest_AcceptsNamespacedPrefix(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "manifest.yaml")
	yaml := `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test-manifest
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations:
        feature.aggrega.ai/standard-library-binding:
          feature: core/foo:1.0.0
          maps:
            infra:
              - field_a
        feature.myco/enterprise-binding:
          feature: core/bar:1.0.0
          maps:
            values:
              - field_b
`
	os.WriteFile(f, []byte(yaml), 0644)

	_, err := LoadManifest(f)
	if err != nil {
		t.Fatalf("namespaced prefixes should be accepted; got error: %v", err)
	}
}

func TestLoadManifest_RejectsCrossBindingPillarCollision(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "manifest.yaml")
	yaml := `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test-manifest
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations:
        feature.myco/first-binding:
          feature: core/shared:1.0.0
          maps:
            infra:
              - pool_size
        feature.myco/second-binding:
          feature: core/shared:1.0.0
          maps:
            infra:
              - pool_size
`
	os.WriteFile(f, []byte(yaml), 0644)

	_, err := LoadManifest(f)
	if err == nil {
		t.Fatal("expected CATALOG_ERROR on cross-binding (path, pillar) collision; got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "CATALOG_ERROR") {
		t.Errorf("error missing CATALOG_ERROR prefix: %s", msg)
	}
	if !strings.Contains(msg, "first-binding") || !strings.Contains(msg, "second-binding") {
		t.Errorf("error should name both colliding bindings; got: %s", msg)
	}
	if !strings.Contains(msg, "core/shared:1.0.0") {
		t.Errorf("error should name the target Feature; got: %s", msg)
	}
	if !strings.Contains(msg, "pool_size") {
		t.Errorf("error should name the colliding path; got: %s", msg)
	}
	if !errors.Is(err, ErrManifestCrossBindingPillarCollision()) {
		t.Errorf("error should wrap ErrManifestCrossBindingPillarCollision; got chain: %v", err)
	}
}

func TestLoadManifest_RejectsCrossBindingCollision_NestedPath(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "manifest.yaml")
	yaml := `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test-manifest
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations:
        feature.myco/first:
          feature: core/shared:1.0.0
          maps:
            values:
              - ingress.host
        feature.myco/second:
          feature: core/shared:1.0.0
          maps:
            values:
              - ingress.host
`
	os.WriteFile(f, []byte(yaml), 0644)

	_, err := LoadManifest(f)
	if err == nil {
		t.Fatal("expected CATALOG_ERROR on nested-path collision; got nil")
	}
	if !strings.Contains(err.Error(), "ingress.host") {
		t.Errorf("error should name the colliding nested path; got: %v", err)
	}
}

func TestLoadManifest_AcceptsCrossBindingDifferentSubkeyOrPillar(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "manifest.yaml")
	// Two bindings targeting the same Feature with different (path, pillar)
	// pairs are legitimate — distinct routing slots compose without collision.
	yaml := `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: test-manifest
  version: "1.0.0"
spec:
  mappings:
    features:
      annotations:
        feature.myco/dev-binding:
          feature: core/shared:1.0.0
          maps:
            values:
              - db_name
        feature.myco/ops-binding:
          feature: core/shared:1.0.0
          maps:
            infra:
              - pool_size
`
	os.WriteFile(f, []byte(yaml), 0644)

	_, err := LoadManifest(f)
	if err != nil {
		t.Fatalf("distinct (path, pillar) pairs on the same Feature should be accepted; got error: %v", err)
	}
}
