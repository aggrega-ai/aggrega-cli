// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bareNameComponentFixture is a minimal catalog Component. The schema is
// authored in object form because the catalog loader validates each entity
// against its envelope schema, which requires spec.schema to be an object.
const bareNameComponentFixture = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/api-server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`

// bareNameFeatureFixture is a minimal catalog Feature in object-schema form,
// for the same envelope-validation reason as the Component above.
const bareNameFeatureFixture = `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: core/metrics
  version: "1.0.0"
  labels:
    feature.aggrega.ai/category: observability
spec:
  schema:
    infra:
      endpoint: { type: string }
`

// writeBareNameCatalog writes each named YAML body into a fresh catalog dir and
// returns the dir path, for driving a bare-identifier dispatchInspect against it.
func writeBareNameCatalog(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir
}

// TestInspectBareName_Plugin proves a bare (non-path, non-oci) identifier that
// names a Plugin resolves across kinds and renders through the Plugin inspector
// rather than a Component-only lookup. The plugin fixture's coordinate is
// `<pluginType>/<name>` = provider/helm-renderer.
func TestInspectBareName_Plugin(t *testing.T) {
	t.Parallel()
	catDir := writeBareNameCatalog(t, map[string]string{"plugin.yaml": pluginFixture})

	var buf bytes.Buffer
	if err := dispatchInspect(resolveTempWorkspace(t, tempWorkspaceOptions{}), "provider/helm-renderer", []string{catDir}, testInspectOptions(&buf)); err != nil {
		t.Fatalf("bare plugin coordinate should inspect cleanly; got: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Plugin: helm-renderer:1.0.0") {
		t.Errorf("output missing Plugin header naming the resolved entity; got:\n%s", out)
	}
	if !strings.Contains(out, "Plugin type: provider") {
		t.Errorf("output missing Plugin-specific type line; got:\n%s", out)
	}
}

// TestInspectBareName_Component_Preserved pins that a bare Component name still
// resolves and renders exactly as before the cross-kind extension: the
// structured header (the identifier as passed) plus the schema body.
func TestInspectBareName_Component_Preserved(t *testing.T) {
	t.Parallel()
	catDir := writeBareNameCatalog(t, map[string]string{"component.yaml": bareNameComponentFixture})

	var buf bytes.Buffer
	if err := dispatchInspect(resolveTempWorkspace(t, tempWorkspaceOptions{}), "core/api-server", []string{catDir}, testInspectOptions(&buf)); err != nil {
		t.Fatalf("bare Component name should inspect cleanly; got: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Component: core/api-server") {
		t.Errorf("output missing Component header; got:\n%s", out)
	}
	if !strings.Contains(out, "port") {
		t.Errorf("output missing rendered schema body; got:\n%s", out)
	}
}

// TestInspectBareName_Feature proves the general cross-kind path resolves a
// Feature by identity and renders it through the Feature inspector.
func TestInspectBareName_Feature(t *testing.T) {
	t.Parallel()
	catDir := writeBareNameCatalog(t, map[string]string{"feature.yaml": bareNameFeatureFixture})

	var buf bytes.Buffer
	if err := dispatchInspect(resolveTempWorkspace(t, tempWorkspaceOptions{}), "core/metrics", []string{catDir}, testInspectOptions(&buf)); err != nil {
		t.Fatalf("bare Feature name should inspect cleanly; got: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Feature: core/metrics:1.0.0") {
		t.Errorf("output missing Feature header; got:\n%s", out)
	}
	if !strings.Contains(out, "endpoint") {
		t.Errorf("output missing rendered Feature schema body; got:\n%s", out)
	}
}

// bareNameAggregateFixture is a minimal catalog Aggregate whose single component
// names the Component fixture above, so the topology pass its inspector runs has
// a resolvable reference.
const bareNameAggregateFixture = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: storefront
  version: "1.0.0"
spec:
  components:
    api:
      type: core/api-server
`

// TestInspectBareName_Aggregate_ResolvesAwayFromTheWorkspaceRoot pins that a bare
// Aggregate name resolves wherever the command is run from.
//
// The Aggregate branch is the one kind whose inspector reads a filesystem path
// rather than the bytes it was handed, and the catalog records that path relative
// to the workspace root — a machine-stable provenance string, not a locatable
// path. Read as though it were locatable it resolves against the process working
// directory, so it finds the file only when that directory IS the workspace root.
//
// The test process runs in the package directory while the workspace is a
// temporary one elsewhere, so simply resolving the name here is the off-root case.
// That is deliberate: it needs no process-wide directory change, which would reach
// every test running concurrently.
func TestInspectBareName_Aggregate_ResolvesAwayFromTheWorkspaceRoot(t *testing.T) {
	t.Parallel()
	catDir := writeBareNameCatalog(t, map[string]string{
		"component.yaml": bareNameComponentFixture,
		"aggregate.yaml": bareNameAggregateFixture,
	})

	var buf bytes.Buffer
	if err := dispatchInspect(resolveTempWorkspace(t, tempWorkspaceOptions{}), "storefront", []string{catDir}, testInspectOptions(&buf)); err != nil {
		t.Fatalf("a bare Aggregate name should resolve from outside the workspace root; got: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Aggregate: storefront:1.0.0") {
		t.Errorf("output missing the Aggregate header; got:\n%s", out)
	}
	if !strings.Contains(out, "core/api-server") {
		t.Errorf("output missing the resolved component; got:\n%s", out)
	}
}

// TestInspectBareName_Unresolvable pins the no-match failure mode: against a
// catalog that loads cleanly, an identifier that names no catalog entity of any
// kind returns a clear error naming the identifier — not a Component-only
// "component not found" message.
func TestInspectBareName_Unresolvable(t *testing.T) {
	t.Parallel()
	catDir := writeBareNameCatalog(t, map[string]string{"component.yaml": bareNameComponentFixture})

	err := dispatchInspect(resolveTempWorkspace(t, tempWorkspaceOptions{}), "core/does-not-exist", []string{catDir}, testInspectOptions(&bytes.Buffer{}))
	if err == nil {
		t.Fatal("expected not-found error for an unresolvable bare name; got nil")
	}
	if !strings.Contains(err.Error(), "core/does-not-exist") {
		t.Errorf("error should name the identifier; got: %v", err)
	}
	if !strings.Contains(err.Error(), "no catalog entity of any kind") {
		t.Errorf("error should state no entity of any kind matched (not a Component-only message); got: %v", err)
	}
}
