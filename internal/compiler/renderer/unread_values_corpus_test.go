// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// uvcStrings builds a one-entry ordered map for the blocks a Recipe writes as
// key-to-template.
func uvcStrings(key, tmplText string) types.OrderedMap[string] {
	var m types.OrderedMap[string]
	m.Set(key, tmplText)
	return m
}

// uvcArtifacts builds a one-entry artifacts block carrying inline content.
func uvcArtifacts(name, tmplText string) types.OrderedMap[types.ArtifactEntry] {
	var m types.OrderedMap[types.ArtifactEntry]
	m.Set(name, types.ArtifactEntry{Inline: tmplText})
	return m
}

// uvcGraph is the fixture every row below reads: one node carrying a values key
// the mapping reads and an infra key only the row's own block reads.
func uvcGraph() *types.IRGraph {
	graph := uvGraph([]string{"api"})
	graph.Spec.ValuesMapping.Set("api", json.RawMessage(`{"mapped":1}`))
	graph.Spec.InfraMapping.Set("api", json.RawMessage(`{"only_here":"x"}`))
	return graph
}

// uvcRecipe wraps one mutated spec in a Recipe, with the values mapping always
// populated and always reading a DIFFERENT key — so every row exercises the
// reference collection rather than the entry guard, and the infra key's verdict
// is decided by the block the row mutates and by nothing else.
func uvcRecipe(mutate func(*types.RecipeSpec)) *types.Recipe {
	spec := types.RecipeSpec{
		ValuesMapping: types.ValuesMapping{Inline: `m: {{ .values.mapped }}`},
	}
	mutate(&spec)
	return &types.Recipe{Metadata: types.RecipeMetadata{Name: "consumer-recipe"}, Spec: spec}
}

// TestUnreadValueWarnings_EveryAuthoredTemplateCounts is the reading's central
// claim, one row per block a Recipe can read an authored key from.
//
// The claim the reading makes is that NOTHING reads the key, which is a claim
// over the whole Recipe: a key consumed by any one of these blocks is read, and
// reporting it unread would tell an operator that the run ignored a value the
// same run obeyed. Each row puts the key beyond the values mapping's reach and
// asserts silence; the two controls at the end keep the table from passing
// vacuously, because a reading that had simply stopped reporting would satisfy
// every silent row perfectly.
func TestUnreadValueWarnings_EveryAuthoredTemplateCounts(t *testing.T) {
	t.Parallel()

	const reads = "{{ .infra.only_here }}"

	cases := []struct {
		block  string
		mutate func(*types.RecipeSpec)
		want   bool // true where the key must still be reported
	}{
		{block: "source, authored whole as a scalar", mutate: func(s *types.RecipeSpec) {
			s.Source = reads
		}},
		{block: "source.version", mutate: func(s *types.RecipeSpec) {
			s.Source = map[string]any{"kind": "helm-https-chart", "url": "https://charts/", "version": reads}
		}},
		{block: "source.ref", mutate: func(s *types.RecipeSpec) {
			s.Source = map[string]any{"kind": "git-repository", "url": "https://host/r.git", "ref": reads}
		}},
		{block: "source.chart", mutate: func(s *types.RecipeSpec) {
			s.Source = map[string]any{"kind": "helm-https-chart", "url": "https://charts/", "chart": reads}
		}},
		{block: "source.module_subdir", mutate: func(s *types.RecipeSpec) {
			s.Source = map[string]any{"kind": "git-repository", "url": "https://host/r.git", "module_subdir": reads}
		}},
		{block: "source.url", mutate: func(s *types.RecipeSpec) {
			s.Source = map[string]any{"kind": "helm-https-chart", "url": reads}
		}},
		{block: "locals", mutate: func(s *types.RecipeSpec) {
			s.Locals = uvcStrings("computed", reads)
		}},
		{block: "provides", mutate: func(s *types.RecipeSpec) {
			s.Provides = uvcStrings("host", reads)
		}},
		{block: "assertions", mutate: func(s *types.RecipeSpec) {
			s.Assertions = uvcStrings("sized", reads)
		}},
		{block: "workload", mutate: func(s *types.RecipeSpec) {
			s.Workload = types.WorkloadTemplate{Inline: reads}
		}},
		{block: "artifacts entry", mutate: func(s *types.RecipeSpec) {
			s.Artifacts = uvcArtifacts("extra.yaml", reads)
		}},
		{block: "pipeline step input", mutate: func(s *types.RecipeSpec) {
			s.Pipeline = []types.PipelineStep{{ID: "shape", With: reads}}
		}},
		{block: "pipeline step gate", mutate: func(s *types.RecipeSpec) {
			s.Pipeline = []types.PipelineStep{{ID: "shape", When: reads}}
		}},
		{block: "the values mapping itself", mutate: func(s *types.RecipeSpec) {
			s.ValuesMapping = types.ValuesMapping{Inline: "m: {{ .values.mapped }}\no: " + reads}
		}},
		{
			block:  "CONTROL: nothing anywhere reads it",
			mutate: func(*types.RecipeSpec) {},
			want:   true,
		},
		{
			block: "CONTROL: every other block reads a DIFFERENT key",
			mutate: func(s *types.RecipeSpec) {
				s.Source = map[string]any{"kind": "helm-https-chart", "url": "https://charts/", "version": "{{ .infra.elsewhere }}"}
				s.Locals = uvcStrings("computed", "{{ .infra.elsewhere }}")
				s.Provides = uvcStrings("host", "{{ .infra.elsewhere }}")
				s.Assertions = uvcStrings("sized", "{{ .infra.elsewhere }}")
				s.Workload = types.WorkloadTemplate{Inline: "{{ .infra.elsewhere }}"}
				s.Artifacts = uvcArtifacts("extra.yaml", "{{ .infra.elsewhere }}")
				s.Pipeline = []types.PipelineStep{{ID: "shape", With: "{{ .infra.elsewhere }}", When: "{{ .infra.elsewhere }}"}}
			},
			want: true,
		},
	}

	graph := uvcGraph()
	for _, c := range cases {
		got := uvWarnings(t, "api", graph, uvcRecipe(c.mutate))
		reported := strings.Contains(uvJoined(got), `"only_here"`)
		if reported != c.want {
			verdict := map[bool]string{true: "reported", false: "silent"}
			t.Errorf("%s: the key was %s, want %s\ngot:\n%s",
				c.block, verdict[reported], verdict[c.want], uvJoined(got))
		}
	}
}

// TestUnreadValueWarnings_ImportedSurfaceMakesNoClaim pins the bound that makes
// the wider reference set honest.
//
// A workload body or an artifacts entry supplied by `$import:` carries no text
// at this point, so the references it makes cannot be collected. Counting such a
// Recipe's remaining templates as the whole set would report every key that
// imported surface reads — which is the very fault the wider set exists to
// remove, reintroduced one field along. No claim is made instead.
func TestUnreadValueWarnings_ImportedSurfaceMakesNoClaim(t *testing.T) {
	t.Parallel()

	graph := uvcGraph()

	importedWorkload := uvcRecipe(func(s *types.RecipeSpec) {
		s.Workload = types.WorkloadTemplate{Import: "./workload.yaml"}
	})
	if got := uvWarnings(t, "api", graph, importedWorkload); got != nil {
		t.Errorf("an imported workload produced a claim about the node's keys:\n%s", uvJoined(got))
	}

	importedArtifact := uvcRecipe(func(s *types.RecipeSpec) {
		var m types.OrderedMap[types.ArtifactEntry]
		m.Set("extra.yaml", types.ArtifactEntry{Import: "./extra.yaml"})
		s.Artifacts = m
	})
	if got := uvWarnings(t, "api", graph, importedArtifact); got != nil {
		t.Errorf("an imported artifacts entry produced a claim about the node's keys:\n%s", uvJoined(got))
	}

	// Control: the same Recipes with the same surfaces written inline read
	// nothing, so the key IS reported — which is what shows the two readings
	// above were silenced by the import and not by the fixture.
	inlineWorkload := uvcRecipe(func(s *types.RecipeSpec) {
		s.Workload = types.WorkloadTemplate{Inline: "kind: Workload"}
	})
	if got := uvWarnings(t, "api", graph, inlineWorkload); !strings.Contains(uvJoined(got), `"only_here"`) {
		t.Errorf("control: an inline workload reading nothing must leave the key reported; got:\n%s", uvJoined(got))
	}
}

// TestUnreadValueWarnings_UnparseableSurfaceMakesNoClaim extends to every block
// the bound the values mapping already carried: a template that does not parse
// yields no reference set, and reporting every key as unread on top of the
// diagnostic the evaluation raises for the same template would bury it.
func TestUnreadValueWarnings_UnparseableSurfaceMakesNoClaim(t *testing.T) {
	t.Parallel()

	graph := uvcGraph()
	const broken = `{{ .infra.only_here `

	for _, c := range []struct {
		block  string
		mutate func(*types.RecipeSpec)
	}{
		{"source.version", func(s *types.RecipeSpec) {
			s.Source = map[string]any{"kind": "helm-https-chart", "url": "https://charts/", "version": broken}
		}},
		{"locals", func(s *types.RecipeSpec) { s.Locals = uvcStrings("computed", broken) }},
		{"provides", func(s *types.RecipeSpec) { s.Provides = uvcStrings("host", broken) }},
		{"assertions", func(s *types.RecipeSpec) { s.Assertions = uvcStrings("sized", broken) }},
		{"workload", func(s *types.RecipeSpec) { s.Workload = types.WorkloadTemplate{Inline: broken} }},
		{"artifacts entry", func(s *types.RecipeSpec) { s.Artifacts = uvcArtifacts("extra.yaml", broken) }},
		{"pipeline step input", func(s *types.RecipeSpec) {
			s.Pipeline = []types.PipelineStep{{ID: "shape", With: broken}}
		}},
	} {
		if got := uvWarnings(t, "api", graph, uvcRecipe(c.mutate)); got != nil {
			t.Errorf("%s: an unparseable template produced a claim about the node's keys:\n%s",
				c.block, uvJoined(got))
		}
	}
}

// TestUnreadValueWarnings_MessageNamesNoRecipeField is the wording guard.
//
// The advisory's reader is whoever authored the key — the developer for a
// `.values` key, the operator for an `.infra` one, the consuming author for a
// per-connection one — and none of them edits the Recipe. A sentence naming the
// block the reference was looked for in hands that reader a coordinate in a
// document they do not open, and it stopped being true the moment the reading
// began consulting every template rather than one. So the message states the
// fact and the two things its reader can act on, and names no Recipe field.
func TestUnreadValueWarnings_MessageNamesNoRecipeField(t *testing.T) {
	t.Parallel()

	graph := uvcGraph()
	got := uvWarnings(t, "api", graph, uvcRecipe(func(*types.RecipeSpec) {}))
	if len(got) != 1 {
		t.Fatalf("warning count = %d, want 1:\n%s", len(got), uvJoined(got))
	}

	want := `node "api": .infra key "only_here" is read by no template in recipe "consumer-recipe" — correct the spelling, or remove the key`
	if got[0] != want {
		t.Errorf("message moved:\n got  %s\n want %s", got[0], want)
	}
	for _, field := range []string{"valuesMapping", "locals", "provides", "workload", "artifacts", "pipeline", "assertions"} {
		if strings.Contains(got[0], field) {
			t.Errorf("the message names the Recipe field %q, which its reader does not author:\n %s", field, got[0])
		}
	}
}
