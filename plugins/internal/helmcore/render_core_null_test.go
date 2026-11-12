// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package helmcore_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	"helm.sh/helm/v4/pkg/action"
	release "helm.sh/helm/v4/pkg/release/v1"
	releaseutil "helm.sh/helm/v4/pkg/release/v1/util"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/helmcore"
)

// nullChartFiles is a chart whose two ConfigMaps print the values each chart's
// templates read: parent-values carries the parent chart's .Values, sub-values
// the dependency's. Each default sits where one kind of null is aimed at it — a
// top-level key, a key inside a table, a key of the dependency's values — beside
// a sibling the null must leave alone, and extraArgs is a table the chart leaves
// empty, the shape of a flag map whose entries the chart has no default for.
func nullChartFiles() map[string]string {
	const printValues = "data:\n  values.yaml: |\n{{ toYaml .Values | indent 4 }}\n"
	return map[string]string{
		"Chart.yaml": "apiVersion: v2\nname: nulls\nversion: 0.1.0\n" +
			"dependencies:\n  - name: sub\n    version: 0.1.0\n",
		"values.yaml": "top: kept\n" +
			"sibling: kept\n" +
			"probe:\n  httpGet:\n    path: /healthz\n    port: 8080\n  initialDelaySeconds: 5\n" +
			"extraArgs: {}\n",
		"templates/values.yaml":  "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: parent-values\n" + printValues,
		"charts/sub/Chart.yaml":  "apiVersion: v2\nname: sub\nversion: 0.1.0\n",
		"charts/sub/values.yaml": "subKey: from-sub\nsubOther: kept\n",
		"charts/sub/templates/values.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: sub-values\n" +
			printValues,
	}
}

// nullCase is one set of supplied values and what each chart's templates must
// read under it. Each check names the ConfigMap (parent-values or sub-values), a
// key path inside the values it prints, and either the value that path holds
// or that the path is absent. A want of nil with absent false is a key present
// with a null value.
type nullCase struct {
	name     string
	supplied func() map[string]any
	checks   []nullCheck
}

type nullCheck struct {
	configMap string
	path      []string
	absent    bool
	want      any
}

func nullCases() []nullCase {
	return []nullCase{
		{
			// The positive control: every default the other rows aim a null at
			// reaches the templates when nothing is supplied, so a row seeing a key
			// absent is seeing the null remove it rather than a fixture that never
			// had it.
			name:     "nothing supplied renders every default",
			supplied: func() map[string]any { return map[string]any{} },
			checks: []nullCheck{
				{configMap: "parent-values", path: []string{"top"}, want: "kept"},
				{configMap: "parent-values", path: []string{"probe", "httpGet", "path"}, want: "/healthz"},
				{configMap: "parent-values", path: []string{"extraArgs"}, want: map[string]any{}},
				{configMap: "sub-values", path: []string{"subKey"}, want: "from-sub"},
			},
		},
		{
			name:     "a null at a top-level key removes the chart's default for it",
			supplied: func() map[string]any { return map[string]any{"top": nil} },
			checks: []nullCheck{
				{configMap: "parent-values", path: []string{"top"}, absent: true},
				{configMap: "parent-values", path: []string{"sibling"}, want: "kept"},
			},
		},
		{
			name: "a null at a key inside a table removes that default and keeps the table's other keys",
			supplied: func() map[string]any {
				return map[string]any{"probe": map[string]any{"httpGet": nil}}
			},
			checks: []nullCheck{
				{configMap: "parent-values", path: []string{"probe", "httpGet"}, absent: true},
				{configMap: "parent-values", path: []string{"probe", "initialDelaySeconds"}, want: 5},
			},
		},
		{
			name: "a null at a key of a dependency's values removes the dependency's default for it",
			supplied: func() map[string]any {
				return map[string]any{"sub": map[string]any{"subKey": nil}}
			},
			checks: []nullCheck{
				{configMap: "sub-values", path: []string{"subKey"}, absent: true},
				{configMap: "sub-values", path: []string{"subOther"}, want: "kept"},
			},
		},
		{
			name: "a null at a key the chart gives no default reaches the templates as null",
			supplied: func() map[string]any {
				return map[string]any{
					"extraArgs":      map[string]any{"web.enable-feature": nil},
					"unknownToChart": nil,
				}
			},
			checks: []nullCheck{
				{configMap: "parent-values", path: []string{"extraArgs", "web.enable-feature"}, want: nil},
				{configMap: "parent-values", path: []string{"unknownToChart"}, want: nil},
			},
		},
	}
}

// printedValues reads the values a named ConfigMap prints from a stream of
// rendered documents.
func printedValues(t *testing.T, docs []string, configMap string) map[string]any {
	t.Helper()
	for _, d := range docs {
		var cm struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
			Data map[string]string `yaml:"data"`
		}
		if err := yaml.Unmarshal([]byte(d), &cm); err != nil {
			t.Fatalf("parse a rendered document: %v\n%s", err, d)
		}
		if cm.Kind != "ConfigMap" || cm.Metadata.Name != configMap {
			continue
		}
		var vals map[string]any
		if err := yaml.Unmarshal([]byte(cm.Data["values.yaml"]), &vals); err != nil {
			t.Fatalf("parse the values %s prints: %v\n%s", configMap, err, cm.Data["values.yaml"])
		}
		return vals
	}
	t.Fatalf("no rendered ConfigMap is named %q", configMap)
	return nil
}

// lookup walks path through nested maps, reporting whether the last key is
// present and what it holds.
func lookup(vals map[string]any, path []string) (any, bool) {
	var cur any = vals
	for _, k := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = m[k]; !ok {
			return nil, false
		}
	}
	return cur, true
}

// renderThroughCore renders the fixture through the render core and returns
// the documents it emitted.
func renderThroughCore(t *testing.T, supplied map[string]any) []string {
	t.Helper()
	files, err := helmcore.RenderLoadedChart(context.Background(), chartFromFiles(t, nullChartFiles()),
		supplied, nodeCtx("nulls", "inline"), helmcore.NewHermeticValuesValidator(forbidFetch(t)))
	if err != nil {
		t.Fatalf("render through the render core: %v", err)
	}
	docs := make([]string, 0, len(files))
	for _, k := range sortedFileKeys(files) {
		docs = append(docs, string(files[k]))
	}
	return docs
}

// renderThroughHelmTemplate renders the fixture through the Helm SDK's install
// action in client-only dry-run mode — the action and the settings `helm
// template` drives — and returns the documents of the manifest it produced.
func renderThroughHelmTemplate(t *testing.T, supplied map[string]any) []string {
	t.Helper()
	install := action.NewInstall(action.NewConfiguration())
	install.DryRunStrategy = action.DryRunClient
	install.ReleaseName = "release-name"
	install.Replace = true
	rel, err := install.RunWithContext(context.Background(), chartFromFiles(t, nullChartFiles()), supplied)
	if err != nil {
		t.Fatalf("render through the install action: %v", err)
	}
	var manifest string
	switch r := rel.(type) {
	case *release.Release:
		manifest = r.Manifest
	case release.Release:
		manifest = r.Manifest
	default:
		t.Fatalf("the install action returned a release of type %T", rel)
	}
	// The documents are searched by name, so their order is not read.
	split := releaseutil.SplitManifests(manifest)
	docs := make([]string, 0, len(split))
	for _, d := range split {
		if strings.TrimSpace(d) != "" {
			docs = append(docs, d)
		}
	}
	return docs
}

// TestRenderChart_ANullRemovesTheChartsDefaultAsAHelmInstallDoes pins how a null
// in the supplied values meets a chart's defaults: the values are coalesced over
// the defaults once, so a null removes the default it is aimed at — at the top
// level, inside a table and in a dependency's values — and leaves every other
// key alone, while a null at a key the chart gives no default reaches the
// templates as null. A second coalescing pass would find each removed key absent
// and restore its default, which the absent rows refuse.
func TestRenderChart_ANullRemovesTheChartsDefaultAsAHelmInstallDoes(t *testing.T) {
	t.Parallel()
	for _, tc := range nullCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			docs := renderThroughCore(t, tc.supplied())
			for _, c := range tc.checks {
				got, ok := lookup(printedValues(t, docs, c.configMap), c.path)
				switch {
				case c.absent && ok:
					t.Errorf("%s: %s is %v, but the supplied null should have removed the chart's default for it",
						c.configMap, strings.Join(c.path, "."), got)
				case !c.absent && !ok:
					t.Errorf("%s: %s is absent, want %v", c.configMap, strings.Join(c.path, "."), c.want)
				case !c.absent && !reflect.DeepEqual(got, c.want):
					t.Errorf("%s: %s is %#v, want %#v", c.configMap, strings.Join(c.path, "."), got, c.want)
				}
			}
		})
	}
}

// TestRenderChart_TheValuesATemplateReadsAreTheOnesHelmTemplateGivesIt holds the
// render core to the Helm SDK's own install action for every row above: for the
// same chart and the same supplied values, each chart's templates read the same
// values in both. The comparison is over the values each template prints, which
// is what the coalescing decides; the release name, the namespace and the labels
// the render core writes are the render's own and are not compared.
func TestRenderChart_TheValuesATemplateReadsAreTheOnesHelmTemplateGivesIt(t *testing.T) {
	t.Parallel()
	for _, tc := range nullCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ours := renderThroughCore(t, tc.supplied())
			helms := renderThroughHelmTemplate(t, tc.supplied())
			for _, cm := range []string{"parent-values", "sub-values"} {
				got, want := printedValues(t, ours, cm), printedValues(t, helms, cm)
				if !reflect.DeepEqual(got, want) {
					t.Errorf("%s: the render core's templates read\n%#v\nand Helm's install action's read\n%#v", cm, got, want)
				}
			}
		})
	}
}
