// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// End-to-end coverage of an omits entry naming the subchart a document came from,
// through the real pipeline and the embedded helm provider.
//
// The fixture's chart carries two subcharts, operator and catalog, each shipping the
// CustomResourceDefinition widgets.example.com under crds/ with differing bytes —
// catalog's copy marked as not for the cluster. The Recipe's one entry names that
// definition and the subchart the Release writes into infra, active only where the
// Release writes one, so each cell below is one Release over one catalog.

package compiler_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// helmOmitsSubchartInput compiles one Release of the subchart fixture.
func helmOmitsSubchartInput(release string) pipelinetest.Input {
	return pipelinetest.Input{
		ReleasePath:  "testdata/helm-omits-subchart/" + release,
		CatalogPaths: []string{"testdata/helm-omits-subchart/catalog"},
	}
}

// TestHelmOmitsSubchart_LeavesOutOneSubchartsCopyAndKeepsTheOther builds the control,
// which leaves nothing out and halts on the two copies at one output name, and the
// Release naming catalog, which writes exactly one definition: operator's, the copy
// carrying no not-for-the-cluster marker. The chart's own ConfigMap is written either
// way the build completes.
func TestHelmOmitsSubchart_LeavesOutOneSubchartsCopyAndKeepsTheOther(t *testing.T) {
	t.Parallel()

	_, err := pipelinetest.BuildE(t, helmOmitsSubchartInput("release.yaml"))
	if !errors.Is(err, plugincondition.ErrPluginOutputNameCollision()) {
		t.Fatalf("the control did not halt on two copies at one name, so the cell below proves nothing: %v", err)
	}
	for _, want := range []string{"charts/operator/crds/widgets.yaml", "charts/catalog/crds/widgets.yaml"} {
		if !strings.Contains(cerrors.CauseChain(err), want) {
			t.Errorf("the control's halt does not name %q:\n%s", want, cerrors.CauseChain(err))
		}
	}

	result, err := pipelinetest.BuildE(t, helmOmitsSubchartInput("release-catalog-left-out.yaml"))
	if err != nil {
		t.Fatalf("leaving catalog's copy out must clear the collision: %s", cerrors.CauseChain(err))
	}
	var definitions, settings int
	for path, docs := range omitsDocuments(t, result.Artifacts) {
		for _, d := range docs {
			switch {
			case d.Kind == "CustomResourceDefinition" && d.Metadata.Name == "widgets.example.com":
				definitions++
				if _, marked := d.Metadata.Annotations["config.kubernetes.io/local-config"]; marked {
					t.Errorf("%s keeps catalog's copy, the one the entry names:\n%s", path, result.Artifacts[path])
				}
			case d.Kind == "ConfigMap" && d.Metadata.Name == "op-settings":
				settings++
			}
		}
	}
	if definitions != 1 {
		t.Errorf("the build writes %d copies of the definition, want exactly operator's", definitions)
	}
	if settings != 1 {
		t.Errorf("the build writes %d of the chart's own ConfigMap, want 1", settings)
	}
}

// TestHelmOmitsSubchart_AnEntryNamingASubchartTheChartDoesNotRenderHalts builds the
// Release naming a subchart the chart does not have, and requires the halt naming
// the entry, the subchart it names and the subcharts the chart renders.
func TestHelmOmitsSubchart_AnEntryNamingASubchartTheChartDoesNotRenderHalts(t *testing.T) {
	t.Parallel()

	_, err := pipelinetest.BuildE(t, helmOmitsSubchartInput("release-unknown-subchart.yaml"))
	if !errors.Is(err, plugincondition.ErrPluginRequiredInputUnresolved()) {
		t.Fatalf("got %v; want the refusal of an entry naming a subchart the chart does not render", err)
	}
	chain := cerrors.CauseChain(err)
	for _, want := range []string{
		`an omits entry (kind "CustomResourceDefinition", name "widgets.example.com") names subchart "addons"`,
		"the chart renders no subchart of that name",
		`the subcharts it renders are "catalog", "operator"`,
	} {
		if !strings.Contains(chain, want) {
			t.Errorf("the halt does not carry %q:\n%s", want, chain)
		}
	}
}

// helmOmitsSubchartSensitiveLicence is the value the sensitive fixture's Aggregate
// declares sensitive.
const helmOmitsSubchartSensitiveLicence = "omits-sensitive-licence-plaintext"

// TestHelmOmitsSubchart_TheHaltKeepsItsSentenceWhereTheInputMarksAValueSensitive builds
// a fixture whose values mapping reads a value declared sensitive, so the chart's
// values reach the provider marked as a whole, and whose values schema types a
// boolean the mapping writes — a value the placeholder cannot stand in for. The
// Release naming a subchart the chart does not have halts naming the entry, the
// subchart and the subcharts the chart renders; the control, whose chart refuses
// quoting the declared value, still keeps that value out of its text.
func TestHelmOmitsSubchart_TheHaltKeepsItsSentenceWhereTheInputMarksAValueSensitive(t *testing.T) {
	t.Parallel()
	input := func(release string) pipelinetest.Input {
		return pipelinetest.Input{
			ReleasePath:  "testdata/helm-omits-subchart-sensitive/" + release,
			CatalogPaths: []string{"testdata/helm-omits-subchart-sensitive/catalog"},
		}
	}

	t.Run("the unknown-subchart halt", func(t *testing.T) {
		t.Parallel()
		_, err := pipelinetest.BuildE(t, input("release-unknown-subchart.yaml"))
		if !errors.Is(err, plugincondition.ErrPluginRequiredInputUnresolved()) {
			t.Fatalf("got %v; want the refusal of an entry naming a subchart the chart does not render", err)
		}
		chain := cerrors.CauseChain(err)
		for _, want := range []string{
			`an omits entry (kind "CustomResourceDefinition", name "widgets.example.com") names subchart "addons"`,
			"the chart renders no subchart of that name",
			`the subcharts it renders are "operator"`,
		} {
			if !strings.Contains(chain, want) {
				t.Errorf("the halt does not carry %q:\n%s", want, chain)
			}
		}
		if strings.Contains(chain, helmOmitsSubchartSensitiveLicence) {
			t.Errorf("the halt carries the value declared sensitive:\n%s", chain)
		}
	})

	t.Run("the control: a chart quoting the declared value", func(t *testing.T) {
		t.Parallel()
		_, err := pipelinetest.BuildE(t, input("release-quote.yaml"))
		if err == nil {
			t.Fatal("the control built; its chart must refuse, or the absence below proves nothing")
		}
		text := err.Error() + "\n" + cerrors.CauseChain(err)
		if strings.Contains(text, helmOmitsSubchartSensitiveLicence) {
			t.Errorf("the refusal carries the value declared sensitive:\n%s", text)
		}
		if !strings.Contains(text, "templates/quote.yaml") {
			t.Errorf("the refusal does not name the template that refused, so it is not the chart's own:\n%s", text)
		}
	})
}

// TestHelmOmitsSubchart_LeavingOneCopyOutIsDeterministic builds the Release leaving
// catalog's copy out repeatedly and requires byte-identical output.
func TestHelmOmitsSubchart_LeavingOneCopyOutIsDeterministic(t *testing.T) {
	t.Parallel()
	pipelinetest.AssertDeterministic(t, helmOmitsSubchartInput("release-catalog-left-out.yaml"))
}
