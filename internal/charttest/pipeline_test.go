// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package charttest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/charttest"
)

// writeChartTree materialises a chart tree from a path-to-content map and
// returns its root. Paths are slash-separated and relative to that root.
func writeChartTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return root
}

const (
	validChartYAML = "apiVersion: v2\nname: probe\nversion: 0.1.0\n"
	validConfigMap = "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: probe\ndata:\n  key: value\n"
	notesText      = "Thank you for installing probe.\nRun `helm status` to see it.\n"
	partialTpl     = "{{- define \"probe.fullname\" -}}probe{{- end -}}\n"
)

// TestRenderThroughDependencyPipelineRefusals pins which refusals the helper
// reproduces and which it does not.
//
// Every refusing row carries ONE fault and asserts the class that fault pins,
// naming its own fixture path: the gates run in sequence, so a row bundling
// several faults would keep passing on an earlier gate's objection and stop
// covering its subject. The accepting rows are the positive controls — without
// them a helper that refused everything would pass every refusing row.
//
// The assertions match helm's own wrapper text, which is the only handle a
// caller has: the library returns unwrapped errors carrying no sentinel.
func TestRenderThroughDependencyPipelineRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// files is the chart tree.
		files map[string]string
		// wantErr, when non-empty, is a substring the refusal must carry. Empty
		// means the chart must be accepted.
		wantErr string
		// wantOut, when non-empty, is a substring the accepted render must carry.
		wantOut string
		// wantNoOut, when true, requires the accepted render to be empty.
		wantNoOut bool
	}{
		{
			name: "an unterminated double quote in a discovered template is refused",
			files: map[string]string{
				"Chart.yaml": validChartYAML,
				"templates/unterminated-quote.yaml": "apiVersion: v1\nkind: ConfigMap\n" +
					"metadata:\n  name: \"unterminated\n",
			},
			wantErr: "YAML parse error on probe/templates/unterminated-quote.yaml",
		},
		{
			name: "an unterminated flow sequence in a discovered template is refused",
			files: map[string]string{
				"Chart.yaml": validChartYAML,
				"templates/unterminated-seq.yaml": "apiVersion: v1\nkind: ConfigMap\n" +
					"metadata:\n  name: probe\ndata:\n  seq: [a, b\n",
			},
			wantErr: "YAML parse error on probe/templates/unterminated-seq.yaml",
		},
		{
			name: "a literal tab indenting a discovered template is refused",
			files: map[string]string{
				"Chart.yaml": validChartYAML,
				"templates/literal-tab.yaml": "apiVersion: v1\nkind: ConfigMap\n" +
					"metadata:\n  name: probe\ndata:\n\tkey: value\n",
			},
			wantErr: "YAML parse error on probe/templates/literal-tab.yaml",
		},
		{
			name: "a discovered template rendering something that is not a manifest is refused",
			files: map[string]string{
				"Chart.yaml":                    validChartYAML,
				"templates/not-a-manifest.yaml": "just a sentence, which is a YAML scalar rather than a manifest\n",
			},
			wantErr: "YAML parse error on probe/templates/not-a-manifest.yaml",
		},
		{
			name: "a dependency declared with no matching sub-chart is refused",
			files: map[string]string{
				"Chart.yaml": "apiVersion: v2\nname: probe\nversion: 0.1.0\n" +
					"dependencies:\n  - name: absent-sub\n    version: 0.1.0\n",
				"templates/cm.yaml": validConfigMap,
			},
			wantErr: "missing in charts/ directory: absent-sub",
		},
		{
			name: "a dependency declared with its sub-chart present is accepted",
			files: map[string]string{
				"Chart.yaml": "apiVersion: v2\nname: probe\nversion: 0.1.0\n" +
					"dependencies:\n  - name: present-sub\n    version: 0.1.0\n",
				"templates/cm.yaml":                    validConfigMap,
				"charts/present-sub/Chart.yaml":        "apiVersion: v2\nname: present-sub\nversion: 0.1.0\n",
				"charts/present-sub/templates/cm.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: present-sub\n",
			},
			wantOut: "name: present-sub",
		},
		{
			name: "a valid chart carrying prose notes and a partial is accepted",
			files: map[string]string{
				"Chart.yaml":             validChartYAML,
				"templates/cm.yaml":      validConfigMap,
				"templates/NOTES.txt":    notesText,
				"templates/_helpers.tpl": partialTpl,
			},
			wantOut: "kind: ConfigMap",
		},
		{
			name: "a chart whose only manifest sits at an undiscovered position is accepted and renders nothing",
			files: map[string]string{
				"Chart.yaml":            validChartYAML,
				"extra/deployment.yaml": validConfigMap,
			},
			wantNoOut: true,
		},
		{
			// The remaining blindness, pinned deliberately rather than left to be
			// discovered: a defect inside a chart nothing discovers is refused by
			// no surface, so this helper must not refuse it either.
			name: "a YAML defect at an undiscovered position is accepted, because nothing discovers it",
			files: map[string]string{
				"Chart.yaml":              validChartYAML,
				"extra/unterminated.yaml": "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: \"unterminated\n",
			},
			wantNoOut: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := writeChartTree(t, tc.files)
			out, err := charttest.RenderThroughDependencyPipeline(dir)

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("the helper accepted this chart and returned %d output byte(s), so it does not "+
						"reproduce the refusal this row pins; wanted an error carrying %q",
						len(out), tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("the helper refused this chart on some other ground, so this row no longer covers "+
						"its own fault: got %q, wanted a substring %q", err.Error(), tc.wantErr)
				}
				return
			}

			if err != nil {
				t.Fatalf("the helper refused a chart the tool accepts: %v", err)
			}
			if tc.wantNoOut && strings.TrimSpace(out) != "" {
				t.Fatalf("wanted an empty render, got %q", out)
			}
			if tc.wantOut != "" && !strings.Contains(out, tc.wantOut) {
				t.Fatalf("the render is missing %q; got %q", tc.wantOut, out)
			}
		})
	}
}

// TestRenderThroughDependencyPipelineRefusesSchemaViolation pins the refusal
// that arrives through the render-values step rather than through either of the
// two the surrounding table covers.
func TestRenderThroughDependencyPipelineRefusesSchemaViolation(t *testing.T) {
	t.Parallel()

	dir := writeChartTree(t, map[string]string{
		"Chart.yaml":        validChartYAML,
		"values.yaml":       "replicas: \"not-a-number\"\n",
		"templates/cm.yaml": validConfigMap,
		"values.schema.json": `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {"replicas": {"type": "integer"}}
}`,
	})

	out, err := charttest.RenderThroughDependencyPipeline(dir)
	if err == nil {
		t.Fatalf("the helper accepted values its own chart's schema rejects and returned %d output byte(s)", len(out))
	}
	const want = "values don't meet the specifications of the schema"
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("the helper refused this chart on some other ground, so this test no longer covers the "+
			"schema violation it pins: got %q, wanted a substring %q", err.Error(), want)
	}
}
