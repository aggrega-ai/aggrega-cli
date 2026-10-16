// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore_test

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/helmcore"
	"helm.sh/helm/v4/pkg/chart/loader/archive"
	chart "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
)

// The value the fixture's input marks sensitive, and one it does not. The marked
// one is long enough to satisfy the fixture schema's minimum length while the
// placeholder is not, which is what lets a row show a substituted run failing
// somewhere the real one did not.
const (
	withheldMarkedValue   = "PLANTED-MARKED-VALUE"
	withheldUnmarkedValue = "UNMARKED-NOTE"
)

// chartLoaderFor returns a loader building a fresh in-memory chart from files on
// every call, the way the provider guest builds one from its packed source.
func chartLoaderFor(files map[string]string) helmcore.ChartLoader {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return func() (*chart.Chart, error) {
		bufs := make([]*archive.BufferedFile, 0, len(names))
		for _, name := range names {
			bufs = append(bufs, &archive.BufferedFile{Name: name, Data: []byte(files[name])})
		}
		return loader.LoadFiles(bufs)
	}
}

// withheldFixtureFiles is the fixture chart with extra files laid over it.
func withheldFixtureFiles(extra map[string]string) map[string]string {
	files := map[string]string{
		"Chart.yaml":  "apiVersion: v2\nname: vault\nversion: 0.1.0\n",
		"values.yaml": "adminPassword: \"\"\nadminNote: \"\"\nreplicas: 1\n",
	}
	for name, content := range extra {
		files[name] = content
	}
	return files
}

// nestedValues is a values map the way a values mapping writes one — keys of the
// mapping's own invention, nested — carrying the value the input marks; and
// nestedPasswordSchema refuses that value where it sits.
const (
	nestedValues         = `{"auth":{"password":"` + withheldMarkedValue + `"},"replicas":1}`
	nestedPasswordSchema = `{"type":"object","properties":{"auth":{"type":"object","properties":{"password":{"type":"string","pattern":"^ref:"}}}}}`
)

// configMapTemplate is a template emitting one ConfigMap named name, carrying
// body under data.
func configMapTemplate(name, body string) string {
	return "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: " + name + "\ndata:\n" + body
}

// crossingText is the failure text a guest reports for err — the text every
// surface prints for a plugin's failure.
func crossingText(err error) string {
	return rendercore.FailureOf(err).Message
}

// TestRenderChartWithholding_KeepsAMarkedValueOutOfTheFailure renders one chart
// per row twice — plainly, and through the withholding entry point with the
// node's marking — and holds the second to the contract: a failure carries no
// value the input marks sensitive, and a failure quoting none is byte-identical
// to the plain render's. Each row that withholds first proves the plain render
// does quote the value, so a row cannot pass over a failure that never carried it.
func TestRenderChartWithholding_KeepsAMarkedValueOutOfTheFailure(t *testing.T) {
	marked := map[string]string{"adminPassword": "values.adminPassword"}
	values := `{"adminPassword":"` + withheldMarkedValue + `","adminNote":"` + withheldUnmarkedValue + `","replicas":1}`

	tests := []struct {
		name   string
		files  map[string]string
		values string
		marked map[string]string
		// sensitiveValues: the input marks the values as a whole — a values mapping
		// that read a declared value produced them.
		sensitiveValues bool
		// renders: the render succeeds and its output is the plain render's.
		renders bool
		// identical: the failure's text is the plain render's, byte for byte.
		identical bool
		// plainQuotes: the plain render's failure carries the marked value.
		plainQuotes bool
		want        []string
		absent      []string
		// sameCondition: the failure carries the plain render's registered condition.
		sameCondition bool
	}{
		{
			name: "a render that succeeds is the plain render",
			files: map[string]string{"templates/a.yaml": configMapTemplate("cm",
				"  password: {{ .Values.adminPassword | quote }}\n")},
			values: values, marked: marked, renders: true,
		},
		{
			name:   "a fail quoting the marked value keeps the chart's sentence with the placeholder",
			files:  map[string]string{"templates/a.yaml": `{{ fail (printf "the admin password %s must be supplied as a reference" .Values.adminPassword) }}`},
			values: values, marked: marked, plainQuotes: true,
			want: []string{
				"the admin password " + redact.Placeholder() + " must be supplied as a reference",
				"execution error at (vault/templates/a.yaml:",
			},
		},
		{
			name:   "a fail quoting an unmarked value is byte-identical",
			files:  map[string]string{"templates/a.yaml": `{{ fail (printf "the admin note %s must not be written inline" .Values.adminNote) }}`},
			values: values, marked: marked, identical: true,
			want: []string{withheldUnmarkedValue},
		},
		{
			name:   "a fail reading the marked value without quoting it is byte-identical",
			files:  map[string]string{"templates/a.yaml": `{{ if .Values.adminPassword }}{{ fail "the admin password must not be written inline" }}{{ end }}`},
			values: values, marked: marked, identical: true,
		},
		{
			name:   "a fail whose branch depends on the marked value withholds the sentence at its position",
			files:  map[string]string{"templates/a.yaml": `{{ if hasPrefix "PLANTED" .Values.adminPassword }}{{ fail (printf "the admin password %s is a planted literal" .Values.adminPassword) }}{{ end }}`},
			values: values, marked: marked, plainQuotes: true,
			want: []string{"execution error at (vault/templates/a.yaml:", "failure text withheld"},
		},
		{
			name:   "a fault the engine raises on a branch the marked value decides is withheld at its position",
			files:  map[string]string{"templates/a.yaml": `{{ if hasPrefix "PLANTED" .Values.adminPassword }}{{ .Values.auth.password }}{{ end }}`},
			values: values, marked: marked,
			want: []string{"execution error at (vault/templates/a.yaml:1:", "failure text withheld"},
		},
		{
			name: "a values-schema finding on the marked value carries the placeholder",
			files: map[string]string{
				"values.schema.json": `{"type":"object","properties":{"adminPassword":{"type":"string","pattern":"^ref:"}}}`,
				"templates/a.yaml":   configMapTemplate("cm", "  replicas: {{ .Values.replicas | quote }}\n"),
			},
			values: values, marked: marked, plainQuotes: true,
			want: []string{redact.Placeholder(), "pattern"},
		},
		{
			name: "a schema finding elsewhere is withheld rather than replaced by one the placeholder raised",
			files: map[string]string{
				"values.schema.json": `{"type":"object","properties":{"adminPassword":{"type":"string","minLength":12},"replicas":{"type":"integer","minimum":1}}}`,
				"templates/a.yaml":   configMapTemplate("cm", "  replicas: {{ .Values.replicas | quote }}\n"),
			},
			values: `{"adminPassword":"` + withheldMarkedValue + `","replicas":0}`, marked: marked,
			want:   []string{"failure text withheld", "values do not meet the chart's schema at vault at '/replicas' (minimum)"},
			absent: []string{"minLength"},
		},
		{
			name: "a rendered document the marked value breaks is withheld",
			files: map[string]string{"templates/a.yaml": configMapTemplate("cm",
				"  password: *{{ .Values.adminPassword }}\n")},
			values: values, marked: marked, plainQuotes: true,
			want: []string{"failure text withheld"},
		},
		{
			name: "an output collision a marked value names is withheld with its condition kept",
			files: map[string]string{
				"templates/a.yaml": configMapTemplate("{{ .Values.adminPassword | lower }}", "  copy: \"1\"\n"),
				"templates/b.yaml": configMapTemplate("{{ .Values.adminPassword | lower }}", "  copy: \"2\"\n"),
			},
			values: values, marked: marked, plainQuotes: true, sameCondition: true,
			want: []string{"failure text withheld"},
		},
		{
			name:   "a marking naming no key of the values leaves the plain render's failure",
			files:  map[string]string{"templates/a.yaml": `{{ fail (printf "the admin password %s must be supplied as a reference" .Values.adminPassword) }}`},
			values: values, marked: map[string]string{"absentKey": "values.absentKey"}, identical: true,
			want: []string{withheldMarkedValue},
		},
		{
			name: "a values-schema finding on a nested value of a map marked whole carries the placeholder",
			files: map[string]string{
				"values.schema.json": nestedPasswordSchema,
				"templates/a.yaml":   configMapTemplate("cm", "  ready: \"true\"\n"),
			},
			values: nestedValues, sensitiveValues: true, plainQuotes: true,
			want: []string{"at '/auth/password': '" + redact.Placeholder() + "' does not match pattern '^ref:'"},
		},
		{
			name:   "a fail quoting a nested value of a map marked whole keeps the chart's sentence with the placeholder",
			files:  map[string]string{"templates/a.yaml": `{{ fail (printf "the password %s must be a reference" .Values.auth.password) }}`},
			values: nestedValues, sensitiveValues: true, plainQuotes: true,
			want: []string{"the password " + redact.Placeholder() + " must be a reference", "execution error at (vault/templates/a.yaml:"},
		},
		{
			name: "a schema finding a map marked whole changes under the placeholder is withheld with its location",
			files: map[string]string{
				"values.schema.json": `{"type":"object","properties":{"replicas":{"type":"integer","minimum":1}}}`,
				"templates/a.yaml":   configMapTemplate("cm", "  ready: \"true\"\n"),
			},
			values: `{"auth":{"password":"` + withheldMarkedValue + `"},"replicas":0}`, sensitiveValues: true,
			want: []string{"failure text withheld", "values do not meet the chart's schema at vault at '/replicas' (minimum)"},
		},
		{
			name: "a rendered document a nested value of a map marked whole breaks is withheld",
			files: map[string]string{"templates/a.yaml": configMapTemplate("cm",
				"  password: *{{ .Values.auth.password }}\n")},
			values: nestedValues, sensitiveValues: true, plainQuotes: true,
			want: []string{"failure text withheld"},
		},
		{
			name: "a map the input does not mark leaves the plain render's failure",
			files: map[string]string{
				"values.schema.json": nestedPasswordSchema,
				"templates/a.yaml":   configMapTemplate("cm", "  ready: \"true\"\n"),
			},
			values: nestedValues, identical: true,
			want: []string{withheldMarkedValue},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			nodeCtx := rendercore.NodeRenderContext{NodeID: "api", SensitiveValues: tc.sensitiveValues}
			load := chartLoaderFor(withheldFixtureFiles(tc.files))

			plainChart, err := load()
			if err != nil {
				t.Fatalf("load fixture chart: %v", err)
			}
			plainOut, plainErr := helmcore.RenderLoadedChart(ctx, plainChart, decodeValues(t, tc.values), nodeCtx,
				helmcore.NewHermeticValuesValidator(forbidFetch(t)))
			gotRender, gotErr := helmcore.RenderChartWithholding(ctx, load, decodeValues(t, tc.values), nodeCtx,
				helmcore.NewHermeticValuesValidator(forbidFetch(t)), tc.marked)
			gotOut := gotRender.Files

			if tc.renders {
				if plainErr != nil || gotErr != nil {
					t.Fatalf("render failed: plain %v, withholding %v", plainErr, gotErr)
				}
				if !reflect.DeepEqual(plainOut, gotOut) {
					t.Fatalf("output differs from the plain render's")
				}
				return
			}
			if plainErr == nil || gotErr == nil {
				t.Fatalf("render succeeded: plain %v, withholding %v", plainErr, gotErr)
			}
			plain, got := crossingText(plainErr), crossingText(gotErr)

			if tc.plainQuotes && !strings.Contains(strings.ToLower(plain), strings.ToLower(withheldMarkedValue)) {
				t.Fatalf("the plain render's failure does not carry the marked value, so this row proves nothing: %s", plain)
			}
			if tc.identical {
				if got != plain {
					t.Errorf("failure text moved:\n got: %s\nwant: %s", got, plain)
				}
			} else if strings.Contains(strings.ToLower(got), strings.ToLower(withheldMarkedValue)) {
				t.Errorf("failure carries the marked value: %s", got)
			}
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("failure does not carry %q: %s", want, got)
				}
			}
			for _, absent := range tc.absent {
				if strings.Contains(got, absent) {
					t.Errorf("failure carries %q: %s", absent, got)
				}
			}
			if tc.sameCondition {
				plainCode, plainCoded := cerrors.CodeOf(plainErr)
				gotCode, gotCoded := cerrors.CodeOf(gotErr)
				if !plainCoded || !gotCoded || plainCode != gotCode {
					t.Errorf("condition = %q (%v), want the plain render's %q (%v)", gotCode, gotCoded, plainCode, plainCoded)
				}
			}
		})
	}
}
