// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// A refusal the helm or compose guest raises never carries a value its input marks
// sensitive, through the real pipeline and the guest itself. The guest composes
// this text, not the compiler, so the compiler carries it unchanged and only the
// guest can keep the value out of it: each row proves the marking reaches the
// guest and the guest withholds by it. The input marks a value in two ways, and
// both are driven: per key, where a chart receives the Component's own values
// keyed by the leaves the Component declares; and as a whole payload, where the
// template that produced the payload produced output depending on a declared
// value — a values mapping, the node's, an attachment's FeatureRecipe's or a boundary's feature's, a
// workload, an artifact — and no key of its output can be traced to a
// declaration. Every row quoting the declared value has a control quoting the
// value nobody declared, which must reach the text unchanged.

package compiler_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

const (
	// The declared-sensitive value the chart fixture quotes, and the value nobody
	// declared that the control quotes.
	helmRefusalSecret = "helm-refusal-secret-plaintext"
	helmRefusalNote   = "helm-refusal-note-plaintext"
)

// helmRefusalText builds one fixture through the real pipeline and returns the
// whole text a surface can render of its refusal, failing when the build does not
// refuse at all.
func helmRefusalText(t *testing.T, release string) string {
	t.Helper()
	_, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  "testdata/helm-refusal-withholding/" + release,
		CatalogPaths: []string{"testdata/helm-refusal-withholding/catalog/base"},
	})
	if err == nil {
		t.Fatalf("%s built; the fixture must refuse, or every assertion below is vacuous", release)
	}
	return err.Error() + "\n" + cerrors.CauseChain(err)
}

// TestHelmChartRefusal_WithholdsAMarkedValueThroughTheGuest builds each fixture
// that refuses quoting the declared value, and its control quoting the undeclared
// one.
func TestHelmChartRefusal_WithholdsAMarkedValueThroughTheGuest(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		release string
		want    string
		absent  string
	}{
		{
			name:    "a chart quoting the declared value reports the placeholder in its place",
			release: "quoting-release.yaml",
			want:    "the admin password " + redact.Placeholder() + " must be supplied as a reference",
			absent:  helmRefusalSecret,
		},
		{
			name:    "a chart quoting the value nobody declared reports it unchanged",
			release: "note-release.yaml",
			want:    "the admin note " + helmRefusalNote + " must not be written inline",
		},
		{
			name:    "a chart's schema refusing a value a values mapping wrote from the declared value reports the placeholder",
			release: "mapped-release.yaml",
			want:    "at '/auth/password': '" + redact.Placeholder() + "' does not match pattern '^ref:'",
			absent:  helmRefusalSecret,
		},
		{
			name:    "a chart's schema refusing a value a values mapping wrote from the undeclared value reports it unchanged",
			release: "mapped-note-release.yaml",
			want:    "at '/auth/password': '" + helmRefusalNote + "' does not match pattern '^ref:'",
		},
		{
			name:    "an attachment's values mapping writing its declared leaf reports the placeholder",
			release: "fr-mapped-release.yaml",
			want:    "at '/auth/password': '" + redact.Placeholder() + "' does not match pattern '^ref:'",
			absent:  helmRefusalSecret,
		},
		{
			name:    "an attachment's values mapping writing its undeclared leaf reports it unchanged",
			release: "fr-mapped-note-release.yaml",
			want:    "at '/auth/password': '" + helmRefusalNote + "' does not match pattern '^ref:'",
		},
		{
			// A boundary's feature renders on a context copied from the component whose
			// render reached it, so the marking it hands the guest is its own mapping's
			// only where it replaces the one it copied; the component here maps no
			// values, so the copied marking says nothing was read.
			name:    "a boundary's feature whose values mapping writes its declared infra leaf reports the placeholder",
			release: "boundary-mapped-release.yaml",
			want:    "at '/auth/password': '" + redact.Placeholder() + "' does not match pattern '^ref:'",
			absent:  helmRefusalSecret,
		},
		{
			name:    "a boundary's feature whose values mapping writes its undeclared infra leaf reports it unchanged",
			release: "boundary-mapped-note-release.yaml",
			want:    "at '/auth/password': '" + helmRefusalNote + "' does not match pattern '^ref:'",
		},
		{
			name:    "a workload quantity rendered from the declared value is refused quoting the placeholder",
			release: "workload-release.yaml",
			want:    `containers.main.resources.limits.cpu: "` + redact.Placeholder() + `" is not a quantity: it does not begin with a number`,
			absent:  helmRefusalSecret,
		},
		{
			name:    "a workload quantity rendered from the undeclared value is refused quoting it",
			release: "workload-note-release.yaml",
			want:    `containers.main.resources.limits.cpu: "` + helmRefusalNote + `" is not a quantity: it does not begin with a number`,
		},
		{
			name:    "an artifact broken by the declared value is refused without the parser's text",
			release: "artifact-release.yaml",
			want: `artifact "cm.yaml" template output is not valid YAML: the parser's text is withheld because ` +
				"the template that produced this artifact reads a value declared sensitive",
			absent: helmRefusalSecret,
		},
		{
			name:    "an artifact broken by the undeclared value is refused with the parser's text",
			release: "artifact-note-release.yaml",
			want:    `artifact "cm.yaml" template output is not valid YAML: yaml: unknown anchor '` + helmRefusalNote + `' referenced`,
		},
		{
			name:    "a compose workload quantity rendered from the declared value is refused quoting the placeholder",
			release: "compose-workload-release.yaml",
			want:    `containers.main.resources.limits.memory: "` + redact.Placeholder() + `" is not a quantity: it does not begin with a number`,
			absent:  helmRefusalSecret,
		},
		{
			name:    "a compose workload quantity rendered from the undeclared value is refused quoting it",
			release: "compose-workload-note-release.yaml",
			want:    `containers.main.resources.limits.memory: "` + helmRefusalNote + `" is not a quantity: it does not begin with a number`,
		},
		{
			name:    "a compose fragment broken by the declared value is refused without the parser's text",
			release: "compose-artifact-release.yaml",
			want: `compose: artifact "sidecar.yaml" template output is not valid YAML: the parser's text is withheld because ` +
				"the template that produced this artifact reads a value declared sensitive",
			absent: helmRefusalSecret,
		},
		{
			name:    "a compose fragment broken by the undeclared value is refused with the parser's text",
			release: "compose-artifact-note-release.yaml",
			want:    `compose: artifact "sidecar.yaml" template output is not valid YAML: yaml: unknown anchor '` + helmRefusalNote + `' referenced`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			text := helmRefusalText(t, tc.release)
			if !strings.Contains(text, tc.want) {
				t.Errorf("refusal does not carry %q:\n%s", tc.want, text)
			}
			if tc.absent != "" && strings.Contains(text, tc.absent) {
				t.Errorf("refusal carries the declared value %q:\n%s", tc.absent, text)
			}
		})
	}
}
