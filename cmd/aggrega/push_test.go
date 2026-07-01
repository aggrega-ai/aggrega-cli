// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

func writeEntityFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// TestPushBareYAMLEntity pins per-kind round-trip for the three
// bare-YAML kinds: Component, Feature, Provider. Each kind bundles
// to bare YAML bytes under its canonical mediatype and round-trips
// through Push + Pull against an in-memory CAS.
func TestPushBareYAMLEntity(t *testing.T) {
	cases := []struct {
		name      string
		kind      string
		mediaType string
		body      string
	}{
		{
			name:      "Component",
			kind:      "Component",
			mediaType: oci.MediaTypeComponent,
			body: `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/api-server
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      type: object
`,
		},
		{
			name:      "Feature",
			kind:      "Feature",
			mediaType: oci.MediaTypeFeature,
			body: `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: core/ingress
  version: 1.0.0
spec:
  schema:
    values:
      type: object
`,
		},
		{
			name:      "Provider",
			kind:      "Provider",
			mediaType: oci.MediaTypeProvider,
			body: `apiVersion: aggrega.ai/v1alpha1
kind: Provider
metadata:
  name: helm
  version: 1.0.0
spec:
  plugin: "builtin:helm"
`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			entityPath := filepath.Join(root, "1.0.0", "entity.yaml")
			writeEntityFixture(t, entityPath, tc.body)

			store := memory.New()
			if err := pushRun(pushConfig{
				EntityPath: entityPath,
				Registry:   "example.com",
				PushTarget: store,
			}); err != nil {
				t.Fatalf("pushRun: %v", err)
			}

			pulled, err := oci.Pull(context.Background(), store, "1.0.0")
			if err != nil {
				t.Fatalf("oci.Pull: %v", err)
			}
			if pulled.MediaType != tc.mediaType {
				t.Errorf("MediaType: got %q; want %q", pulled.MediaType, tc.mediaType)
			}
			if string(pulled.Bytes) != tc.body {
				t.Errorf("round-trip bytes mismatch")
			}
		})
	}
}

// TestPushSchemaLibraryHonoursPerVersionSubdir pins that a library
// authored at the canonical per-version layout (<kind>/<namespace>/
// <name>/<version>/library.yaml) with matching metadata.version
// passes the per-version subdir gate inside pkg/oci.Bundle.
func TestPushSchemaLibraryHonoursPerVersionSubdir(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	libPath := filepath.Join(root, "schemalibrary", "core", "Probes", "1.0.0", "library.yaml")
	writeEntityFixture(t, libPath, `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: Probes
  version: 1.0.0
spec:
  schemas:
    LivenessProbe:
      type: object
`)
	store := memory.New()
	if err := pushRun(pushConfig{
		EntityPath: libPath,
		Registry:   "example.com",
		PushTarget: store,
	}); err != nil {
		t.Fatalf("pushRun: %v", err)
	}
	pulled, err := oci.Pull(context.Background(), store, "1.0.0")
	if err != nil {
		t.Fatalf("oci.Pull: %v", err)
	}
	if pulled.MediaType != oci.MediaTypeSchemaLibrary {
		t.Errorf("MediaType: got %q; want %q", pulled.MediaType, oci.MediaTypeSchemaLibrary)
	}
}

// TestPushSchemaLibraryRejectsPathVersionMismatch pins that a
// library whose metadata.version disagrees with its per-version
// subdirectory halts with the canonical mismatch sentinel before
// any network call.
func TestPushSchemaLibraryRejectsPathVersionMismatch(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	libPath := filepath.Join(root, "schemalibrary", "core", "Probes", "1.0.0", "library.yaml")
	writeEntityFixture(t, libPath, `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: Probes
  version: 2.0.0
spec:
  schemas:
    LivenessProbe:
      type: object
`)
	err := pushRun(pushConfig{
		EntityPath: libPath,
		Registry:   "example.com",
		PushTarget: memory.New(),
	})
	if err == nil {
		t.Fatal("expected ErrEntityVersionPathMismatch; got nil")
	}
	if !errors.Is(err, oci.ErrEntityVersionPathMismatch) {
		t.Errorf("error should wrap ErrEntityVersionPathMismatch; got %v", err)
	}
}

// TestPushRecipeBundlesCoLocatedTreeAsTarGz pins the acceptance
// criterion 4: a Recipe directory's co-located content lands in the
// published .tar.gz under the canonical mediatype.
//
// Two on-disk co-location forms are covered explicitly:
//
//   - templates/<file>.yaml.tmpl — colocated source files alongside
//     the Recipe YAML, picked up by the bundler's recursive directory
//     walk.
//   - partials/header.yaml — a file referenced by a $import: field in
//     the Recipe YAML at a non-templates path. Pinned explicitly so
//     a future bundler change that accidentally restricted the walk
//     to a known-subtrees allowlist would surface here.
//
// Inline artifacts: content (the third form enumerated in criterion
// 4) flows through naturally as Recipe YAML content; the YAML
// round-trip in pulled.Bytes covers it without a separate test
// fixture.
func TestPushRecipeBundlesCoLocatedTreeAsTarGz(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	recipeDir := filepath.Join(root, "recipes", "server", "1.0.0")
	writeEntityFixture(t, filepath.Join(recipeDir, "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server
  version: 1.0.0
spec:
  provider: helm
  implements: core/server
  artifacts:
    header:
      $import: partials/header.yaml
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
`)
	writeEntityFixture(t, filepath.Join(recipeDir, "templates", "deployment.yaml.tmpl"), "kind: Deployment\n")
	writeEntityFixture(t, filepath.Join(recipeDir, "partials", "header.yaml"), "# header partial\n")

	store := memory.New()
	if err := pushRun(pushConfig{
		EntityPath: filepath.Join(recipeDir, "server.yaml"),
		Registry:   "example.com",
		PushTarget: store,
	}); err != nil {
		t.Fatalf("pushRun: %v", err)
	}

	pulled, err := oci.Pull(context.Background(), store, "1.0.0")
	if err != nil {
		t.Fatalf("oci.Pull: %v", err)
	}
	if pulled.MediaType != oci.MediaTypeRecipe {
		t.Errorf("MediaType: got %q; want %q", pulled.MediaType, oci.MediaTypeRecipe)
	}

	names := tarEntryNames(t, pulled.Bytes)
	wantEntries := []string{"server.yaml", "templates/deployment.yaml.tmpl", "partials/header.yaml"}
	for _, want := range wantEntries {
		if !containsString(names, want) {
			t.Errorf("tar archive missing %q; got entries: %v", want, names)
		}
	}
}

// TestPushRecipePreservesRemoteSourceURL pins the acceptance
// criterion: a Recipe's remote `source:` URL is published as-is
// in the YAML; the chart bytes are NOT pulled-then-bundled.
func TestPushRecipePreservesRemoteSourceURL(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	recipeDir := filepath.Join(root, "recipes", "mariadb", "1.0.0")
	recipeBody := `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: mariadb
  version: 1.0.0
spec:
  provider: helm-chart
  implements: stateful/mariadb
  source: oci://registry-1.docker.io/bitnamicharts/mariadb:25.0.8
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
`
	writeEntityFixture(t, filepath.Join(recipeDir, "mariadb.yaml"), recipeBody)

	store := memory.New()
	if err := pushRun(pushConfig{
		EntityPath: filepath.Join(recipeDir, "mariadb.yaml"),
		Registry:   "example.com",
		PushTarget: store,
	}); err != nil {
		t.Fatalf("pushRun: %v", err)
	}
	pulled, err := oci.Pull(context.Background(), store, "1.0.0")
	if err != nil {
		t.Fatalf("oci.Pull: %v", err)
	}
	entries := tarEntries(t, pulled.Bytes)
	yamlBytes, ok := entries["mariadb.yaml"]
	if !ok {
		t.Fatalf("tar archive missing mariadb.yaml")
	}
	if !strings.Contains(string(yamlBytes), "oci://registry-1.docker.io/bitnamicharts/mariadb:25.0.8") {
		t.Errorf("source URL not preserved verbatim; got:\n%s", yamlBytes)
	}
	// Defensive: ensure no random `.tgz` artifact crept in via a
	// hypothetical publish-time pull-then-bundle.
	for name := range entries {
		if strings.HasSuffix(name, ".tgz") {
			t.Errorf("unexpected .tgz entry %q in published bundle; chart bytes must not be pulled at publish time", name)
		}
	}
}

// TestPushAcceptsAggregate confirms an Aggregate publishes via the generic
// catalog-kind path (no special-case): pushRun bundles it under the Aggregate
// mediatype and pushes without error.
func TestPushAcceptsAggregate(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	entityPath := filepath.Join(root, "1.0.0", "aggregate.yaml")
	writeEntityFixture(t, entityPath, `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: smoke
  version: 1.0.0
`)
	if err := pushRun(pushConfig{
		EntityPath: entityPath,
		Registry:   "example.com",
		PushTarget: memory.New(),
	}); err != nil {
		t.Fatalf("Aggregate should publish via the generic catalog-kind path; got %v", err)
	}
}

// TestPushRejectsDeploymentSideKinds: an Aggregate is publishable, but a
// Release and a Manifest are deployment-side and rejected with a wrapped
// ErrUnknownEntityKind.
func TestPushRejectsDeploymentSideKinds(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{name: "Release", body: `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: smoke-rel
  version: 1.0.0
  environment: testing
spec:
  aggregate: smoke
  manifest: smoke
`},
		{name: "Manifest", body: `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: smoke-manifest
  version: 1.0.0
`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Chdir(root)
			entityPath := filepath.Join(root, "entity.yaml")
			writeEntityFixture(t, entityPath, tc.body)

			err := pushRun(pushConfig{
				EntityPath: entityPath,
				Registry:   "example.com",
				PushTarget: memory.New(),
			})
			if err == nil {
				t.Fatalf("expected ErrUnknownEntityKind for %s; got nil", tc.name)
			}
			if !errors.Is(err, oci.ErrUnknownEntityKind) {
				t.Errorf("error should wrap ErrUnknownEntityKind; got %v", err)
			}
		})
	}
}

// TestPushDryRunSkipsNetworkCall pins that --dry-run computes the
// bundle and prints the proposed reference without invoking Push.
// PushTarget is left nil; if dry-run were broken the production
// path's NewRemoteRepository call would fail (or any direct Push
// call against a nil target would panic).
func TestPushDryRunSkipsNetworkCall(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	entityPath := filepath.Join(root, "1.0.0", "entity.yaml")
	writeEntityFixture(t, entityPath, `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/api-server
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      type: object
`)
	if err := pushRun(pushConfig{
		EntityPath: entityPath,
		Registry:   "example.com",
		DryRun:     true,
		PushTarget: nil,
	}); err != nil {
		t.Fatalf("pushRun --dry-run: %v", err)
	}
}

// TestPushUsesWorkspaceDefaultRegistry pins that the destination
// registry resolves from (in priority order): --registry flag, then
// workspace.default_registry. Uses --dry-run mode so pushRun prints
// the resolved <reference>:<tag> without contacting any registry,
// and captures stdout to assert the printed reference's registry
// host matches the expected resolution.
//
// Stronger pinning than asserting "both invocations succeed": the
// in-memory oras.Target accepts any reference, so a regression that
// quietly used the wrong registry host would still produce a
// successful Push call. Inspecting the printed reference directly
// pins the registry-resolution arm at unit-test time.
func TestPushUsesWorkspaceDefaultRegistry(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	writeEntityFixture(t, filepath.Join(root, ".aggrega-config.yaml"), `
default_registry: workspace.example.com
`)
	entityPath := filepath.Join(root, "1.0.0", "entity.yaml")
	writeEntityFixture(t, entityPath, `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/api-server
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      type: object
`)

	// Without --registry, workspace.default_registry wins.
	var buf bytes.Buffer
	if err := pushRun(pushConfig{
		EntityPath: entityPath,
		DryRun:     true,
		Out:        &buf,
	}); err != nil {
		t.Fatalf("pushRun (default_registry): %v", err)
	}
	wantRef := "workspace.example.com/component/core/api-server:1.0.0"
	if !strings.Contains(buf.String(), wantRef) {
		t.Errorf("default_registry: stdout missing %q; got:\n%s", wantRef, buf.String())
	}

	// With --registry, the flag wins over workspace.default_registry.
	buf.Reset()
	if err := pushRun(pushConfig{
		EntityPath: entityPath,
		Registry:   "flag.example.io",
		DryRun:     true,
		Out:        &buf,
	}); err != nil {
		t.Fatalf("pushRun (flag override): %v", err)
	}
	wantRef = "flag.example.io/component/core/api-server:1.0.0"
	if !strings.Contains(buf.String(), wantRef) {
		t.Errorf("flag override: stdout missing %q; got:\n%s", wantRef, buf.String())
	}
	// Defensive: when the flag wins, the workspace-default host must
	// NOT appear in the output. A regression that quietly fell back
	// to the workspace default after seeing the flag would surface here.
	if strings.Contains(buf.String(), "workspace.example.com") {
		t.Errorf("flag override: stdout unexpectedly contained workspace.example.com; flag should win:\n%s", buf.String())
	}
}

// TestPushReferenceRoundTripsThroughSplitURIForOCI pins the
// publish/consume reference-shape symmetry across all seven
// publishable catalog kinds. The push-side helper
// buildPushReference constructs a reference; appending ":<version>"
// yields a canonical entity URI; catalog.SplitURIForOCI parses it
// back into (reference, tag); the round-trip MUST be exact.
//
// The round-trip is only meaningful for publishable kinds. Non-
// publishable kinds halt at the bundle step BEFORE reference
// construction (see TestPushRejectsNonCatalogKinds); SplitURIForOCI
// itself is kindless and parses any URI shape, so it cannot reject
// non-publishable kinds at the parsing layer anyway.
//
// Any future divergence between buildPushReference and
// SplitURIForOCI surfaces here at unit-test time, not end-to-end at
// runtime when published artifacts no longer match what consumers
// request.
func TestPushReferenceRoundTripsThroughSplitURIForOCI(t *testing.T) {
	t.Parallel()
	cases := []struct {
		kind         string
		provider     string
		metadataName string
		version      string
	}{
		{"Component", "", "core/api-server", "1.0.0"},
		{"Feature", "", "core/ingress", "1.2.0"},
		{"Recipe", "helm", "server", "2.0.0"},
		{"FeatureRecipe", "helm", "ingress-nginx", "1.0.0"},
		{"Provider", "", "helm", "3.0.0"},
		{"SchemaLibrary", "", "Probes", "1.0.0"},
		{"MappingLibrary", "", "BaseMappings", "2.1.0"},
		// Plugin kind: path segment is the canonical "wasm" (not
		// "plugin"); plugin's metadata.name path-encodes the
		// socket. Reference shape: <registry>/wasm/<socket>/<name>.
		{"Plugin", "", "linter/aggrega-publishing", "1.0.0"},
	}
	registry := "hub.aggrega.ai"
	for _, tc := range cases {
		t.Run(tc.kind, func(t *testing.T) {
			t.Parallel()
			ref := buildPushReference(registry, tc.kind, tc.provider, tc.metadataName)
			uri := ref + ":" + tc.version
			gotRef, gotTag, ok := catalog.SplitURIForOCI(uri)
			if !ok {
				t.Fatalf("SplitURIForOCI(%q) returned ok=false", uri)
			}
			if gotRef != ref {
				t.Errorf("reference round-trip: got %q; want %q", gotRef, ref)
			}
			if gotTag != tc.version {
				t.Errorf("tag round-trip: got %q; want %q", gotTag, tc.version)
			}
		})
	}
}

// tarEntryNames returns the names of every entry in a gzipped tar
// archive. Helper for asserting which co-located files made it into
// a Recipe bundle.
func tarEntryNames(t *testing.T, archive []byte) []string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer gz.Close()
	var names []string
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next: %v", err)
		}
		names = append(names, hdr.Name)
	}
	return names
}

// tarEntries returns the full file-name → content map for every
// entry in a gzipped tar archive. Helper for tests that need to
// inspect entry contents (e.g., that a Recipe's source URL was
// preserved verbatim).
func tarEntries(t *testing.T, archive []byte) map[string][]byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer gz.Close()
	entries := map[string][]byte{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next: %v", err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read %q: %v", hdr.Name, err)
		}
		entries[hdr.Name] = data
	}
	return entries
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
