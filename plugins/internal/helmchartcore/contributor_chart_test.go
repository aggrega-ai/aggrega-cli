// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchartcore

import (
	"strings"
	"testing"
)

// A contributor whose render placed no chart manifest is a Feature attachment
// rendered through another provider: finished manifests at the root of its own
// directory under charts/. These cases pin the chart the umbrella assembly
// writes for it — the manifest, the one template emitting each rendered file,
// and the dependency entry read from that manifest — driven through
// AssembleUmbrellaCore, the one entry point the provider guest calls.

// renderedContributor is a manifest-less contributor as the guest hands it to
// the core: the dispatch identity the compiler routed it under, the identity it
// is addressed by, its position, and the files its render placed.
func renderedContributor(files ...string) CoreSubChart {
	return CoreSubChart{
		InstanceID:      "<attachment>:api@guardrail",
		NameBasis:       "cfr--api--guardrail",
		OutputDirectory: "charts/api--guardrail",
		Values:          map[string]any{"label": "injected"},
		RenderedFiles:   files,
	}
}

func TestContributorWithoutAManifestIsGivenAChartNamedForItsIdentity(t *testing.T) {
	t.Parallel()

	files, err := AssembleUmbrellaCore("main/helm-chart", "pairing",
		[]CoreSubChart{renderedContributor("guardrail-config.yaml")}, "", 2)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}

	manifest, ok := files["charts/api--guardrail/Chart.yaml"]
	if !ok {
		t.Fatalf("no manifest written for the contributor; keys %v", keys(files))
	}
	// The name is the contributor's own identity through the encoding every
	// placed chart's name goes through — the name a chart the same attachment
	// vendored would declare — and never the dispatch identity it was routed
	// under, which no chart name admits.
	if got, want := string(manifest), "apiVersion: v2\nname: cfr-zh-api-zh-guardrail\nversion: 0.1.0\n"; got != want {
		t.Errorf("manifest = %q, want %q", got, want)
	}

	umbrella := string(files["Chart.yaml"])
	for _, want := range []string{
		"  - name: cfr-zh-api-zh-guardrail\n    version: 0.1.0\n    alias: cfr-zh-api-zh-guardrail\n    repository: file://charts/api--guardrail\n",
	} {
		if !strings.Contains(umbrella, want) {
			t.Errorf("the umbrella's dependency entry does not bind the written manifest; want %q in\n%s", want, umbrella)
		}
	}
	if strings.Contains(umbrella, "zx00003c") {
		t.Errorf("the dependency is named from the dispatch identity rather than the written manifest:\n%s", umbrella)
	}
	if values := string(files["values.yaml"]); !strings.Contains(values, "cfr-zh-api-zh-guardrail:\n  label: injected\n") {
		t.Errorf("the contributor's values are not keyed by the name its chart declares:\n%s", values)
	}
}

func TestContributorTemplateEmitsEachRenderedManifestByName(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		rendered []string
		// want is the template written, or "" where none may be written.
		want string
	}{
		{
			name:     "each rendered manifest, in the order the render emitted them",
			rendered: []string{"rule.yaml", "a/config.yml", "deployment.yaml"},
			want: "---\n{{ .Files.Get \"rule.yaml\" }}\n" +
				"---\n{{ .Files.Get \"a/config.yml\" }}\n" +
				"---\n{{ .Files.Get \"deployment.yaml\" }}\n",
		},
		{
			name: "files Helm does not expose through .Files are left out",
			rendered: []string{"values.yaml", "requirements.yaml", "templates/own.yaml", "charts/x/Chart.yaml",
				"notes.txt", "kept.yaml"},
			want: "---\n{{ .Files.Get \"kept.yaml\" }}\n",
		},
		{
			name:     "a name carrying a quote or a backslash is written as a string literal reading back as written",
			rendered: []string{`odd "name".yaml`, `back\slash.yaml`},
			want:     "---\n{{ .Files.Get \"odd \\\"name\\\".yaml\" }}\n---\n{{ .Files.Get \"back\\\\slash.yaml\" }}\n",
		},
		{
			name:     "no manifest at all writes no template",
			rendered: []string{"notes.txt"},
			want:     "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			files, err := AssembleUmbrellaCore("main", "", []CoreSubChart{renderedContributor(tc.rendered...)}, "", 2)
			if err != nil {
				t.Fatalf("assemble: %v", err)
			}
			got, written := files["charts/api--guardrail/templates/rendered.yaml"]
			if tc.want == "" {
				if written {
					t.Errorf("a template was written with nothing to emit:\n%s", got)
				}
				return
			}
			if string(got) != tc.want {
				t.Errorf("template =\n%s\nwant\n%s", got, tc.want)
			}
			// Deterministic: identical input writes identical bytes.
			again, err := AssembleUmbrellaCore("main", "", []CoreSubChart{renderedContributor(tc.rendered...)}, "", 2)
			if err != nil {
				t.Fatalf("assemble again: %v", err)
			}
			if string(again["charts/api--guardrail/templates/rendered.yaml"]) != string(got) {
				t.Error("the template is not byte-stable across two assemblies of identical input")
			}
		})
	}
}

// TestContributorThatPlacedAManifestIsGivenNoChart is the positive control for
// the cases above: a contributor whose render placed its own manifest — a chart
// vendored by a component or by an attachment — is addressed by that manifest and
// gets nothing written for it, so the vendored case is unchanged.
func TestContributorThatPlacedAManifestIsGivenNoChart(t *testing.T) {
	t.Parallel()

	sc := renderedContributor("Chart.yaml", "templates/configmap.yaml")
	sc.PlacedChartYAML = []byte("apiVersion: v2\nname: cfr-zh-api-zh-guardrail\nversion: 0.2.0\n")
	files, err := AssembleUmbrellaCore("main", "", []CoreSubChart{sc}, "", 2)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if len(files) != 2 {
		t.Errorf("the assembly wrote %d files, want the umbrella's own two; keys %v", len(files), keys(files))
	}
	if umbrella := string(files["Chart.yaml"]); !strings.Contains(umbrella, "version: 0.2.0") {
		t.Errorf("the dependency does not carry the placed chart's own version:\n%s", umbrella)
	}
}

// TestContributorWithoutAManifestAndWithoutABasisIsNamedForItsID pins the
// fallback the identity reads through: a contributor carrying no basis is
// addressed by its own id, encoded, exactly as an ordinary component is.
func TestContributorWithoutAManifestAndWithoutABasisIsNamedForItsID(t *testing.T) {
	t.Parallel()

	sc := renderedContributor("x.yaml")
	sc.InstanceID, sc.NameBasis = "data.main-db", ""
	files, err := AssembleUmbrellaCore("main", "", []CoreSubChart{sc}, "", 2)
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	if got := string(files["charts/api--guardrail/Chart.yaml"]); !strings.Contains(got, "name: data-zdmain-db\n") {
		t.Errorf("manifest = %q, want the id encoded as the name", got)
	}
}
