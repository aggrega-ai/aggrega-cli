// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// The test in this file drives what an author meets who gives one container one
// environment variable twice — as a `variables` entry and as an `attachments`
// entry of the same name delivered `as: env` — through the real pull and build
// and the providers embedded in the binary, on helm and on compose alike. A pair
// on a main container and a pair on an init container each halt, positioned at
// the Recipe's Workload key and naming the container and the variable, and are
// matched both by the condition and by the words a reader is shown. A pair
// written under complementary template conditions renders one of the two and
// builds, and its output carries the name exactly once.

// envPairSentence is the condition's registered sentence, which every refusal
// below opens its finding with.
const envPairSentence = "a container declares one environment variable twice"

// envPairAdvice is what every refusal below tells its reader to do.
const envPairAdvice = "Rename one of the two, or write them under complementary template conditions " +
	"so that only one of them renders"

// envPairPosition is where every refusal below is positioned: the Workload key of
// the Recipe the fixture writes, which names the Recipe's own document.
const envPairPosition = "recipe.yaml at .spec.workload"

// envPairMain gives the main container the variable both ways, unguarded.
const envPairMain = `    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      main:
        image: app:1
        variables:
          DB_PASSWORD: plain
        attachments:
          DB_PASSWORD:
            from: credential
            as: env
`

// envPairInit gives an init container the variable both ways, unguarded, and
// leaves the main container without a pair.
const envPairInit = `    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    initContainers:
      - name: migrate
        image: app:1
        variables:
          DB_PASSWORD: plain
        attachments:
          DB_PASSWORD:
            from: credential
            as: env
    containers:
      main:
        image: app:1
`

// envPairGuarded writes the variable only where no credential is attached and
// the attachment only where one is — the way a Recipe falls back to a plain
// value — so exactly one of the two renders whichever way the condition goes.
// The fixture attaches the credential, so the attachment is the one rendered.
const envPairGuarded = `    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    containers:
      main:
        image: app:1
        variables:
          APP_MODE: standard
    {{- if not (hasKey (default dict .features) "credential") }}
          DB_PASSWORD: plain
    {{- end }}
    {{- if hasKey (default dict .features) "credential" }}
        attachments:
          DB_PASSWORD:
            from: credential
            as: env
    {{- end }}
`

// envPairFixture lays down a workspace whose one component is realized by a
// synthesized Recipe for provider with workload as its Workload body. The
// component declares a credential slot that permits env delivery, and the
// Aggregate fills it with a Feature whose FeatureRecipe publishes the name and
// the data key a Workload reference reads, so every check beside the one under
// test passes. Every entity is in the workspace's catalog directory, so nothing
// reaches a registry.
func envPairFixture(t *testing.T, root, provider, workload string) (catalogDir, releasePath string) {
	t.Helper()
	catalogDir = filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogDir, "component/example/compute/web/1.0.0/component.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: example/compute/web
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
    component.aggrega.ai/category: compute
spec:
  featuresSchema:
    credential:
      inject: [env]
      description: The database password the application reads from its environment.
`)
	writeFixture(t, filepath.Join(catalogDir, "feature/example/credential/1.0.0/feature.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: example/credential
  version: 1.0.0
spec:
  schema:
    values:
      origin:
        type: string
        default: app-db
      key:
        type: string
        default: password
    provides:
      name:
        type: string
        required: true
      key:
        type: string
        required: true
`)
	writeFixture(t, filepath.Join(catalogDir, "feature-recipe/"+provider+"/credential-ref/1.0.0/recipe.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: FeatureRecipe
metadata:
  name: credential-ref
  version: 1.0.0
spec:
  capability: `+provider+`
  implements: example/credential:1.0.0
  provides:
    name: "{{ .values.origin }}"
    key: "{{ .values.key }}"
`)
	writeFixture(t, filepath.Join(catalogDir, "recipe/"+provider+"/web/1.0.0/recipe.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: web
  version: 1.0.0
spec:
  capability: `+provider+`
  implements: example/compute/web:1.0.0
  workload: |
`+workload)
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: site
  version: "1.0.0"
spec:
  components:
    web:
      type: example/compute/web
      features:
        credential:
          type: example/credential
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: site-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        example/compute/web:1.0.0: `+provider+`/web:1.0.0
    features:
      type:
        example/credential:1.0.0: `+provider+`/credential-ref:1.0.0
`)
	releasePath = filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: site-release
  environment: testing
  version: "1.0.0"
spec:
  aggregate: site
  manifest: site-manifest
  boundaries:
    app:
      directory: app
      components:
        web: {}
`)
	return catalogDir, releasePath
}

// buildEnvPairFixture pulls and builds the fixture, returning the workspace root
// and the build's error. A pull failure fails the test: every row's fault is one
// the build meets, not one the pull does.
func buildEnvPairFixture(t *testing.T, provider, workload string) (string, error) {
	t.Helper()
	root := t.TempDir()
	catalogDir, rel := envPairFixture(t, root, provider, workload)
	if err := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: rel,
		CatalogDirs: []string{catalogDir},
		DiagOut:     io.Discard,
		Out:         io.Discard,
		diag:        buildDiagOptions{quiet: true},
	}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	return root, dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		rel, []string{catalogDir}, false, io.Discard, io.Discard)
}

// helmMainEnv reads the env entries of the main container of the Deployment the
// helm build wrote, in the order written. The Deployment is found by its kind
// among the files the build wrote rather than by a file name, so the assertion
// reads what was rendered rather than where a name convention puts it.
func helmMainEnv(t *testing.T, root string) []helmEnvEntry {
	t.Helper()
	dir := filepath.Join(root, "dist", "app", "helm")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read the helm output: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		dec := yaml.NewDecoder(bytes.NewReader(mustReadFile(t, filepath.Join(dir, e.Name()))))
		for {
			var doc struct {
				Kind string `yaml:"kind"`
				Spec struct {
					Template struct {
						Spec struct {
							Containers []struct {
								Name string         `yaml:"name"`
								Env  []helmEnvEntry `yaml:"env"`
							} `yaml:"containers"`
						} `yaml:"spec"`
					} `yaml:"template"`
				} `yaml:"spec"`
			}
			if err := dec.Decode(&doc); err != nil {
				if errors.Is(err, io.EOF) {
					break
				}
				t.Fatalf("parse %s: %v", e.Name(), err)
			}
			if doc.Kind != "Deployment" {
				continue
			}
			for _, c := range doc.Spec.Template.Spec.Containers {
				if c.Name == "main" {
					return c.Env
				}
			}
		}
	}
	t.Fatalf("the helm build wrote no Deployment with a main container; files: %q", names)
	return nil
}

// helmEnvEntry is one env entry of a rendered container: its name, and whether
// it is read from a Secret rather than given a value.
type helmEnvEntry struct {
	Name      string `yaml:"name"`
	ValueFrom *struct {
		SecretKeyRef *struct {
			Name string `yaml:"name"`
			Key  string `yaml:"key"`
		} `yaml:"secretKeyRef"`
	} `yaml:"valueFrom"`
}

// composeWebEnvironment reads the web service's environment mapping off the
// docker-compose.yaml the build assembled, as its key and value nodes in the
// order written. It reads nodes rather than a decoded map so that a key written
// twice is counted twice rather than refused or folded by the decode.
func composeWebEnvironment(t *testing.T, root string) []*yaml.Node {
	t.Helper()
	body := mustReadFile(t, filepath.Join(root, "dist", "app", "compose", "docker-compose.yaml"))
	var doc yaml.Node
	if err := yaml.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse docker-compose.yaml: %v\n%s", err, body)
	}
	if len(doc.Content) == 0 {
		t.Fatalf("docker-compose.yaml holds no document:\n%s", body)
	}
	env := mappingValue(mappingValue(mappingValue(doc.Content[0], "services"), "web"), "environment")
	if env == nil || env.Kind != yaml.MappingNode {
		t.Fatalf("the web service carries no environment mapping:\n%s", body)
	}
	return env.Content
}

// TestDuplicateEnvironmentVariableThroughTheBuild holds each row against the
// condition the build halted with, or against what it wrote. The guarded rows
// are the controls: the same name, written both ways in one template, built
// because only one of the two renders.
func TestDuplicateEnvironmentVariableThroughTheBuild(t *testing.T) {
	t.Parallel()

	for _, provider := range []string{"helm", "compose"} {
		for _, row := range []struct {
			name     string
			workload string
			// values are the variable and the container the printed finding names,
			// as its parenthesis spells them.
			values string
		}{
			{"a main container naming one variable both ways halts", envPairMain,
				`("DB_PASSWORD", container "main")`},
			{"an init container naming one variable both ways halts", envPairInit,
				`("DB_PASSWORD", init container "initContainers[0]")`},
		} {
			t.Run(provider+"/"+row.name, func(t *testing.T) {
				t.Parallel()
				_, err := buildEnvPairFixture(t, provider, row.workload)
				if err == nil {
					t.Fatalf("a container giving DB_PASSWORD two values built on %s", provider)
				}
				printed := formatCompilerError(err)
				if !errors.Is(err, validator.ErrDuplicateContainerEnvironmentVariable()) {
					t.Fatalf("the build's error does not name the duplicate-environment-variable condition:\n%s", printed)
				}
				for _, want := range []string{
					envPairSentence + " " + row.values + ". " + envPairAdvice,
					envPairPosition,
					"E0345",
				} {
					if !strings.Contains(printed, want) {
						t.Errorf("the printed diagnostic does not carry %q:\n%s", want, printed)
					}
				}
			})
		}

		t.Run(provider+"/a pair under complementary conditions builds and writes the name once", func(t *testing.T) {
			t.Parallel()
			root, err := buildEnvPairFixture(t, provider, envPairGuarded)
			if err != nil {
				t.Fatalf("build:\n%s", formatCompilerError(err))
			}
			switch provider {
			case "helm":
				var found []helmEnvEntry
				for _, e := range helmMainEnv(t, root) {
					if e.Name == "DB_PASSWORD" {
						found = append(found, e)
					}
				}
				if len(found) != 1 {
					t.Fatalf("the main container carries %d DB_PASSWORD entries, want exactly 1", len(found))
				}
				if ref := found[0].ValueFrom; ref == nil || ref.SecretKeyRef == nil ||
					ref.SecretKeyRef.Name != "app-db" || ref.SecretKeyRef.Key != "password" {
					t.Errorf("DB_PASSWORD is not the Secret-read entry the attachment renders: %+v", found[0])
				}
			case "compose":
				env := composeWebEnvironment(t, root)
				var values []string
				for i := 0; i+1 < len(env); i += 2 {
					if env[i].Value == "DB_PASSWORD" {
						values = append(values, env[i+1].Value)
					}
				}
				if len(values) != 1 {
					t.Fatalf("the web service's environment names DB_PASSWORD %d times, want exactly 1", len(values))
				}
				// The attachment renders, and compose writes an attachment-read
				// variable's name with an empty value; "plain" here would mean
				// the variable rendered instead.
				if values[0] != "" {
					t.Errorf("DB_PASSWORD = %q; the attachment-read entry carries an empty value on compose", values[0])
				}
			}
		})
	}
}
