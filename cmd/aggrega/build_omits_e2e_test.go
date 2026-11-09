// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// End-to-end coverage of a Recipe's omits key through the command's own build: a
// chart fetched or copied from elsewhere generates a value at render time, so two
// builds differ and the build cannot be reproduced; leaving the generated document
// out restores it. Every proof here builds through pin, pull and the build dispatch
// the command runs, renders through the embedded helm provider, and reads the
// output it wrote.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// omitsChart selects what the fixture chart's token Secret and seed ConfigMap
// hold: values generated afresh on every render, or fixed text.
type omitsChart int

const (
	chartGeneratesValues omitsChart = iota
	chartFixedValues
)

// omitsWorkspace authors, pins and pulls a workspace whose one component renders
// through a Recipe sourcing a local chart of three documents — the Secret
// <release>-token, the ConfigMap <release>-seed and the ConfigMap
// <release>-settings — with recipeTail spliced into the Recipe's spec verbatim. It
// returns the Release path and the catalog directory a build is handed.
func omitsWorkspace(t *testing.T, root string, chart omitsChart, recipeTail string) (releasePath, catalogDir string) {
	t.Helper()
	catalogDir = filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    app:
      type: server
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: omits-rel
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        app: {}
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
        server: server
`)
	writeFixture(t, filepath.Join(catalogDir, "components", "server.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values: {}
`)
	writeHelmRendererPlugin(t, filepath.Join(catalogDir, "providers"))

	token, seed := `{{ randAlphaNum 24 | quote }}`, `{{ randInt 0 1000000000 | quote }}`
	if chart == chartFixedValues {
		token, seed = "supplied-by-the-operator", `"42"`
	}
	chartDir := filepath.Join(catalogDir, "recipes", "charts", "token")
	writeFixture(t, filepath.Join(chartDir, "Chart.yaml"), "apiVersion: v2\nname: token\nversion: 0.1.0\n")
	writeFixture(t, filepath.Join(chartDir, "templates", "secret.yaml"), `apiVersion: v1
kind: Secret
metadata:
  name: {{ .Release.Name }}-token
stringData:
  token: `+token+"\n")
	writeFixture(t, filepath.Join(chartDir, "templates", "seed.yaml"), `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Release.Name }}-seed
data:
  seed: `+seed+"\n")
	writeFixture(t, filepath.Join(chartDir, "templates", "settings.yaml"), `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Release.Name }}-settings
data:
  level: info
`)
	recipePath := filepath.Join(catalogDir, "recipes", "server.yaml")
	writeFixture(t, recipePath, `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: server
  version: "1.0.0"
spec:
  capability: helm
  implements: server
  source: ./charts/token
`+recipeTail)

	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	if err := pinRun(context.Background(), io.Discard, ws, recipePath, false); err != nil {
		t.Fatalf("pin the fixture's Recipe: %v", err)
	}
	releasePath = filepath.Join(root, "release.yaml")
	if err := pullRun(context.Background(), pullConfig{ws: ws, ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
		t.Fatalf("prime the lockfile via pull: %v", err)
	}
	return releasePath, catalogDir
}

// buildOmitsWorkspace runs the command's build over the workspace at root, into
// root/dist, with the determinism check on when verify is set.
func buildOmitsWorkspace(t *testing.T, root, releasePath, catalogDir string, verify bool) error {
	t.Helper()
	return dispatchBuildWithDiag(context.Background(),
		resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		releasePath, []string{catalogDir}, false,
		buildDiagOptions{},
		rootedBuildOptions(t, root, buildops.RunOptions{VerifyDeterminism: verify}),
		io.Discard, io.Discard)
}

// builtTree builds the workspace at root and returns every file the build wrote,
// keyed by its path under the output directory.
func builtTree(t *testing.T, root, releasePath, catalogDir string) map[string]string {
	t.Helper()
	if err := buildOmitsWorkspace(t, root, releasePath, catalogDir, false); err != nil {
		t.Fatalf("build: %s", cerrors.CauseChain(err))
	}
	return readTree(t, filepath.Join(root, "dist"))
}

// omitsGeneratedDocuments leaves out both documents the chart generates a value in.
const omitsGeneratedDocuments = `  omits:
    - kind: Secret
      name: "{{ .context.rendered_name }}-token"
    - kind: ConfigMap
      name: "{{ .context.rendered_name }}-seed"
`

// TestBuild_OmitsLeaveRenderTimeValuesOutAndTheBuildRepeats is the reason the key
// exists, read in three parts. Without the key, two builds of a chart generating
// values at render time write different trees — which is also what shows the
// comparison below can see a difference — and the determinism check halts the
// build. With entries leaving the two generated documents out, two builds write the
// same tree, the determinism check passes, and the one document kept is byte for
// byte the one the build without the key wrote.
func TestBuild_OmitsLeaveRenderTimeValuesOutAndTheBuildRepeats(t *testing.T) {
	t.Parallel()

	plainRoot := t.TempDir()
	plainRelease, plainCatalog := omitsWorkspace(t, plainRoot, chartGeneratesValues, "")
	first := builtTree(t, plainRoot, plainRelease, plainCatalog)
	second := builtTree(t, plainRoot, plainRelease, plainCatalog)
	if maps.Equal(first, second) {
		t.Fatalf("two builds of a chart generating values at render time wrote the same tree, so the comparison "+
			"below cannot tell two trees apart:\n%v", slices.Sorted(maps.Keys(first)))
	}
	if err := buildOmitsWorkspace(t, plainRoot, plainRelease, plainCatalog, true); !errors.Is(err, renderer.ErrRenderNondeterministic()) {
		t.Fatalf("the determinism check over a chart generating values at render time: got %v, want the non-deterministic render halt", err)
	}

	root := t.TempDir()
	releasePath, catalogDir := omitsWorkspace(t, root, chartGeneratesValues, omitsGeneratedDocuments)
	once := builtTree(t, root, releasePath, catalogDir)
	twice := builtTree(t, root, releasePath, catalogDir)
	if !maps.Equal(once, twice) {
		t.Errorf("two builds leaving the generated documents out wrote different trees:\n%v\n%v", once, twice)
	}
	if err := buildOmitsWorkspace(t, root, releasePath, catalogDir, true); err != nil {
		t.Errorf("the determinism check over the build leaving the generated documents out: %s", cerrors.CauseChain(err))
	}

	kept := slices.Sorted(maps.Keys(once))
	if len(kept) != 1 || !strings.Contains(once[kept[0]], "name: app-settings") {
		t.Fatalf("the build leaving the generated documents out wrote %v, want the settings document alone", kept)
	}
	if want, written := first[kept[0]]; !written || want != once[kept[0]] {
		t.Errorf("the kept document %s differs from the one the build without the key wrote:\n%s\nwant:\n%s",
			kept[0], once[kept[0]], want)
	}
}

// TestBuild_AnOmitsEntryThatMatchesNothingHaltsNamingIt holds the halt on an active
// entry the provider matched to nothing: the second entry names a Secret the chart
// does not render, and the build stops on it — not on the first, which matched —
// naming the node, the provider, the entry's position in the Recipe and the kind,
// name and namespace it resolved to.
func TestBuild_AnOmitsEntryThatMatchesNothingHaltsNamingIt(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	releasePath, catalogDir := omitsWorkspace(t, root, chartFixedValues, `  omits:
    - kind: Secret
      name: "{{ .context.rendered_name }}-token"
    - kind: Secret
      name: "{{ .context.rendered_name }}-missing"
      namespace: apps
`)
	err := buildOmitsWorkspace(t, root, releasePath, catalogDir, false)
	if !errors.Is(err, renderer.ErrOmitMatchedNothing()) {
		t.Fatalf("an entry naming a Secret the chart does not render: got %v, want the halt on an entry that left nothing out", err)
	}
	chain := cerrors.CauseChain(err)
	for _, want := range []string{
		renderer.ErrOmitMatchedNothing().Error(),
		`node "app"`, `provider "provider/helm:`, `"server" omits[1]`,
		`kind "Secret"`, `name "app-missing"`, `namespace "apps"`,
	} {
		if !strings.Contains(chain, want) {
			t.Errorf("the halt does not carry %q:\n%s", want, chain)
		}
	}
	if strings.Contains(chain, "omits[0]") {
		t.Errorf("the halt names the entry that matched:\n%s", chain)
	}
}

// TestBuild_AnOmitsEntryWhoseWhenIsFalsyLeavesTheBuildAsWithoutTheKey holds an
// entry its when drops: the build writes the tree the build with no key writes,
// file for file. The same entry with a true when changes the tree, which is what
// shows the comparison can see the entry at all.
func TestBuild_AnOmitsEntryWhoseWhenIsFalsyLeavesTheBuildAsWithoutTheKey(t *testing.T) {
	t.Parallel()

	entry := func(when string) string {
		return `  omits:
    - when: '` + when + `'
      kind: Secret
      name: "{{ .context.rendered_name }}-token"
`
	}
	tree := func(tail string) map[string]string {
		root := t.TempDir()
		releasePath, catalogDir := omitsWorkspace(t, root, chartFixedValues, tail)
		return builtTree(t, root, releasePath, catalogDir)
	}

	without := tree("")
	if active := tree(entry(`{{ eq .context.rendered_name "app" }}`)); maps.Equal(active, without) {
		t.Fatalf("an entry whose when is true left the tree as the build without the key wrote it, so the "+
			"comparison below cannot see an entry:\n%v", slices.Sorted(maps.Keys(active)))
	}
	if dropped := tree(entry(`{{ eq .context.rendered_name "nobody" }}`)); !maps.Equal(dropped, without) {
		t.Errorf("an entry whose when is false changed the tree:\n%v\nwant:\n%v",
			slices.Sorted(maps.Keys(dropped)), slices.Sorted(maps.Keys(without)))
	}
}

// TestBuild_OmitsOnAProviderThatRendersNothingAtBuildTimeIsRefused holds the
// refusal of a Recipe declaring omits whose provider's declared contract does not
// claim to honour it. helm-chart and terraform hand their source on to be rendered
// at deploy time, so neither claims it, and each is refused before anything renders,
// naming the provider and the node and positioned at the component the Aggregate
// wrote — the position the rule declared over the build's described facts carries,
// which no refusal made while a node renders has.
func TestBuild_OmitsOnAProviderThatRendersNothingAtBuildTimeIsRefused(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		capability string
		source     map[string]string
	}{
		{"helm-chart", map[string]string{
			"Chart.yaml":            "apiVersion: v2\nname: token\nversion: 0.1.0\n",
			"templates/secret.yaml": "apiVersion: v1\nkind: Secret\nmetadata:\n  name: token\n",
		}},
		{"terraform", map[string]string{
			"main.tf": "variable \"x\" {\n  type    = string\n  default = \"y\"\n}\n",
		}},
	} {
		t.Run(tc.capability, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			catalogDir := filepath.Join(root, "catalog")
			writeFixture(t, filepath.Join(root, "aggregate.yaml"), "apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\n"+
				"metadata:\n  name: aggregate\n  version: \"1.0.0\"\nspec:\n  components:\n    app:\n      type: server\n")
			writeFixture(t, filepath.Join(root, "release.yaml"), "apiVersion: aggrega.ai/v1beta1\nkind: Release\n"+
				"metadata:\n  name: omits-refused\n  environment: testing\n  version: \"1.0.0\"\nspec:\n"+
				"  aggregate: ./aggregate.yaml\n  manifest: ./manifest.yaml\n  boundaries:\n    main:\n"+
				"      directory: ./dist/main\n      components:\n        app: {}\n")
			writeFixture(t, filepath.Join(root, "manifest.yaml"), "apiVersion: aggrega.ai/v1beta1\nkind: Manifest\n"+
				"metadata:\n  name: manifest\n  version: \"1.0.0\"\nspec:\n  mappings:\n    components:\n      type:\n        server: server\n")
			writeFixture(t, filepath.Join(catalogDir, "components", "server.yaml"), "apiVersion: aggrega.ai/v1beta1\n"+
				"kind: Component\nmetadata:\n  name: server\n  version: \"1.0.0\"\n  labels:\n"+
				"    component.aggrega.ai/type: \"server\"\nspec:\n  schema:\n    values: {}\n")
			seedEmbeddedPlugins(t, filepath.Join(catalogDir, "providers"), "provider/"+tc.capability)
			for rel, content := range tc.source {
				writeFixture(t, filepath.Join(catalogDir, "recipes", "source", filepath.FromSlash(rel)), content)
			}
			recipePath := filepath.Join(catalogDir, "recipes", "server.yaml")
			writeFixture(t, recipePath, "apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: server\n"+
				"  version: \"1.0.0\"\nspec:\n  capability: "+tc.capability+"\n  implements: server\n  source: ./source\n"+
				"  omits:\n    - kind: Secret\n      name: token\n")

			ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
			if err := pinRun(context.Background(), io.Discard, ws, recipePath, false); err != nil {
				t.Fatalf("pin the fixture's Recipe: %v", err)
			}
			releasePath := filepath.Join(root, "release.yaml")
			if err := pullRun(context.Background(), pullConfig{ws: ws, ReleasePath: releasePath, CatalogDirs: []string{catalogDir}}); err != nil {
				t.Fatalf("prime the lockfile via pull: %v", err)
			}
			err := buildOmitsWorkspace(t, root, releasePath, catalogDir, false)
			if !errors.Is(err, renderer.ErrUnsupportedOmits()) {
				t.Fatalf("a %s Recipe declaring omits: got %v, want the refusal of a provider that does not support omits", tc.capability, err)
			}
			chain := cerrors.CauseChain(err)
			for _, want := range []string{
				renderer.ErrUnsupportedOmits().Error() + ` ("` + tc.capability + `", node "app")`,
				"remove the omits key",
				"aggregate.yaml at .spec.components.app",
			} {
				if !strings.Contains(chain, want) {
					t.Errorf("the refusal does not carry %q:\n%s", want, chain)
				}
			}
		})
	}
}

// TestValidate_TheOmitsKeyNegativeCorpus drives one document per constraint the
// key's schema declares through the single-entity scope's load, which reports the
// document-envelope condition with the findings that name the constraint. Each row
// carries one fault; the control row is the same document with none, and its
// schema tier passes.
func TestValidate_TheOmitsKeyNegativeCorpus(t *testing.T) {
	t.Parallel()

	recipe := func(spec string) string {
		return "apiVersion: aggrega.ai/v1beta1\nkind: Recipe\nmetadata:\n  name: corpus\n  version: \"1.0.0\"\nspec:\n" + spec
	}
	featureRecipe := func(spec string) string {
		return "apiVersion: aggrega.ai/v1beta1\nkind: FeatureRecipe\nmetadata:\n  name: corpus\n  version: \"1.0.0\"\nspec:\n" + spec
	}
	const entries = "  omits:\n    - kind: Secret\n      name: token\n"
	const sourced = "  capability: helm\n  implements: server\n  source: ./chart\n"
	const workloadSpec = "  capability: helm\n  implements: server\n  workload: |\n    apiVersion: aggrega.ai/v1beta1\n" +
		"    kind: Workload\n    containers:\n      main:\n        image: x:1\n"

	for _, tc := range []struct {
		name, document, finding string
	}{
		{"a Recipe carrying omits and no source", recipe(workloadSpec + entries), `spec: dependentRequired["omits"]: missing properties ["source"]`},
		{"a misspelt entry field", recipe(sourced + entries + "      namspace: apps\n"), `spec.omits[0].namspace: unexpected property: "namspace"`},
		{"a value that is not a string", recipe(sourced + "  omits:\n    - kind: Secret\n      name: 42\n"), `spec.omits[0].name: type: 42 has type "integer", want "string"`},
		{"an empty list", recipe(sourced + "  omits: []\n"), "spec.omits: minItems: array length 0 is less than 1"},
		{"a FeatureRecipe carrying omits and no source", featureRecipe("  capability: helm\n  implements: core/f\n" + entries), `spec: dependentRequired["omits"]: missing properties ["source"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "entity.yaml")
			writeFixture(t, path, tc.document)
			var out bytes.Buffer
			err := dispatchValidate(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), path, "", nil, false, &out)
			if !errors.Is(err, envelope.ErrEntityEnvelope()) {
				t.Fatalf("got %v, want the document-envelope condition\noutput:\n%s", err, out.String())
			}
			if chain := cerrors.CauseChain(err); !strings.Contains(chain, tc.finding) {
				t.Errorf("the refusal does not name the constraint (%q):\n%s", tc.finding, chain)
			}
		})
	}

	t.Run("control: a Recipe declaring omits beside its source", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(t.TempDir(), "entity.yaml")
		writeFixture(t, path, recipe(sourced+entries))
		var out bytes.Buffer
		err := dispatchValidate(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{}), path, "", nil, false, &out)
		if errors.Is(err, envelope.ErrEntityEnvelope()) {
			t.Fatalf("the control is refused at the document envelope: %s", cerrors.CauseChain(err))
		}
		if !strings.Contains(out.String(), "Schema: valid") {
			t.Errorf("the control's schema tier did not pass:\n%s", out.String())
		}
	})
}
