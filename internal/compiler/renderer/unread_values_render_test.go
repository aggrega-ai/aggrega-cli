// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// uvrEdgeInput extends the base input with a target node and one api->target
// edge carrying the supplied per-connection values, so a real render reaches the
// per-edge reading exactly as a build does.
func uvrEdgeInput(edgeValues types.OrderedMap[any]) renderer.RendererInput {
	input := testInput()
	input.Graph.Spec.Nodes.Set("db", types.IRNode{
		Component:       "core/datastore/db",
		ResolvedVersion: "1.0.0",
		BoundaryPath:    "local/default",
	})
	input.Graph.Spec.Edges = []types.IREdge{{
		Source: "api", Target: "db", Socket: "databases", Alias: "main", Values: edgeValues,
	}}
	return input
}

// uvrLookup serves the consumer a Recipe whose inline valuesMapping is the
// supplied text, and the target a Recipe with no mapping of its own — so every
// warning the render produces is attributable to the consumer.
func uvrLookup(consumerMapping string) renderer.RecipeLookup {
	const workload = `apiVersion: aggrega.ai/v1beta1
kind: Workload
containers:
  main:
    image: app:latest
    ports:
      web: 8080
`
	return renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			r := &types.Recipe{
				Metadata: types.RecipeMetadata{Name: "consumer-recipe"},
				Spec: types.RecipeSpec{
					Capability: "helm",
					Implements: componentType,
					Workload:   types.WorkloadTemplate{Inline: workload},
				},
			}
			if componentType == "core/compute/api-server" {
				r.Spec.ValuesMapping = types.ValuesMapping{Inline: consumerMapping}
			} else {
				r.Metadata.Name = "target-recipe"
			}
			return r, true
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}
}

// uvrRender renders the fixture and returns the advisory lines a reader is
// shown, rendered through the one display formatter so the rows keep asserting
// on the text a build prints.
func uvrRender(t *testing.T, edgeValues types.OrderedMap[any], consumerMapping string) []string {
	t.Helper()
	result, err := renderer.RenderAll(context.Background(),
		uvrEdgeInput(edgeValues), uvrLookup(consumerMapping), testHelmFactory(t))
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	if result.Warnings == nil {
		return nil
	}
	out := make([]string, 0, len(result.Warnings))
	for _, d := range result.Warnings {
		out = append(out, renderer.RenderDiagnosticText(d))
	}
	return out
}

func uvrEdgeValues(pairs ...any) types.OrderedMap[any] {
	m := types.OrderedMap[any]{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m.Set(pairs[i].(string), pairs[i+1])
	}
	return m
}

// TestRenderAll_PerEdgeGhostInfrastructureWarning is the real-render proof: a
// per-connection value the consumer's valuesMapping never reads surfaces as a
// warning on the render result, a sibling it does read stays silent, and the
// build still succeeds — the reading advises and never halts.
func TestRenderAll_PerEdgeGhostInfrastructureWarning(t *testing.T) {
	t.Parallel()

	warnings := uvrRender(t,
		uvrEdgeValues("protocol", "grpc", "statement_cache", 0),
		`{"proto": "{{ .context.uses.main.values.protocol }}", "port": {{ .values.port }}}`)

	var perEdge []string
	for _, w := range warnings {
		if strings.Contains(w, ".context.uses.main.values") {
			perEdge = append(perEdge, w)
		}
	}
	if len(perEdge) != 1 {
		t.Fatalf("want exactly one per-connection warning; got %d of %v", len(perEdge), warnings)
	}
	if !strings.Contains(perEdge[0], `"statement_cache"`) {
		t.Errorf("warning does not name the unread key:\n %s", perEdge[0])
	}
	if strings.Contains(perEdge[0], "protocol") {
		t.Errorf("a per-connection value the valuesMapping reads was reported unread:\n %s", perEdge[0])
	}
	// Attribution: the consumer is named, never the edge's target.
	if !strings.Contains(perEdge[0], `node "api"`) || strings.Contains(perEdge[0], `node "db"`) {
		t.Errorf("per-connection warning is not consumer-attributed:\n %s", perEdge[0])
	}
}

// TestRenderAll_PerEdgeWarningIsNonFatalAndDeterministic pins the two standing
// properties on the real render path: the build succeeds with warnings present,
// and repeated renders of one input produce one warning stream.
func TestRenderAll_PerEdgeWarningIsNonFatalAndDeterministic(t *testing.T) {
	t.Parallel()

	const mapping = `{"port": {{ .values.port }}}`
	values := uvrEdgeValues("zeta", 1, "alpha", 2)

	first := uvrRender(t, values, mapping)
	if len(first) == 0 {
		t.Fatal("expected per-connection warnings; got none")
	}
	// A successful render with warnings present is the non-fatal property: the
	// helper above already failed the test on a RenderAll error.
	for range 3 {
		again := uvrRender(t, values, mapping)
		if strings.Join(again, "\n") != strings.Join(first, "\n") {
			t.Fatalf("warning stream not stable across renders:\n first:\n%s\n again:\n%s",
				strings.Join(first, "\n"), strings.Join(again, "\n"))
		}
	}
}

// TestRenderAll_NoPerEdgeValuesNoPerEdgeWarning pins the quiet case on the real
// render path: an edge carrying no authored per-connection value produces no
// per-connection warning, however little the consumer's valuesMapping reads.
func TestRenderAll_NoPerEdgeValuesNoPerEdgeWarning(t *testing.T) {
	t.Parallel()

	warnings := uvrRender(t, types.OrderedMap[any]{}, `{"port": {{ .values.port }}}`)
	for _, w := range warnings {
		if strings.Contains(w, ".context.uses") {
			t.Errorf("an edge with no authored per-connection value produced: %s", w)
		}
	}
}

// TestRenderAll_KeyConsumedBySourceIsNotReportedUnread is the real-render proof
// that a key a source coordinate consumes is read.
//
// It has to run through RenderAll rather than through the reading alone, and the
// reason is the whole of this test. By the time a node's advisories are raised,
// the render holds a copy of the Recipe whose source carries the value the
// template PRODUCED — the template text is gone. A reading given that copy finds
// no reference to the key and reports it unread, on the same run whose output the
// key decided. The reading itself cannot notice: handed the authored document, as
// every unit-level reading is, it answers correctly.
//
// The fixture is a terraform recipe sourcing a local module directory, so the
// source resolves from disk and the render completes with nothing fetched. The
// module sits inside the Recipe's own directory and the template produces a path
// relative to it, because a path a source template produces is held to that
// directory like every content path a Recipe declares; the lookup reports the
// Recipe's document so the produced path is resolved against it. The second infra
// key is the positive control in the same render: a reading that had simply gone
// quiet would satisfy the first assertion perfectly, so the two run together or
// neither means anything.
func TestRenderAll_KeyConsumedBySourceIsNotReportedUnread(t *testing.T) {
	files := map[string]string{}
	for rel, body := range terraformModuleFixture() {
		files["module/"+rel] = body
	}
	recipeDir := writeTerraformModule(t, files)

	const valuesPillar = `{"db_name":"appdb","upstream_endpoint":"${aws_db_instance.main.endpoint}"}`
	const valuesMapping = `db_name: "{{ .values.db_name }}"
rds_endpoint: "{{ .values.upstream_endpoint }}"
`
	input := terraformLocalInput("db", valuesPillar)
	input.Graph.Spec.InfraMapping.Set("db", json.RawMessage(`{"module_dir":"./module","read_by_nothing":"x"}`))

	// The source is a template over the infra key, and nothing else reads it.
	lookup := terraformLocalRecipeLookup("{{ .infra.module_dir }}", valuesMapping)
	recipePath := filepath.Join(recipeDir, "recipe.yaml")
	lookup.RecipeDocument = func(*types.Recipe) (string, bool) { return recipePath, true }

	result, err := renderer.RenderAll(context.Background(), input, lookup, wasmEngineFactory(t))
	if err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	var lines []string
	for _, d := range result.Warnings {
		lines = append(lines, renderer.RenderDiagnosticText(d))
	}
	joined := strings.Join(lines, "\n")

	if strings.Contains(joined, `"module_dir"`) {
		t.Errorf("a key the source coordinate consumes was reported unread:\n%s", joined)
	}
	if !strings.Contains(joined, `"read_by_nothing"`) {
		t.Errorf("the control key, which nothing reads, was not reported — the reading is silent rather than correct:\n%s", joined)
	}
}
