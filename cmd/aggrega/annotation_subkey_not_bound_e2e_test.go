// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// annotationBindingWorkspace writes a Release, the Aggregate and Manifest it names,
// and a catalog whose Manifest binds the annotation feature.myco/db-pool to a
// Feature, routing db_name and read_replicas through values and pool_size and
// max_connections through infra. releaseValue and aggregateValue are the structured
// values the two documents write under that annotation, each a block of YAML lines
// indented under the key. It returns the catalog directory and the Release's path.
func annotationBindingWorkspace(t *testing.T, releaseValue, aggregateValue string) (catalogRoot, releasePath string) {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: app
  version: "1.0.0"
spec:
  subsystems:
    data:
      annotations:
        feature.myco/db-pool:
`+aggregateValue+`      components:
        api:
          type: core/server:1.0.0
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: rel
  version: "1.0.0"
  environment: testing
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    prod:
      directory: ./dist/prod
      annotations:
        feature.myco/db-pool:
`+releaseValue+`      components:
        api: {}
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/server:1.0.0: server-recipe:1.0.0
    features:
      type:
        core/db-pool: db-pool-helm:1.0.0
      annotations:
        feature.myco/db-pool:
          feature: core/db-pool:1.0.0
          maps:
            values:
              - db_name
              - read_replicas
            infra:
              - pool_size
              - max_connections
`)
	catalogRoot = filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogRoot, "base", "components", "server.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      image: { type: string, default: "registry.example/server:1.0" }
`)
	writeFixture(t, filepath.Join(catalogRoot, "base", "features", "db-pool.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: core/db-pool
  version: "1.0.0"
spec:
  schema:
    values:
      db_name: { type: string, default: "d" }
      read_replicas: { type: integer, default: 0 }
    infra:
      pool_size: { type: integer, default: 1 }
      max_connections: { type: integer, default: 1 }
`)
	writeHelmRendererPlugin(t, filepath.Join(catalogRoot, "base", "providers"))
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "helm", "server.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: server-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/server:1.0.0
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    containers:
      server:
        image: "{{ .values.image }}"
        ports:
          web: 8080
    services:
      svc:
        type: ClusterIP
        ports:
          web: 8080
`)
	writeFixture(t, filepath.Join(catalogRoot, "feature-recipes", "helm", "db-pool.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: FeatureRecipe
metadata:
  name: db-pool-helm
  version: "1.0.0"
spec:
  capability: helm
  implements: core/db-pool:1.0.0
  artifacts:
    pool.yaml: |
      apiVersion: v1
      kind: ConfigMap
      metadata:
        name: pool-{{ .context.instance_id }}
      data:
        db_name: "{{ .values.db_name }}"
        pool_size: "{{ .infra.pool_size }}"
`)
	return catalogRoot, filepath.Join(root, "release.yaml")
}

// TestPull_AnAnnotationSubkeyItsBindingDoesNotRouteHaltsWithItsShortCode resolves,
// through the real pull entry point, a Release and an Aggregate whose annotation
// values a Manifest binding routes, and reads the diagnostic a user reads.
//
// A subkey the binding does not route halts naming the unbound-subkey condition on
// either side: its identity is on the chain, the printed head carries its short
// code and the category of the document the subkey was written in, and the finding
// names the subkey, the annotation key and every path the binding does route, so a
// misspelling can be matched against the intended name. A routed subkey whose value
// the Feature refuses keeps its own condition, and a document whose subkeys all
// route resolves — the controls showing the halt is this rule's alone.
//
// Codes are read off the conditions' accessors, never written out, so a renumbered
// condition cannot leave this asserting a stale code.
func TestPull_AnAnnotationSubkeyItsBindingDoesNotRouteHaltsWithItsShortCode(t *testing.T) {
	notBound, ok := cerrors.ShortCodeOf(resolver.ErrAnnotationSubkeyNotBound())
	if !ok || notBound == "" {
		t.Fatal("the unbound-subkey condition reports no short code")
	}
	notConforming, ok := cerrors.ShortCodeOf(resolver.ErrAnnotationSubkeyNotConforming())
	if !ok || notConforming == "" {
		t.Fatal("the subkey-conformance condition reports no short code")
	}
	const routedRelease = "          pool_size: 4\n"
	const routedAggregate = "          db_name: \"orders\"\n"
	declaredPaths := []string{"pool_size", "max_connections", "db_name", "read_replicas"}

	rows := []struct {
		name      string
		release   string
		aggregate string
		// halts is false for the control, which resolves.
		halts     bool
		condition error
		head      string
		names     []string
	}{
		{
			name:      "a Release subkey the binding does not route",
			release:   "          nosuch: 1\n",
			aggregate: routedAggregate,
			halts:     true,
			condition: resolver.ErrAnnotationSubkeyNotBound(),
			head:      "[" + string(cerrors.OpsError) + " " + notBound + "]",
			names:     append([]string{"nosuch", "feature.myco/db-pool"}, declaredPaths...),
		},
		{
			name:      "an Aggregate subkey the binding does not route",
			release:   routedRelease,
			aggregate: "          dbname: \"orders\"\n",
			halts:     true,
			condition: resolver.ErrAnnotationSubkeyNotBound(),
			head:      "[" + string(cerrors.DeveloperError) + " " + notBound + "]",
			names:     append([]string{"dbname", "feature.myco/db-pool"}, declaredPaths...),
		},
		{
			name:      "a routed subkey whose value the Feature refuses keeps its own condition",
			release:   "          pool_size: \"many\"\n",
			aggregate: routedAggregate,
			halts:     true,
			condition: resolver.ErrAnnotationSubkeyNotConforming(),
			head:      "[" + string(cerrors.OpsError) + " " + notConforming + "]",
			names:     []string{"pool_size", "feature.myco/db-pool"},
		},
		{
			name:      "subkeys the binding routes resolve",
			release:   routedRelease,
			aggregate: routedAggregate,
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			catalogRoot, releasePath := annotationBindingWorkspace(t, row.release, row.aggregate)
			ws := resolveTempWorkspace(t, tempWorkspaceOptions{})
			err := pullRun(context.Background(), pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogRoot}, ws: ws})
			if !row.halts {
				if err != nil {
					t.Fatalf("the control failed to resolve:\n%s", formatCompilerError(err))
				}
				return
			}
			if err == nil {
				t.Fatal("the pull returned nil; want it to halt on the annotation")
			}
			printed := formatCompilerError(err)
			if !errors.Is(err, row.condition) {
				t.Errorf("the pull halted without the condition %v on its chain:\n%s", row.condition, printed)
			}
			if row.condition != resolver.ErrAnnotationSubkeyNotBound() && errors.Is(err, resolver.ErrAnnotationSubkeyNotBound()) {
				t.Errorf("a routed subkey was reported as one its binding does not route:\n%s", printed)
			}
			if !strings.Contains(printed, row.head) {
				t.Errorf("the printed head does not carry %s:\n%s", row.head, printed)
			}
			for _, want := range row.names {
				if !strings.Contains(printed, want) {
					t.Errorf("the finding does not name %q:\n%s", want, printed)
				}
			}
		})
	}
}
