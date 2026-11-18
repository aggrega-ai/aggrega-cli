// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
)

// The test in this file drives one producer publishing a short-form intrinsic as
// text — the way the catalog's CloudFormation Recipes publish a reference — to a
// consumer reading it through its connection, through the real pull and build with
// the embedded CloudFormation provider. The consumer splices the value three ways:
// into a fragment it authors in the same stack, into a values mapping filling a
// sourced template's parameter in the same stack, and into a fragment in another
// stack; each written quoted and bare. Quoted and bare mean the same on every path:
// the same-stack paths receive the intrinsic itself, and the cross-stack path the
// import of the producer's export.

const (
	cfnProducerProvides = `"!GetAtt {{ capitalizedWords .context.instance_id }}.Endpoint.Address"`
	cfnSplice           = "{{ .context.inject.env.DB_HOST }}"
)

// cfnShortFormWorkspace lays down the producer pg and the consumer api in one
// Release; the consumer's Recipe is consumerBody (the lines beneath its
// `implements:`), and its template file, when sourced, is written beside it.
// twoStacks places the two in different stacks.
func cfnShortFormWorkspace(t *testing.T, root, consumerBody string, sourced, twoStacks bool) (catalogDir, releasePath string) {
	t.Helper()
	writeWorkspaceConfig(t, root, "workspace: {}\noffline: true\npin_sources_at_pull: true")
	catalogDir = filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogDir, "component/acme/datastore/pg/1.0.0/component.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: acme/datastore/pg
  version: 1.0.0
  labels:
    component.aggrega.ai/type: stateful-server
spec:
  schema:
    provides:
      host: { type: string }
`)
	writeFixture(t, filepath.Join(catalogDir, "component/acme/compute/api/1.0.0/component.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: acme/compute/api
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  usesSchema:
    db:
      selector: "component.aggrega.ai/type=stateful-server"
      inject: ["env"]
`)
	writeFixture(t, filepath.Join(catalogDir, "recipe/cloudformation/pg/1.0.0/recipe.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: pg
  version: 1.0.0
spec:
  capability: cloudformation
  implements: acme/datastore/pg:1.0.0
  provides:
    host: `+cfnProducerProvides+`
  artifacts:
    template.yaml: |
      Resources:
        {{ capitalizedWords .context.instance_id }}:
          Type: AWS::RDS::DBInstance
          Properties:
            Engine: postgres
      provides:
        host: !GetAtt {{ capitalizedWords .context.instance_id }}.Endpoint.Address
`)
	writeFixture(t, filepath.Join(catalogDir, "recipe/cloudformation/api/1.0.0/recipe.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: api
  version: 1.0.0
spec:
  capability: cloudformation
  implements: acme/compute/api:1.0.0
`+consumerBody)
	if sourced {
		writeFixture(t, filepath.Join(catalogDir, "recipe/cloudformation/api/1.0.0/templates/api.yaml"), `Parameters:
  DbHost:
    Type: String
Resources:
  Task:
    Type: AWS::ECS::TaskDefinition
    Properties:
      ContainerDefinitions:
        - Name: app
          Environment:
            - Name: DB_HOST
              Value: !Ref DbHost
`)
	}
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: app
  version: 1.0.0
spec:
  components:
    payments-db:
      type: acme/datastore/pg:1.0.0
    api:
      type: acme/compute/api:1.0.0
      uses:
        db:
          primary:
            target: payments-db
            inject:
              env:
                - in: DB_HOST
                  from: host
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: manifest
  version: 1.0.0
spec:
  mappings:
    components:
      type:
        acme/datastore/pg:1.0.0: "cloudformation/pg:1.0.0"
        acme/compute/api:1.0.0: "cloudformation/api:1.0.0"
`)
	boundaries := `  boundaries:
    stack:
      directory: stack
      components:
        payments-db: {}
        api: {}
`
	if twoStacks {
		boundaries = `  boundaries:
    stack-a:
      directory: stack-a
      components:
        payments-db: {}
    stack-b:
      directory: stack-b
      components:
        api: {}
`
	}
	releasePath = filepath.Join(root, "release.yaml")
	writeFixture(t, releasePath, `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: orders
  version: 1.0.0
  environment: probe
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
`+boundaries)
	return catalogDir, releasePath
}

// cfnFragmentConsumer is the consumer Recipe authoring its own fragment, the
// spliced value written quoted or bare.
func cfnFragmentConsumer(quoted bool) string {
	value := cfnSplice
	if quoted {
		value = `"` + cfnSplice + `"`
	}
	return `  artifacts:
    template.yaml: |
      Resources:
        {{ capitalizedWords .context.instance_id }}:
          Type: AWS::ECS::TaskDefinition
          Properties:
            ContainerDefinitions:
              - Name: app
                Environment:
                  - Name: DB_HOST
                    Value: ` + value + `
`
}

// cfnMappingConsumer is the consumer Recipe filling its sourced template's DbHost
// parameter through a values mapping, the spliced value written quoted or bare.
func cfnMappingConsumer(quoted bool) string {
	value := cfnSplice
	if quoted {
		value = `"` + cfnSplice + `"`
	}
	return `  source: ./templates/api.yaml
  valuesMapping: |
    DbHost: ` + value + `
`
}

// cfnBuildDBHost pulls and builds the workspace and returns the value the
// consumer's DB_HOST environment entry holds in the stack directory dirSegment.
func cfnBuildDBHost(t *testing.T, consumerBody string, sourced, twoStacks bool, dirSegment string) any {
	t.Helper()
	root := t.TempDir()
	catalogDir, rel := cfnShortFormWorkspace(t, root, consumerBody, sourced, twoStacks)
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	if err := pullRun(context.Background(), pullConfig{
		ws: ws, ReleasePath: rel, CatalogDirs: []string{catalogDir},
		DiagOut: io.Discard, Out: io.Discard, diag: buildDiagOptions{quiet: true},
	}); err != nil {
		t.Fatalf("pull:\n%s", formatCompilerError(err))
	}
	if err := dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		rel, []string{catalogDir}, false, io.Discard, io.Discard); err != nil {
		t.Fatalf("build:\n%s", formatCompilerError(err))
	}
	for path, body := range renderedTree(t, filepath.Join(root, "dist")) {
		if !strings.Contains(path, dirSegment) || !strings.HasSuffix(path, "cloudformation-stack.json") {
			continue
		}
		var stack map[string]any
		if err := json.Unmarshal([]byte(body), &stack); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		if v, found := findDBHost(stack); found {
			return v
		}
	}
	t.Fatalf("no stack under %q carries a DB_HOST environment entry", dirSegment)
	return nil
}

// findDBHost walks a decoded stack for the environment entry named DB_HOST and
// returns its value.
func findDBHost(v any) (any, bool) {
	switch x := v.(type) {
	case map[string]any:
		if x["Name"] == "DB_HOST" {
			if value, ok := x["Value"]; ok {
				return value, true
			}
		}
		for _, child := range x {
			if found, ok := findDBHost(child); ok {
				return found, true
			}
		}
	case []any:
		for _, child := range x {
			if found, ok := findDBHost(child); ok {
				return found, true
			}
		}
	}
	return nil, false
}

// TestCloudFormation_AShortFormSplicedQuotedOrBareReadsAsOneIntrinsic builds each
// path quoted and bare, and holds the two to one value: the producer's intrinsic
// itself in the same stack, its export's import across stacks.
func TestCloudFormation_AShortFormSplicedQuotedOrBareReadsAsOneIntrinsic(t *testing.T) {
	embeddedentities.Require(t)
	t.Parallel()
	getAtt := map[string]any{"Fn::GetAtt": []any{"PaymentsDb", "Endpoint.Address"}}
	for _, path := range []struct {
		name      string
		body      func(quoted bool) string
		sourced   bool
		twoStacks bool
		dir       string
		want      func(t *testing.T, got any)
	}{
		{"a fragment in the same stack", cfnFragmentConsumer, false, false, "stack/",
			func(t *testing.T, got any) {
				if !reflect.DeepEqual(got, getAtt) {
					t.Errorf("DB_HOST = %#v, want the intrinsic %#v", got, getAtt)
				}
			}},
		{"a values mapping in the same stack", cfnMappingConsumer, true, false, "stack/",
			func(t *testing.T, got any) {
				if !reflect.DeepEqual(got, getAtt) {
					t.Errorf("DB_HOST = %#v, want the intrinsic %#v", got, getAtt)
				}
			}},
		{"a fragment in another stack", cfnFragmentConsumer, false, true, "stack-b/",
			func(t *testing.T, got any) {
				m, ok := got.(map[string]any)
				if !ok || len(m) != 1 || m["Fn::ImportValue"] == nil {
					t.Errorf("DB_HOST = %#v, want an import of the producer's export", got)
				}
			}},
	} {
		t.Run(path.name, func(t *testing.T) {
			t.Parallel()
			quoted := cfnBuildDBHost(t, path.body(true), path.sourced, path.twoStacks, path.dir)
			bare := cfnBuildDBHost(t, path.body(false), path.sourced, path.twoStacks, path.dir)
			path.want(t, quoted)
			if !reflect.DeepEqual(quoted, bare) {
				t.Errorf("quoted and bare differ:\nquoted: %#v\nbare:   %#v", quoted, bare)
			}
		})
	}
}
