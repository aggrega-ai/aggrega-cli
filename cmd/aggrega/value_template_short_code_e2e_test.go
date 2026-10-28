// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// valueTemplateWorkspace writes a Release, the Aggregate and Manifest it names, and
// a catalog whose one Recipe carries the locals, provides and workload image
// expression given, so a test can make exactly one of those value templates fail
// while it runs. locals and provides are YAML lines indented under the Recipe's
// spec, or "" for none. It returns the catalog directory and the Release's path.
func valueTemplateWorkspace(t *testing.T, locals, provides, image string) (catalogRoot, releasePath string) {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: value-template-failure
  version: "1.0.0"
spec:
  components:
    only:
      type: core/server
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: value-template-failure-release
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        only:
          infra:
            replicas: 1
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: value-template-failure-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/server: server-recipe:1.0.0
`)
	catalogRoot = filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogRoot, "base", "components", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    infra:
      replicas: { type: integer, default: 1 }
`)
	writeHelmRendererPlugin(t, filepath.Join(catalogRoot, "base", "providers"))
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "helm", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/server
`+locals+provides+`  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: {{ .infra.replicas }}
    containers:
      server:
        image: "`+image+`"
        ports:
          web: 8080
    services:
      svc:
        type: ClusterIP
        ports:
          web: 8080
`)
	return catalogRoot, filepath.Join(root, "release.yaml")
}

// TestBuild_AValueTemplateFailingWhileItRunsCarriesItsConditionsShortCode builds,
// through the real pull and build entry points, a Release whose Recipe carries one
// value template that fails while it runs, and reads the diagnostic a user reads.
//
// A workload template and a locals template each halt naming the value-template
// condition: its identity is on the chain, the printed head carries its short code,
// and the engine's own reason is still printed beneath it, so the failure can be
// looked up without losing what went wrong. A provides template halts under the
// provides condition as before, its head carrying that condition's code rather
// than this one's. A workload that renders builds, the control showing the fixture
// halts only on the failing template.
//
// Codes are read off the conditions' accessors, never written out, so a renumbered
// condition cannot leave this asserting a stale code.
func TestBuild_AValueTemplateFailingWhileItRunsCarriesItsConditionsShortCode(t *testing.T) {
	failed, ok := cerrors.ShortCodeOf(renderer.ErrValueTemplateExecutionFailed())
	if !ok || failed == "" {
		t.Fatal("the value-template condition reports no short code")
	}
	provides, ok := cerrors.ShortCodeOf(renderer.ErrProvidesEvaluation())
	if !ok || provides == "" {
		t.Fatal("the provides condition reports no short code")
	}
	const absentRead = "nil pointer evaluating interface {}.tag"
	const fine = "registry.example/server:1.0"

	rows := []struct {
		name     string
		locals   string
		provides string
		image    string
		// halts is false for the control, which builds.
		halts     bool
		condition error
		code      string
		reason    string
	}{
		{
			name:      "a workload reading a field through an absent map",
			image:     "{{ .values.nosuch.tag }}",
			halts:     true,
			condition: renderer.ErrValueTemplateExecutionFailed(),
			code:      failed,
			reason:    absentRead,
		},
		{
			name:      "a workload comparing values of two kinds",
			image:     "{{ if eq .context.instance_id 1 }}x{{ end }}" + fine,
			halts:     true,
			condition: renderer.ErrValueTemplateExecutionFailed(),
			code:      failed,
			reason:    "incompatible types for comparison",
		},
		{
			name:      "a locals entry reading a field through an absent map",
			locals:    "  locals:\n    tag: \"{{ .values.nosuch.tag }}\"\n",
			image:     fine,
			halts:     true,
			condition: renderer.ErrValueTemplateExecutionFailed(),
			code:      failed,
			reason:    absentRead,
		},
		{
			name:      "a provides entry reading a field through an absent map keeps the provides condition",
			provides:  "  provides:\n    tag: \"{{ .values.nosuch.tag }}\"\n",
			image:     fine,
			halts:     true,
			condition: renderer.ErrProvidesEvaluation(),
			code:      provides,
			reason:    absentRead,
		},
		{
			name:  "a workload that renders builds",
			image: fine,
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			catalogRoot, releasePath := valueTemplateWorkspace(t, row.locals, row.provides, row.image)
			ws := resolveTempWorkspace(t, tempWorkspaceOptions{})
			if err := pullRun(context.Background(), pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogRoot}, ws: ws}); err != nil {
				t.Fatalf("prime lockfile via pull: %v", err)
			}
			var diag, out bytes.Buffer
			err := dispatchBuild(context.Background(), ws, releasePath, []string{catalogRoot}, false, &diag, &out)
			if !row.halts {
				if err != nil {
					t.Fatalf("the control failed to build:\n%s", formatCompilerError(err))
				}
				return
			}
			if err == nil {
				t.Fatalf("the build returned nil; want it to halt on the failing template\ndiagnostics:\n%s", diag.String())
			}
			printed := formatCompilerError(err)
			if !errors.Is(err, row.condition) {
				t.Errorf("the build halted without the condition %v on its chain:\n%s", row.condition, printed)
			}
			if !strings.Contains(printed, "[CATALOG_ERROR "+row.code+"]") {
				t.Errorf("the printed head does not carry [CATALOG_ERROR %s]:\n%s", row.code, printed)
			}
			if !chainContains(err, row.reason) {
				t.Errorf("the build's error no longer carries the engine's reason %q:\n%s", row.reason, printed)
			}
		})
	}
}
