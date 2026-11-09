// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// importedSchemaPublisher is a publisher tree whose Component takes its whole
// contract from a file beside it, bound by a Recipe carrying a local chart.
var importedSchemaPublisher = map[string]string{
	"component/acme/compute/imported/1.0.0/component.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: acme/compute/imported
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
    component.aggrega.ai/category: compute
spec:
  schema:
    $import: ./schema.yaml
`,
	"component/acme/compute/imported/1.0.0/schema.yaml": `values:
  greeting: { type: string, default: hello }
  replicas: { type: integer, minimum: 1, default: 1 }
`,
	"recipe/helm/imported-local/1.0.0/recipe.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: imported-local
  version: 1.0.0
spec:
  capability: helm
  implements: acme/compute/imported:1.0.0
  source: ./chart
  valuesMapping: |
    {"greeting": "{{ .values.greeting }}", "replicas": {{ .values.replicas }}}
`,
	"recipe/helm/imported-local/1.0.0/chart/Chart.yaml": "apiVersion: v2\nname: imported\nversion: 0.1.0\n",
	"recipe/helm/imported-local/1.0.0/chart/templates/cm.yaml": `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Release.Name }}-greeting
data:
  greeting: {{ .Values.greeting | quote }}
  replicas: {{ .Values.replicas | quote }}
`,
}

// importedSchemaConsumer returns a consumer workspace's files, with the
// component's values block as given, reaching the publisher's entities by
// identity only.
func importedSchemaConsumer(values string) map[string]string {
	return map[string]string{
		"aggregate.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    front:
      type: acme/compute/imported:1.0.0
      values:
` + values,
		"manifest.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        acme/compute/imported:1.0.0: helm/imported-local:1.0.0
`,
		"release.yaml": `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: dev
  environment: development
  version: "1.0.0"
spec:
  aggregate: aggregate
  manifest: manifest
  components:
    front: {}
`,
		// A decoy at a path an import anchored at the consumer's working directory
		// could reach. It declares no replica count at all, so a contract read from
		// here would refuse the positive build's replicas as an undeclared key.
		"schema.yaml": "values:\n  greeting: { type: string, default: decoy }\n",
	}
}

// TestIntegration_PulledComponentValidatesAgainstTheSchemaItsArchiveCarries
// publishes a Component whose contract is imported from a file, and builds a
// Release that reaches it from the registry alone.
//
// The publication carries the schema file, so the Component publishes as an
// archive of its document and that file. The consumer's build then validates the
// component's values against the published contract: a value it admits builds and
// renders, and a value it refuses — a replica count below the declared minimum —
// halts the build with the schema-conformance condition. A consumer that could not
// read the file would fail both builds the same way, as an entity that does not
// resolve, so the two outcomes differing is what shows the contract was read; the
// decoy in the consumer's own directory, which declares no replica count, is what
// shows where it was read from.
func TestIntegration_PulledComponentValidatesAgainstTheSchemaItsArchiveCarries(t *testing.T) {
	host := startRegistry(t, nil)

	pub := t.TempDir()
	for rel, body := range importedSchemaPublisher {
		writeFixture(t, filepath.Join(pub, filepath.FromSlash(rel)), body)
	}
	publisher := resolveTempWorkspace(t, tempWorkspaceOptions{Root: pub})
	// Each publication pins first, as the publisher would: the Recipe reads a local
	// chart and is refused unpinned, and the Component has nothing to pin.
	for _, entity := range []string{
		"component/acme/compute/imported/1.0.0/component.yaml",
		"recipe/helm/imported-local/1.0.0/recipe.yaml",
	} {
		if err := pushRun(t.Context(), pushConfig{Workspace: publisher, EntityPath: filepath.Join(pub, entity), Registry: host, PinSources: boolPtr(true)}); err != nil {
			t.Fatalf("push %s: %v", entity, err)
		}
	}
	repo, err := oci.NewRemoteRepository(host+"/component/acme/compute/imported", &http.Client{})
	if err != nil {
		t.Fatalf("repository: %v", err)
	}
	published, err := oci.Pull(context.Background(), repo, "1.0.0")
	if err != nil {
		t.Fatalf("pull the published Component back: %v", err)
	}
	if want, _ := oci.MediaTypeFor("Component", true); published.MediaType != want {
		t.Fatalf("the Component published as %q; a Component carrying its schema file publishes as %q", published.MediaType, want)
	}

	build := func(t *testing.T, values string) (string, error) {
		t.Helper()
		ws := t.TempDir()
		for rel, body := range importedSchemaConsumer(values) {
			writeFixture(t, filepath.Join(ws, rel), body)
		}
		writeWorkspaceConfig(t, ws, "workspace:\n  catalog_dirs: []\ndefault_registry: \""+host+"\"\noffline: false")
		t.Chdir(ws)
		consumer := resolveTempWorkspace(t, tempWorkspaceOptions{Root: ws})
		if err := pullRun(context.Background(), pullConfig{ws: consumer, ReleasePath: "release.yaml"}); err != nil {
			return "", err
		}
		if err := dispatchBuild(context.Background(), consumer, "release.yaml", nil, false, os.Stderr, os.Stdout); err != nil {
			return "", err
		}
		var rendered strings.Builder
		if err := filepath.WalkDir(filepath.Join(ws, "dist"), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, rerr := os.ReadFile(path)
			rendered.Write(data)
			return rerr
		}); err != nil {
			t.Fatalf("read rendered output: %v", err)
		}
		return rendered.String(), nil
	}

	t.Run("a value the published contract admits builds", func(t *testing.T) {
		rendered, err := build(t, "        greeting: ciao\n        replicas: 2\n")
		if err != nil {
			t.Fatalf("a value the published contract admits was refused: %v", err)
		}
		for _, want := range []string{`greeting: "ciao"`, `replicas: "2"`} {
			if !strings.Contains(rendered, want) {
				t.Errorf("the rendered output does not carry %q:\n%s", want, rendered)
			}
		}
	})

	t.Run("a value the published contract refuses halts the build", func(t *testing.T) {
		_, err := build(t, "        greeting: ciao\n        replicas: 0\n")
		if !errors.Is(err, resolver.ErrSchemaConformance()) {
			t.Fatalf("a replica count below the published minimum must halt as a schema-conformance failure; got %v", err)
		}
		// The halt returns the guard roll-up, whose text is a count of what it
		// found; each finding travels on it structurally, with the locator of the
		// key it refused. So the field is read off the finding that carries the
		// condition, not searched for in the roll-up's text.
		var multi *resolver.MultiResolverError
		if !errors.As(err, &multi) {
			t.Fatalf("the refusal must carry its findings as a %T; got %T: %v", multi, err, err)
		}
		const refused = ".spec.components.front.values.replicas"
		var fields []string
		named := false
		for _, v := range multi.Violations {
			fields = append(fields, v.Field)
			if v.Field == refused && errors.Is(v, resolver.ErrSchemaConformance()) {
				named = true
			}
		}
		if !named {
			t.Errorf("no schema-conformance finding names the refused field %q; the findings name %q", refused, fields)
		}
	})
}
