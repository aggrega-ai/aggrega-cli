// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// End-to-end proof that the annotations an Aggregate writes on its subsystems
// and components reach the `.context.annotations.aggregate` bucket a template
// reads, through the real pipeline.

package pipeline_test

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

const aggregateAnnotationBucketDir = "testdata/aggregate-annotation-bucket/"

// renderedVariable returns the value a rendered manifest gives the container
// variable name: the "value:" line following its "- name:" line, unquoted.
// Found reports whether the variable is present at all.
func renderedVariable(body, name string) (value string, found bool) {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "- name: "+name || i+1 >= len(lines) {
			continue
		}
		next := strings.TrimSpace(lines[i+1])
		if !strings.HasPrefix(next, "value:") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(next, "value:"))
		return strings.Trim(v, `"'`), true
	}
	return "", false
}

// TestAggregateAnnotationsReachTheTemplateBucket builds a workspace whose
// Aggregate annotates two levels of subsystem and one component, and whose Recipe
// reads each key from the aggregate bucket, falling back to "unset".
//
// Each row is one node and pins one property of the delivery: a subsystem's
// annotations reach its component; a component's own annotation overrides the
// subsystem's; a nested subsystem inherits its parent's and overrides with its
// own; a node with no annotation on its path reads none. On every node the
// structured value the subsystem wrote is absent from the bucket, and the
// Release's boundary annotation still arrives in its own bucket beside it.
//
// The IR is asserted alongside the rendered output, because the bucket a
// template reads is copied from the graph node and a rendered value alone could
// not tell a graph that carries it from one a template happened to synthesize.
//
// 🔴 PLANTED DEFECT: in buildAnnotationBuckets (internal/compiler/ir/annotations.go)
// drop the copy into the aggregate bucket: every row's IR assertion and every
// OWNER/CENTER assertion fail, while the ZONE assertions still pass.
func TestAggregateAnnotationsReachTheTemplateBucket(t *testing.T) {
	t.Parallel()

	result := pipelinetest.Build(t, pipelinetest.Input{
		ReleasePath:  aggregateAnnotationBucketDir + "release.yaml",
		CatalogPaths: []string{aggregateAnnotationBucketDir + "catalog/base"},
	})
	if result == nil || result.IRGraph == nil {
		t.Fatal("the build returned no graph")
	}

	rows := []struct {
		node   string
		wantIR []string
		owner  string
		center string
	}{
		{"invoice", []string{"team.example/owner=billing-squad", "cost.example/center=CC-3421"}, "billing-squad", "CC-3421"},
		{"payment", []string{"team.example/owner=payments-squad", "cost.example/center=CC-3421"}, "payments-squad", "CC-3421"},
		{"posting", []string{"team.example/owner=billing-squad", "cost.example/center=CC-3422"}, "billing-squad", "CC-3422"},
		{"lone", []string{}, "unset", "unset"},
	}
	for _, row := range rows {
		t.Run(row.node, func(t *testing.T) {
			t.Parallel()
			node, ok := result.IRGraph.Spec.Nodes.Get(row.node)
			if !ok {
				t.Fatalf("no IR node %q; nodes: %v", row.node, result.IRGraph.Spec.Nodes.Keys())
			}
			gotIR := []string{}
			for k, v := range node.AggregateAnnotations.All() {
				gotIR = append(gotIR, k+"="+v)
			}
			if !reflect.DeepEqual(gotIR, row.wantIR) {
				t.Errorf("IR aggregate_annotations = %v, want %v", gotIR, row.wantIR)
			}

			body := artifactEndingIn(t, result.Artifacts, "/"+row.node+"-deployment.yaml")
			for _, want := range []struct{ name, value string }{
				{"OWNER", row.owner},
				{"CENTER", row.center},
				{"LIMITS", "unset"},
				{"ZONE", "z1"},
			} {
				got, found := renderedVariable(body, want.name)
				if !found {
					t.Errorf("variable %s not rendered:\n%s", want.name, body)
					continue
				}
				if got != want.value {
					t.Errorf("variable %s = %q, want %q", want.name, got, want.value)
				}
			}
		})
	}
}

// artifactEndingIn returns the one rendered artifact whose path ends in suffix,
// failing when none or several do.
func artifactEndingIn(t *testing.T, artifacts map[string][]byte, suffix string) string {
	t.Helper()
	var matches []string
	for path := range artifacts {
		if strings.HasSuffix(path, suffix) {
			matches = append(matches, path)
		}
	}
	sort.Strings(matches)
	if len(matches) != 1 {
		paths := make([]string, 0, len(artifacts))
		for path := range artifacts {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		t.Fatalf("artifacts ending in %q = %v, want exactly one; all: %v", suffix, matches, paths)
	}
	return string(artifacts[matches[0]])
}
