// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/lintops"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestLintCatalogWide_CatchesBrokenEntity pins the catalog-wide validation
// home: `aggrega lint` with no entity argument loads every catalog entity and
// runs the schema-pillar (Pass-2) + library resolution/rejection checks, so a
// broken entity — here a Component whose schema carries an unresolvable
// cross-document $ref — fails loudly. This is the surface a broken catalog
// entity is meant to surface on (build, by contrast, only validates a Release's
// closure).
func TestLintCatalogWide_CatchesBrokenEntity(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      storage:
        $ref: "Nonexistent#/schemas/Storage"
`)
	if err := validateCatalogWide("", "", []string{dir}, nil, nil, os.Stderr, os.Stdout); err == nil {
		t.Fatal("expected catalog validation to fail on the unresolvable cross-document $ref; got nil")
	}
}

// TestLintCatalogWide_PassesValidCatalog is the non-vacuity companion: the same
// shape WITHOUT the broken reference validates clean — so the failure above is
// the unresolved $ref, not an unconditional error.
func TestLintCatalogWide_PassesValidCatalog(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, filepath.Join(dir, "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`)
	if err := validateCatalogWide("", "", []string{dir}, nil, nil, os.Stderr, os.Stdout); err != nil {
		t.Fatalf("valid catalog should pass catalog-wide validation; got %v", err)
	}
}

// unloadableGoodComponent is a Component both whole-catalog scopes accept.
const unloadableGoodComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/compute/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`

// unloadableSyntaxComponent declares the Aggrega envelope and does not parse as
// YAML: a mapping value opens where a scalar was expected.
const unloadableSyntaxComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/compute/broken
  version: "1.0.0"
spec:
  schema: values: [
`

// unloadableListedComponent declares the Aggrega envelope, parses, and does not
// decode into the entity header: its metadata is a list.
const unloadableListedComponent = `apiVersion: "aggrega.ai/v1alpha1"
kind: Component
metadata:
  - name: core/compute/listed
spec: {}
`

// unloadableChartTemplate is a Helm chart template as a FeatureRecipe's chart
// carries it: it declares a Kubernetes apiVersion and does not parse as YAML
// until it is rendered, so neither whole-catalog scope is to read it as an
// Aggrega document.
const unloadableChartTemplate = `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: {{ .Values.targetName }}-ingress
  {{- with .Values.namespace }}
  namespace: {{ . }}
  {{- end }}
spec:
  rules:
    - host: {{ .Values.host }}
`

// unloadableWorkspace is a workspace whose conventional catalog directory holds
// two entities both scopes accept and a chart template, plus the documents in
// extra, each keyed by its path under the catalog directory.
func unloadableWorkspace(t *testing.T, extra map[string]string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	docs := map[string]string{
		"catalog/feature/secret-value.yaml":                   perReleaseFeature(""),
		"catalog/component/server.yaml":                       unloadableGoodComponent,
		"catalog/feature-recipe/chart/templates/ingress.yaml": unloadableChartTemplate,
	}
	for rel, body := range extra {
		docs["catalog/"+rel] = body
	}
	return perReleaseCatalogWorkspace(t, "workspace: {}\n", nil, docs)
}

// wholeCatalogScope is one whole-catalog scope's core, run over a workspace.
type wholeCatalogScope struct {
	name string
	run  func(out, diag *bytes.Buffer) error
}

// wholeCatalogScopes is each whole-catalog scope's core over rw, writing what it
// says for the reader to out and its diagnostics to diag.
func wholeCatalogScopes(t *testing.T, rw *workspaceconfig.ResolvedWorkspace) []wholeCatalogScope {
	passes := passesFor(t, rw, nil)
	return []wholeCatalogScope{
		{"aggrega validate (whole catalog)", func(out, diag *bytes.Buffer) error {
			return validateCatalogWide(rw.Root(), rw.Config().ResolvedArtifactCacheDir(), passes[0].Dirs, passes[0].Named, nil, diag, out)
		}},
		{"aggrega lint (whole catalog)", func(out, diag *bytes.Buffer) error {
			return lintops.RunCatalogWide(context.Background(), rw, passes, nil, out, diag, nil)
		}},
	}
}

// runGuarded runs s and returns what it wrote and its verdict, failing the test
// rather than the process when s panics.
func (s wholeCatalogScope) runGuarded(t *testing.T) (out, diag string, err error) {
	t.Helper()
	var o, d bytes.Buffer
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s panicked: %v", s.name, r)
		}
	}()
	err = s.run(&o, &d)
	return o.String(), d.String(), err
}

// TestWholeCatalogScopesHaltOnEveryAggregaDocumentTheyCannotLoad plants two
// Aggrega documents the catalog cannot load — one whose YAML does not parse and
// one whose metadata is a list — beside two good entities and a chart template,
// and holds both whole-catalog scopes to the halt: the document-load condition,
// every planted file named with the parser's own message, and no summary
// claiming the catalog was checked. The chart template is in the same catalog,
// so the halt names exactly the two planted files and never it.
func TestWholeCatalogScopesHaltOnEveryAggregaDocumentTheyCannotLoad(t *testing.T) {
	t.Parallel()
	rw := unloadableWorkspace(t, map[string]string{
		"component/broken.yaml": unloadableSyntaxComponent,
		"component/listed.yaml": unloadableListedComponent,
	})
	for _, scope := range wholeCatalogScopes(t, rw) {
		name := scope.name
		out, diag, err := scope.runGuarded(t)
		if err == nil {
			t.Errorf("%s passed a catalog holding two Aggrega documents it cannot load\nout:\n%s\ndiag:\n%s", name, out, diag)
			continue
		}
		if !errors.Is(err, resolver.ErrYAMLLoad()) {
			t.Errorf("%s halted on another condition: %v", name, err)
		}
		chain := cerrors.CauseChain(err)
		for _, want := range []string{
			filepath.Join("component", "broken.yaml"),
			"mapping values are not allowed",
			filepath.Join("component", "listed.yaml"),
			"a list is written where a map is expected",
		} {
			if !strings.Contains(chain, want) {
				t.Errorf("%s halted without naming %q:\n%s", name, want, chain)
			}
		}
		if strings.Contains(chain, "ingress.yaml") {
			t.Errorf("%s named the chart template, which is not an Aggrega document:\n%s", name, chain)
		}
		for _, summary := range []string{"conventions checked:", "catalog validation passed"} {
			if strings.Contains(out, summary) {
				t.Errorf("%s printed %q over a catalog it halted on:\n%s", name, summary, out)
			}
		}
	}
}

// TestWholeCatalogScopesPassOverAChartTemplate is the control: the same catalog
// without the planted documents, its chart template still in place, passes both
// scopes, and lint counts the two entities it checked.
func TestWholeCatalogScopesPassOverAChartTemplate(t *testing.T) {
	t.Parallel()
	rw := unloadableWorkspace(t, nil)
	for _, scope := range wholeCatalogScopes(t, rw) {
		name := scope.name
		out, diag, err := scope.runGuarded(t)
		if err != nil {
			t.Errorf("%s refused a catalog whose only unparsable YAML is a chart template: %v\ndiag:\n%s", name, err, diag)
			continue
		}
		if strings.HasPrefix(name, "aggrega lint") && !strings.Contains(out, "conventions checked: 2 entities") {
			t.Errorf("%s did not count the two entities:\n%s", name, out)
		}
	}
}

// TestWholeCatalogLintAndValidateThroughTheCommandTreeHaltOnAnUnloadableDocument
// drives `aggrega lint` and `aggrega validate` with no argument through the real
// command tree over the planted catalog and reads the process status and what
// reached the terminal.
func TestWholeCatalogLintAndValidateThroughTheCommandTreeHaltOnAnUnloadableDocument(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	rw := unloadableWorkspace(t, map[string]string{
		"component/broken.yaml": unloadableSyntaxComponent,
		"component/listed.yaml": unloadableListedComponent,
	})
	code, _ := cerrors.CodeOf(resolver.ErrYAMLLoad())
	for _, verb := range []string{"lint", "validate"} {
		var out bytes.Buffer
		var status int
		var panicked any
		stderr := captureStderr(t, func() {
			// Recovered here and reported below, so a panic cannot leave the
			// process's standard error redirected into a closed pipe.
			defer func() { panicked = recover() }()
			root := newRootCmd()
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetIn(strings.NewReader(""))
			root.SetArgs([]string{"--workspace-root", rw.Root(), verb})
			status = executeRoot(context.Background(), root)
		})
		if panicked != nil {
			t.Fatalf("`aggrega %s` panicked: %v", verb, panicked)
		}
		if status == 0 {
			t.Errorf("`aggrega %s` exited 0 over a catalog holding two Aggrega documents it cannot load\nstdout:\n%s\nstderr:\n%s", verb, out.String(), stderr)
			continue
		}
		for _, want := range []string{"broken.yaml", "listed.yaml"} {
			if !strings.Contains(stderr, want) {
				t.Errorf("`aggrega %s` did not name %s:\n%s", verb, want, stderr)
			}
		}
		if short, ok := cerrors.ShortCodeOf(resolver.ErrYAMLLoad()); !ok || !strings.Contains(stderr, short) {
			t.Errorf("`aggrega %s` did not lead with the %s condition's short code:\n%s", verb, code, stderr)
		}
		if strings.Contains(out.String(), "conventions checked:") {
			t.Errorf("`aggrega %s` printed a summary over a catalog it halted on:\n%s", verb, out.String())
		}
	}
}

// TestReleaseLintHaltsOnAnAggregaDocumentItCannotLoad holds the release-scoped
// lint, which walks the catalog directories the same way, to the same halt: a
// Release whose closure resolves, over a catalog stack one of whose directories
// holds an Aggrega document that does not parse, is refused naming that file
// rather than linted around it.
func TestReleaseLintHaltsOnAnAggregaDocumentItCannotLoad(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), cliBackendAggregate)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), cliCleanManifest)
	releasePath := filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, cliDefaultRelease)
	planted := t.TempDir()
	writeFixture(t, filepath.Join(planted, "broken.yaml"), unloadableSyntaxComponent)
	writeFixture(t, filepath.Join(planted, "chart", "templates", "ingress.yaml"), unloadableChartTemplate)

	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	run := func(dirs []string) (string, error) {
		var out, diag bytes.Buffer
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("lintops.RunRelease panicked: %v", r)
			}
		}()
		err := lintops.RunRelease(t.Context(), rw, releasePath, dirs, nil, &out, &diag, nil)
		return out.String(), err
	}

	// The positive control: the chart template alone beside the catalog the
	// Release reads leaves the pass to run.
	chartOnly := t.TempDir()
	writeFixture(t, filepath.Join(chartOnly, "chart", "templates", "ingress.yaml"), unloadableChartTemplate)
	if out, err := run([]string{chartOnly, cliFullstackCatalogDir(t)}); err != nil || !strings.Contains(out, "conventions checked:") {
		t.Fatalf("the release-scoped lint did not run beside a chart template: %v\n%s", err, out)
	}

	out, err := run([]string{planted, cliFullstackCatalogDir(t)})
	if err == nil {
		t.Fatalf("the release-scoped lint passed over an Aggrega document it cannot load:\n%s", out)
	}
	if !errors.Is(err, resolver.ErrYAMLLoad()) {
		t.Errorf("the release-scoped lint halted on another condition: %v", err)
	}
	if chain := cerrors.CauseChain(err); !strings.Contains(chain, filepath.Join(planted, "broken.yaml")) || strings.Contains(chain, "ingress.yaml") {
		t.Errorf("the halt does not name exactly the planted document:\n%s", chain)
	}
	if strings.Contains(out, "conventions checked:") {
		t.Errorf("the release-scoped lint printed a summary over a walk it halted on:\n%s", out)
	}
}
