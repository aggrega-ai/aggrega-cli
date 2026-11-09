// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// A catalog directory holding one name at two versions, resolved end to end.
//
// Every case drives the real pull and the real build over one workspace whose
// catalog directory holds a Component, a Feature, a sub-aggregate and a
// pipeline-step Plugin each at 1.0.0 and 2.0.0. The versions carry different
// values, so the rendered tree says which one a build selected, and the lockfile
// says which one the pull pinned and whose bytes it hashed. A pull and a build that
// disagreed about either would stop at the pull, which refuses to write a lockfile
// its own build would refuse — so a case that completes is the two agreeing, and
// the rendered values say what they agreed on.

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// multiVersionWorkspaceConfig is the workspace configuration the cases write,
// with the offline posture on or off.
func multiVersionWorkspaceConfig(offline bool) string {
	cfg := "workspace:\n  catalog_dirs:\n    - ./catalog\n"
	if offline {
		cfg += "offline: true\n"
	}
	return cfg
}

// writeMultiVersionWorkspace writes the workspace and returns its root and, keyed
// "<name>@<version>", the digest each versioned document's lockfile row records:
// over what a publication of that document would carry, through the product's own
// reading of it (catalog.AuthoredEntityDigestAt). The four Releases
// differ only in the reference form their Aggregate, Manifest and Recipe write.
func writeMultiVersionWorkspace(t *testing.T) (root string, digests map[string]string) {
	t.Helper()
	root = t.TempDir()
	guest, err := os.ReadFile(wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm"))
	if err != nil {
		t.Fatalf("read the guest fixture: %v", err)
	}
	digests = map[string]string{}
	doc := func(rel, body string) {
		writeFixture(t, filepath.Join(root, rel), body)
	}
	versioned := func(key, rel, body string) {
		doc(rel, body)
		row, err := catalog.AuthoredEntityDigestAt(filepath.Join(root, rel))
		if err != nil {
			t.Fatalf("row digest of %s: %v", rel, err)
		}
		digests[key] = row
	}
	writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), multiVersionWorkspaceConfig(true))

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
		pluginDir := "catalog/plugin/pipeline-step/patch/" + v
		doc(pluginDir+"/plugin.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: patch
  pluginType: pipeline-step
  version: "`+v+`"
spec:
  abiVersion: v1beta1
  exports:
    - name: step
      operates: patch
      scope: own
`)
		writeFixtureBytes(t, filepath.Join(root, pluginDir, pluginpath.PluginLeaf("pipeline-step/patch")), guest)
	}
	doc("catalog/component/test/inner/1.0.0/component.yaml", `apiVersion: aggrega.ai/v1beta1
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
	recipe := func(name, implements, pipeline string) {
		body := `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: ` + name + `
  version: "1.0.0"
spec:
  capability: helm
  implements: ` + implements + `
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    replicas: {{ optional .infra ` + "`replicas`" + ` 1 }}
    containers:
      server:
        image: test-server:latest
        variables:
          GREETING: "{{ .values.greeting }}"
`
		if pipeline != "" {
			body += "  pipeline:\n    - id: patch\n      plugin: " + pipeline + "\n      function: step\n"
		}
		doc("catalog/recipe/helm/"+name+"/1.0.0/recipe.yaml", body)
	}
	recipe("inner", "test/inner", "")
	doc("catalog/feature-recipe/helm/tag/1.0.0/feature-recipe.yaml", `apiVersion: aggrega.ai/v1beta1
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
	for _, f := range versionSelectionForms {
		recipe("svc-"+f.id, "test/svc", "pipeline-step/patch"+f.suffix)
		doc("aggregate-"+f.id+".yaml", `apiVersion: aggrega.ai/v1beta1
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
		doc("manifest-"+f.id+".yaml", `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: manifest-`+f.id+`
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        test/svc`+f.suffix+`: helm/svc-`+f.id+`:1.0.0
        test/inner: helm/inner:1.0.0
    features:
      type:
        test/tag`+f.suffix+`: helm/tag:1.0.0
`)
		doc("release-"+f.id+".yaml", `apiVersion: aggrega.ai/v1beta1
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
	return root, digests
}

// versionSelectionForms are the reference forms the Releases write, each with the
// version it must select. The four rows do not agree, so a pull or a build that
// ignored the version, or read the first file carrying the name, fails some row.
var versionSelectionForms = []struct {
	id, suffix, want string
}{
	{"exact1", ":1.0.0", "1.0.0"},
	{"exact2", ":2.0.0", "2.0.0"},
	{"caret1", ":^1.0.0", "1.0.0"},
	{"bare", "", "2.0.0"},
}

// renderedText concatenates every file under dir, so a case can ask what the
// render carried without depending on how a provider names its files.
func renderedText(t *testing.T, dir string) string {
	t.Helper()
	var sb strings.Builder
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		sb.Write(b)
		sb.WriteByte('\n')
		return nil
	})
	if err != nil {
		t.Fatalf("read the rendered tree under %s: %v", dir, err)
	}
	return sb.String()
}

// TestPullAndBuildSelectOneVersionFromAMultiVersionCatalog is the rule end to end,
// per reference form: the pull pins the highest satisfying version of the
// Component type, the Feature attachment and the sub-aggregate, each at its own
// document's digest; a frozen build and an offline build of the same Release both
// render, and the rendered values are those of the version the lockfile names.
func TestPullAndBuildSelectOneVersionFromAMultiVersionCatalog(t *testing.T) {
	t.Parallel()
	for _, f := range versionSelectionForms {
		t.Run(f.id, func(t *testing.T) {
			root, digests := writeMultiVersionWorkspace(t)
			releasePath := filepath.Join(root, "release-"+f.id+".yaml")

			var pullDiag, pullOut bytes.Buffer
			if err := pullRun(context.Background(), pullConfig{
				ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
				ReleasePath: releasePath,
				DiagOut:     &pullDiag,
				Out:         &pullOut,
			}); err != nil {
				t.Fatalf("the pull refused a catalog directory holding two versions of each name: %v\ndiagnostics:\n%s",
					err, pullDiag.String())
			}
			lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
			if err != nil {
				t.Fatalf("load the written lockfile: %v", err)
			}
			rows := map[string]lockfile.Entity{}
			for _, e := range lf.Entities {
				rows[e.URI] = e
			}
			for _, name := range []string{"test/svc", "test/tag", "test/sub"} {
				row, ok := rows[name+f.suffix]
				if !ok {
					t.Errorf("the lockfile records no row for %s", name+f.suffix)
					continue
				}
				if row.ResolvedVersion != f.want {
					t.Errorf("%s is pinned at %q; the highest satisfying version is %q", row.URI, row.ResolvedVersion, f.want)
				}
				if want := digests[name+"@"+f.want]; row.Digest != want {
					t.Errorf("%s is pinned at digest %s; the %s document's own digest is %s", row.URI, row.Digest, f.want, want)
				}
			}

			wantValues := []string{"svc-" + f.want, "tag-" + f.want, "sub-" + f.want, "pipeline-emitted"}
			for _, posture := range []struct {
				name    string
				offline bool
			}{
				{"frozen build", false},
				{"offline build", true},
			} {
				writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), multiVersionWorkspaceConfig(posture.offline))
				ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
				if ws.Config().Offline != posture.offline {
					t.Fatalf("%s: the workspace resolved offline=%v, want %v", posture.name, ws.Config().Offline, posture.offline)
				}
				if err := os.RemoveAll(filepath.Join(root, "dist")); err != nil {
					t.Fatalf("clear the rendered tree: %v", err)
				}
				var diag, out bytes.Buffer
				if err := dispatchBuildWithDiag(context.Background(), ws, releasePath, nil, false,
					buildDiagOptions{}, rootedBuildOptions(t, root, buildops.RunOptions{}), &diag, &out); err != nil {
					t.Fatalf("%s refused the lockfile its own pull wrote: %v\ndiagnostics:\n%s", posture.name, err, diag.String())
				}
				rendered := renderedText(t, filepath.Join(root, "dist"))
				for _, want := range wantValues {
					if !strings.Contains(rendered, want) {
						t.Errorf("%s: the rendered tree does not carry %q, the value of the version the lockfile "+
							"pins; the build selected another version", posture.name, want)
					}
				}
				other := "svc-" + map[string]string{"1.0.0": "2.0.0", "2.0.0": "1.0.0"}[f.want]
				if strings.Contains(rendered, other) {
					t.Errorf("%s: the rendered tree carries %q, the value of the version the reference does not select",
						posture.name, other)
				}
			}
		})
	}
}

// TestValidateReportsAnUnsatisfiablePinAsThePullDoes is the single-version case: a
// pin no version on disk satisfies is refused by the pull, and a validate over the
// same Release — which resolves through the build's view — refuses it with the same
// condition and sentence rather than resolving the pin to the name's other file.
// Both report the version as unsatisfiable rather than the entity as missing,
// because the catalog holds the name at another version.
func TestValidateReportsAnUnsatisfiablePinAsThePullDoes(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	writeFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), multiVersionWorkspaceConfig(true))
	writeFixture(t, filepath.Join(root, "catalog/component/test/svc/1.0.0/component.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: test/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    infra:
      replicas: { type: integer, default: 1 }
`)
	writeFixture(t, filepath.Join(root, "catalog/recipe/helm/svc/1.0.0/recipe.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: svc
  version: "1.0.0"
spec:
  capability: helm
  implements: test/svc
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    containers:
      server:
        image: test-server:latest
`)
	for _, pin := range []string{"1.0.0", "2.0.0"} {
		dir := filepath.Join(root, "pin-"+pin)
		writeFixture(t, filepath.Join(dir, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: app
  version: "1.0.0"
spec:
  components:
    app:
      type: test/svc:`+pin+`
`)
		writeFixture(t, filepath.Join(dir, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        test/svc:`+pin+`: helm/svc:1.0.0
`)
		writeFixture(t, filepath.Join(dir, "release.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        app:
          infra:
            replicas: 1
`)
	}
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve the workspace: %v", err)
	}

	// The control: the satisfiable pin validates, so the refusal below is about the
	// pin and not about a workspace nothing could resolve.
	var cdiag, cout bytes.Buffer
	if err := validateRelease(context.Background(), rw, filepath.Join(root, "pin-1.0.0", "release.yaml"), nil,
		compilerops.VerbosityDetailed, false, false, &cdiag, &cout); err != nil {
		t.Fatalf("control: the satisfiable pin was refused: %v\n%s", err, cdiag.String())
	}

	releasePath := filepath.Join(root, "pin-2.0.0", "release.yaml")
	pullErr := pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		DiagOut:     &bytes.Buffer{},
		Out:         &bytes.Buffer{},
	})
	if pullErr == nil {
		t.Fatal("the pull accepted test/svc:2.0.0 with only 1.0.0 on disk")
	}
	var diag, out bytes.Buffer
	validateErr := validateRelease(context.Background(), rw, releasePath, nil, compilerops.VerbosityDetailed, false, false, &diag, &out)
	if validateErr == nil {
		t.Fatalf("validate accepted test/svc:2.0.0 with only 1.0.0 on disk; the pull refused it:\n%v", pullErr)
	}
	const sentence = `no catalog version satisfies the referenced version constraint: entity "test/svc:2.0.0"`
	for _, got := range []struct {
		surface string
		err     error
		text    string
	}{
		{"pull", pullErr, pullErr.Error()},
		{"validate", validateErr, validateErr.Error() + "\n" + diag.String()},
	} {
		if !errors.Is(got.err, catalog.ErrUnsatisfiableEntityVersion()) {
			t.Errorf("the %s refusal does not carry the unsatisfiable-version condition:\n%s", got.surface, got.text)
		}
		if errors.Is(got.err, closure.ErrUnresolvableURI()) {
			t.Errorf("the %s refusal reports the entity as missing, though the catalog holds it at another version:\n%s",
				got.surface, got.text)
		}
		if !strings.Contains(got.text, sentence) {
			t.Errorf("the %s refusal does not say %q:\n%s", got.surface, sentence, got.text)
		}
	}
}
