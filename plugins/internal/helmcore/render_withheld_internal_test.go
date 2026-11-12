// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package helmcore

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"helm.sh/helm/v4/pkg/chart/common"
	"helm.sh/helm/v4/pkg/chart/loader/archive"
	chart "helm.sh/helm/v4/pkg/chart/v2"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
)

// TestTemplatePosition_ReadsEveryFormTheSDKReportsATemplateFailureIn renders a
// chart failing in each of the forms the Helm SDK reports a template failure in,
// and holds that the position read from the real failure is the failing template's
// file, line and column and nothing more. Each row first proves the failure is in
// the form the row names, so a row cannot pass over a form the SDK no longer
// produces — which is exactly how a parse written for one SDK release stops
// recognising the next one's failures without any test noticing.
func TestTemplatePosition_ReadsEveryFormTheSDKReportsATemplateFailureIn(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		// files are laid over a Chart.yaml naming the chart vault.
		files map[string]string
		// form is a fragment only the failure form the row exercises carries.
		form func(text string) bool
		// position matches the whole position the failure must yield.
		position *regexp.Regexp
	}{
		{
			name:     "a chart's own fail",
			files:    map[string]string{"templates/a.yaml": "x: 1\n{{ fail \"the chart refuses\" }}\n"},
			form:     func(text string) bool { return strings.HasPrefix(text, "execution error at (") },
			position: regexp.MustCompile(`^vault/templates/a\.yaml:2:\d+$`),
		},
		{
			name:     "a fault the engine raises is read from the first line of its trace",
			files:    map[string]string{"templates/a.yaml": "x: 1\ny: {{ .Values.auth.password }}\n"},
			form:     func(text string) bool { return strings.Contains(text, "\n  executing \"") },
			position: regexp.MustCompile(`^vault/templates/a\.yaml:2:\d+$`),
		},
		{
			name: "a fault inside an included template is read at the outermost template",
			files: map[string]string{
				"templates/_helpers.tpl": "{{- define \"vault.password\" -}}\n{{ .Values.auth.password }}\n{{- end -}}\n",
				"templates/a.yaml":       "x: 1\ny: {{ include \"vault.password\" . }}\n",
			},
			form: func(text string) bool {
				return strings.Contains(text, "\n  executing \"") && strings.Contains(text, "vault/templates/_helpers.tpl:")
			},
			position: regexp.MustCompile(`^vault/templates/a\.yaml:2:\d+$`),
		},
		{
			name:     "an error a template function returns keeps the engine's own text",
			files:    map[string]string{"templates/a.yaml": "x: 1\ny: {{ mustRegexMatch \"(\" \"x\" }}\n"},
			form:     func(text string) bool { return strings.HasPrefix(text, "template: ") },
			position: regexp.MustCompile(`^vault/templates/a\.yaml:2:\d+$`),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			files := map[string]string{"Chart.yaml": "apiVersion: v2\nname: vault\nversion: 0.1.0\n"}
			for name, content := range tc.files {
				files[name] = content
			}
			names := make([]string, 0, len(files))
			for name := range files {
				names = append(names, name)
			}
			sort.Strings(names)
			bufs := make([]*archive.BufferedFile, 0, len(names))
			for _, name := range names {
				bufs = append(bufs, &archive.BufferedFile{Name: name, Data: []byte(files[name])})
			}
			ch, err := loader.LoadFiles(bufs)
			if err != nil {
				t.Fatalf("load the fixture chart: %v", err)
			}

			_, err = RenderChart(context.Background(), ch, map[string]any{}, rendercore.NodeRenderContext{NodeID: "api"},
				func(*chart.Chart, common.Values) error { return nil })
			if err == nil {
				t.Fatal("the fixture chart rendered; want a template failure")
			}
			_, cause := chartFailureStage(err)
			text := cause.Error()
			if !tc.form(text) {
				t.Fatalf("the SDK reported this failure in another form than the row exercises, so the row proves nothing:\n%s", text)
			}
			if got := templatePosition(cause); !tc.position.MatchString(got) {
				t.Errorf("read the position %q from the failure, want one matching %s:\n%s", got, tc.position, text)
			}
		})
	}
}
