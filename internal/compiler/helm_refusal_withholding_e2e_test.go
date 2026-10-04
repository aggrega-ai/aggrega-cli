// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// A chart's own refusal never carries a value the helm guest's input marks
// sensitive, through the real pipeline and the guest itself. The guest composes
// this text, not the compiler, so the compiler carries it unchanged and only the
// guest can keep the value out of it: the row proves the marking reaches the guest
// and the guest withholds by it. The chart receives the Component's own values,
// keyed by the leaves the Component declares, which is the shape the render input
// marks. A control chart quoting the value nobody declared must reach the text
// unchanged.

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

// TestHelmChartRefusal_WithholdsAMarkedValueThroughTheGuest builds a chart that
// refuses quoting the declared value, and a control quoting the undeclared one.
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
