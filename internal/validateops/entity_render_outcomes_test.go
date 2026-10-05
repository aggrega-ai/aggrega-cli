// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validateops

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// probeComponent declares three infra fields the probe Recipe reads or could:
// one required with no default, which only the check can fill in a build of one;
// one required with a default, which the resolver fills; and one optional, which
// nothing fills.
const probeComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: example/compute/probe
  version: "1.0.0"
  description: A component the single-entity check places on its own.
  labels:
    component.aggrega.ai/type: "server"
    component.aggrega.ai/category: compute
spec:
  schema:
    infra:
      image:
        type: string
        required: true
        description: Container image, with no default.
      tag:
        type: string
        required: true
        default: "1.0"
        description: Image tag, with a default.
      note:
        type: string
        description: An optional field nothing fills.
`

// probeRecipe is a helm Recipe implementing the probe Component, whose workload
// image is the given template text. form, when not empty, is written into the
// workload as its form.
func probeRecipe(image, form string) string {
	formLine := ""
	if form != "" {
		formLine = "    form: " + form + "\n"
	}
	return `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: probe
  version: "1.0.0"
spec:
  capability: helm
  implements: example/compute/probe
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
` + formLine + `    replicas: 1
    containers:
      main:
        image: "` + image + `"
        ports:
          http: 8080
    services:
      default:
        type: ClusterIP
        ports:
          http: 8080
`
}

// writeProbeCatalog writes the probe Component and a probe Recipe into a fresh
// catalog directory and returns the directory and the Recipe's path.
func writeProbeCatalog(t *testing.T, recipe string) (string, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "catalog")
	files := map[string]string{
		"component/example/compute/probe/1.0.0/component.yaml": probeComponent,
		"recipe/helm/probe/1.0.0/recipe.yaml":                  recipe,
	}
	for rel, content := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return dir, filepath.Join(dir, "recipe/helm/probe/1.0.0/recipe.yaml")
}

// TestValidateRecipeRenderOutcomes drives a Recipe through the one
// single-entity check and reads the outcome of its isolated render.
//
// The first row is a required infra field with no default. A build of one has
// nobody to supply it but the check, so the check fills it exactly as it fills a
// required value — with a placeholder of the field's type — and leaves a field
// carrying a default to that default. The rendered image proves both halves:
// "example" is the placeholder and "1.0" the default. Without the fill this row
// fails at resolution on the missing required property and never renders.
//
// The other rows are faults of the Recipe's own, each a different cause: a read
// of a key nothing supplies, a function that does not exist, and a fault the
// provider guest raises. Each still fails, and its error's text — the one line a
// caller printing the error reads — carries the sentence naming the cause. The
// render's outermost frame names only the node it was rendering, so a check
// returning a plain wrap of the render's error would report that frame and drop
// the sentence; each of these rows then fails on its wantText.
//
// A cause is matched by the sentence a reader is shown, because that sentence is
// what this test is about. Where a fault names a registered condition it is
// matched by that condition too, with errors.Is, so the row cannot pass on a
// different fault that happens to share wording.
func TestValidateRecipeRenderOutcomes(t *testing.T) {
	cases := []struct {
		name  string
		image string
		form  string
		// wantErr is whether the check fails. wantText is a sentence the error's
		// one-line text must carry, and wantCondition a registered condition the
		// error must name, or nil where the fault names none.
		wantErr       bool
		wantText      string
		wantCondition error
		// reportCarries are lines the check's report must carry; artifactCarries
		// is text one rendered artifact must carry.
		reportCarries   []string
		artifactCarries string
	}{
		{
			name:            "a required infra field with no default is filled, and one with a default keeps it",
			image:           "{{ .infra.image }}:{{ .infra.tag }}",
			reportCarries:   []string{"Resolution: valid", "Render: 2 artifact(s)"},
			artifactCarries: "image: example:1.0",
		},
		{
			name:          "a read of a key nothing supplies fails, and the text names the read",
			image:         "{{ .infra.imge.repo }}:{{ .infra.tag }}",
			wantErr:       true,
			wantText:      "at <.infra.imge.repo>: nil pointer evaluating interface {}.repo",
			reportCarries: []string{"Resolution: valid", "Render: not completed in isolation"},
		},
		{
			name:          "a function that does not exist fails, and the text names the function",
			image:         "{{ nosuchfunc .infra.image }}",
			wantErr:       true,
			wantText:      `function "nosuchfunc" not defined`,
			reportCarries: []string{"Resolution: valid", "Render: not completed in isolation"},
		},
		{
			name:          "a fault the provider raises fails, and the text carries the provider's sentence",
			image:         "{{ .infra.image }}:{{ .infra.tag }}",
			form:          "stateful",
			wantErr:       true,
			wantText:      "requires a headless governing Service",
			wantCondition: wasm.ErrPluginRequiredInputUnresolved(),
			reportCarries: []string{"Resolution: valid", "Render: not completed in isolation"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, recipe := writeProbeCatalog(t, probeRecipe(tc.image, tc.form))
			root := filepath.Dir(dir)
			res, err := ValidateEntity(context.Background(), root, filepath.Join(root, "cache"),
				[]string{dir}, nil, recipe, false)
			report := strings.Join(res.Diagnostics, "\n")

			switch {
			case !tc.wantErr && err != nil:
				t.Fatalf("ValidateEntity: %v\nreport:\n%s", err, report)
			case tc.wantErr && err == nil:
				t.Fatalf("ValidateEntity returned nil; want a failure carrying %q\nreport:\n%s", tc.wantText, report)
			}
			if tc.wantErr {
				var stopped *renderStopError
				if !errors.As(err, &stopped) {
					t.Errorf("the failure is not a render stop, so the row did not reach the render:\n%v", err)
				}
				if !strings.Contains(err.Error(), tc.wantText) {
					t.Errorf("the error's text does not carry %q:\n%s", tc.wantText, err.Error())
				}
				if tc.wantCondition != nil && !errors.Is(err, tc.wantCondition) {
					t.Errorf("the error does not name the condition %q:\n%v", tc.wantCondition, err)
				}
			}
			if res.Valid == tc.wantErr {
				t.Errorf("Valid = %v with error %v", res.Valid, err)
			}
			for _, line := range tc.reportCarries {
				if !strings.Contains(report, line) {
					t.Errorf("the report does not carry %q:\n%s", line, report)
				}
			}
			if tc.artifactCarries != "" {
				found := false
				for _, a := range res.Artifacts {
					if bytes.Contains(a.Content, []byte(tc.artifactCarries)) {
						found = true
					}
				}
				if !found {
					t.Errorf("no rendered artifact carries %q; artifacts: %d", tc.artifactCarries, len(res.Artifacts))
				}
			}
		})
	}
}

// TestRequiredPlaceholdersFillEachPillarAlike reads the placeholders the check
// fills for a Component declaring a required field with no default, a required
// field with a default, and an optional field, on the values pillar and the
// infra pillar alike: only the first is filled, on each pillar, by the same
// typed placeholder.
func TestRequiredPlaceholdersFillEachPillarAlike(t *testing.T) {
	compiled := map[string]any{
		"properties": map[string]any{
			"values": map[string]any{
				"required": []any{"database", "charset"},
				"properties": map[string]any{
					"database": map[string]any{"type": "string"},
					"charset":  map[string]any{"type": "string", "default": "utf8mb4"},
					"user":     map[string]any{"type": "string"},
				},
			},
			"infra": map[string]any{
				"required": []any{"replicas", "region"},
				"properties": map[string]any{
					"replicas": map[string]any{"type": "integer", "minimum": 2},
					"region":   map[string]any{"type": "string", "default": "eu"},
					"note":     map[string]any{"type": "string"},
				},
			},
		},
	}
	cases := []struct {
		pillar string
		want   map[string]any
	}{
		{pillar: "values", want: map[string]any{"database": "example"}},
		{pillar: "infra", want: map[string]any{"replicas": 2}},
		{pillar: "provides", want: nil},
	}
	for _, tc := range cases {
		t.Run(tc.pillar, func(t *testing.T) {
			got := requiredFieldPlaceholders(compiled, tc.pillar)
			if len(got) != len(tc.want) {
				t.Fatalf("placeholders = %v, want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("placeholder %q = %v, want %v", k, got[k], v)
				}
			}
		})
	}
}
