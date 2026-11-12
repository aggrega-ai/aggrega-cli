// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

// The build tool over a catalog directory holding one name at two versions.
//
// The tool resolves through the same view a command-line build does, so the
// version it renders is the version the rule selects — the highest satisfying one
// — and a build asked to fill its dependencies first pins that same version at
// that version's own digest. Each version carries a different default value, so
// the rendered files say which one the tool selected.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// versionSelectionWorkspace writes a workspace whose catalog directory holds a
// Component, a Feature and a sub-aggregate at 1.0.0 and 2.0.0, with one Release per
// reference form, and returns the server, the root and, keyed "<name>@<version>",
// the digest each versioned document's lockfile row records: over what a
// publication of that document would carry, through the product's own reading of
// it (catalog.AuthoredEntityDigestAt).
func versionSelectionWorkspace(t *testing.T) (s *server, root string, digests map[string]string) {
	t.Helper()
	root = t.TempDir()
	digests = map[string]string{}
	write := func(rel, body string) {
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	versioned := func(key, rel, body string) {
		write(rel, body)
		row, err := catalog.AuthoredEntityDigestAt(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("row digest of %s: %v", rel, err)
		}
		digests[key] = row
	}
	write(workspaceconfig.ConfigFilename, "workspace:\n  catalog_dirs:\n    - ./catalog\noffline: true\n")
	for _, v := range []string{"1.0.0", "2.0.0"} {
		versioned("test/svc@"+v, "catalog/component/test/svc/"+v+"/component.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: test/svc
  version: "`+v+`"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      greeting: { type: string, default: "svc-`+v+`" }
    infra:
      replicas: { type: integer, default: 1 }
`)
		versioned("test/tag@"+v, "catalog/feature/test/tag/"+v+"/feature.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: test/tag
  version: "`+v+`"
spec:
  schema:
    values:
      label: { type: string, default: "tag-`+v+`" }
`)
		versioned("test/sub@"+v, "catalog/aggregate/test/sub/"+v+"/aggregate.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: test/sub
  version: "`+v+`"
spec:
  components:
    inner:
      type: test/inner
      values:
        greeting: sub-`+v+`
`)
	}
	write("catalog/component/test/inner/1.0.0/component.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: test/inner
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      greeting: { type: string, default: "inner-default" }
    infra:
      replicas: { type: integer, default: 1 }
`)
	for _, r := range []struct{ name, implements string }{{"svc", "test/svc"}, {"inner", "test/inner"}} {
		write("catalog/recipe/helm/"+r.name+"/1.0.0/recipe.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: `+r.name+`
  version: "1.0.0"
spec:
  capability: helm
  implements: `+r.implements+`
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    replicas: {{ optional .infra `+"`replicas`"+` 1 }}
    containers:
      server:
        image: test-server:latest
        variables:
          GREETING: "{{ .values.greeting }}"
`)
	}
	write("catalog/feature-recipe/helm/tag/1.0.0/feature-recipe.yaml", `apiVersion: aggrega.ai/v1beta1
kind: FeatureRecipe
metadata:
  name: tag
  version: "1.0.0"
spec:
  capability: helm
  implements: test/tag
  artifacts:
    tag.yaml: |
      apiVersion: v1
      kind: ConfigMap
      metadata:
        name: tag
      data:
        label: "{{ .values.label }}"
`)
	for _, f := range buildToolVersionForms {
		write("aggregate-"+f.id+".yaml", `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: app-`+f.id+`
  version: "1.0.0"
spec:
  components:
    app:
      type: test/svc`+f.suffix+`
      features:
        tag:
          type: test/tag`+f.suffix+`
  aggregates:
    sub:
      type: test/sub`+f.suffix+`
`)
		write("manifest-"+f.id+".yaml", `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: manifest-`+f.id+`
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        test/svc`+f.suffix+`: helm/svc:1.0.0
        test/inner: helm/inner:1.0.0
    features:
      type:
        test/tag`+f.suffix+`: helm/tag:1.0.0
`)
		write("release-"+f.id+".yaml", `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: rel-`+f.id+`
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate-`+f.id+`.yaml
  manifest: ./manifest-`+f.id+`.yaml
  boundaries:
    main:
      directory: ./dist/`+f.id+`
      components:
        app:
          infra:
            replicas: 1
        sub.inner:
          infra:
            replicas: 1
`)
	}
	s = newTestServer(t)
	s.toolchain = pullToolchainStamp
	return s, root, digests
}

// buildToolVersionForms are the reference forms, each with the version it selects.
var buildToolVersionForms = []struct {
	id, suffix, want string
}{
	{"exact1", ":1.0.0", "1.0.0"},
	{"exact2", ":2.0.0", "2.0.0"},
	{"caret1", ":^1.0.0", "1.0.0"},
	{"bare", "", "2.0.0"},
}

// TestBuildToolSelectsTheVersionTheRuleSelects drives the tool twice per reference
// form: rendering straight from the catalog directory, and filling the lockfile
// first. Both render the selected version's values, and the fill pins that version
// at its own document's row digest — the pin the build it then renders against has to
// agree with, or the fill refuses to write it.
func TestBuildToolSelectsTheVersionTheRuleSelects(t *testing.T) {
	for _, f := range buildToolVersionForms {
		for _, fill := range []bool{false, true} {
			name := f.id + "/render"
			if fill {
				name = f.id + "/fill-then-render"
			}
			t.Run(name, func(t *testing.T) {
				s, root, digests := versionSelectionWorkspace(t)
				releasePath := filepath.Join(root, "release-"+f.id+".yaml")
				res := callTool(t, s, "build", buildArgs{
					Release:       releasePath,
					WorkspaceRoot: root,
					CatalogDirs:   []string{filepath.Join(root, "catalog")},
					AutoPull:      fill,
				})
				if res.IsError {
					t.Fatalf("the build tool refused a catalog directory holding two versions of each name: %s",
						res.Content[0].Text)
				}
				var rendered strings.Builder
				for _, file := range unmarshalBuildPayload(t, res).Files {
					rendered.WriteString(file.Content)
					rendered.WriteByte('\n')
				}
				for _, want := range []string{"svc-" + f.want, "tag-" + f.want, "sub-" + f.want} {
					if !strings.Contains(rendered.String(), want) {
						t.Errorf("the rendered files do not carry %q, the value of the version %s selects",
							want, "release-"+f.id)
					}
				}
				if !fill {
					return
				}
				lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
				if err != nil {
					t.Fatalf("the fill wrote no readable lockfile: %v", err)
				}
				rows := map[string]lockfile.Entity{}
				for _, e := range lf.Entities {
					rows[e.URI] = e
				}
				for _, n := range []string{"test/svc", "test/tag", "test/sub"} {
					row, ok := rows[n+f.suffix]
					if !ok {
						t.Errorf("the fill recorded no row for %s", n+f.suffix)
						continue
					}
					if row.ResolvedVersion != f.want || row.Digest != digests[n+"@"+f.want] {
						t.Errorf("%s is pinned at (%q, %s); want (%q, %s), the selected version and its own digest",
							row.URI, row.ResolvedVersion, row.Digest, f.want, digests[n+"@"+f.want])
					}
				}
			})
		}
	}
}
