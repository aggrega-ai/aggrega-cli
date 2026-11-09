// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The two values the attached policy decides. Each is written by one version of the
// policy's source file, so a graph carrying one of them shows which version of the
// source the transform ran with.
const (
	transformSourceFirst  = "decided-by-the-first-source"
	transformSourceSecond = "decided-by-the-second-source"
)

// transformSourceRole is one transform role the inspection is held to: the export
// the role dispatches, the policy deciding a value from its source file, and what
// the workspace needs beyond the shared one-component fixture for that decision to
// reach the graph.
type transformSourceRole struct {
	role   string
	export string
	policy func(value string) string
	extend func(t *testing.T, root string)
}

// labelTransformSourcePolicy decides one label on every server node.
func labelTransformSourcePolicy(value string) string {
	return `package proof

import rego.v1

labels contains {"node": id, "key": "proof.example/source", "value": "` + value + `"} if {
	some id, node in input.graph.nodes
	node.labels["component.aggrega.ai/type"] == "server"
}
`
}

// annotationTransformSourcePolicy decides one annotation, under the key the
// Manifest binds, on every component.
func annotationTransformSourcePolicy(value string) string {
	return `package proof

import rego.v1

annotations contains {
	"boundary_path": target.boundary_path,
	"component_name": target.component_name,
	"annotation_key": "gate.example.com/tier",
	"annotation_value": "` + value + `",
} if {
	some target in input.targets
	"gate.example.com/tier" in input.binding_keys
}
`
}

// extendWithAnnotationBinding adds a Feature and its compose realization to the
// workspace catalog and binds the annotation the policy decides to that Feature, so
// the decided value expands into a Feature attachment the graph records.
func extendWithAnnotationBinding(t *testing.T, root string) {
	t.Helper()
	catalogDir := filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogDir, "feature", "core", "annotation-gate", "1.0.0", "feature.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Feature
metadata:
  name: core/annotation-gate
  version: "1.0.0"
spec:
  schema:
    infra:
      tier: { type: string }
`)
	writeFixture(t, filepath.Join(catalogDir, "feature-recipe", "compose", "annotation-gate", "1.0.0", "recipe.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: FeatureRecipe
metadata:
  name: annotation-gate-compose
  version: "1.0.0"
spec:
  capability: compose
  implements: core/annotation-gate
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        test/svc:1.0.0: compose/svc:1.0.0
    features:
      type:
        core/annotation-gate: annotation-gate-compose:1.0.0
      annotations:
        gate.example.com/tier:
          feature: core/annotation-gate
          maps:
            infra:
              - tier
`)
}

// TestInspectIRGraphRunsTransformsWithTheSourcesABuildHandsThem holds that the graph
// `aggrega inspect <release> --ir-graph` draws is the graph `aggrega build --ir-graph`
// produces for the same workspace and Release, where a transform attachment decides
// what it adds from its source file.
//
// Both commands are driven through their real dispatch over a lockfile the real pull
// wrote, offline, with the Rego guest the binary carries. Per role, three moments:
//
//   - after the pull, both graphs carry the value the source decides, and their
//     nodes are equal;
//   - after the source file changes and before the next pull, both still carry the
//     value the pull recorded — the source a transform reads is the one pinned;
//   - after the next pull, both carry the new value, and their nodes are equal again.
//
// A transform that ran without its source file decides nothing, so an inspection
// that composed its attachments from the live configuration alone draws neither
// value, while the build draws the first: the first moment fails on it.
func TestInspectIRGraphRunsTransformsWithTheSourcesABuildHandsThem(t *testing.T) {
	own := embeddedRegoVersion(t)

	for _, row := range []transformSourceRole{
		{role: "label-transform", export: "mutate", policy: labelTransformSourcePolicy},
		{role: "annotation-transform", export: "transform_annotations", policy: annotationTransformSourcePolicy,
			extend: extendWithAnnotationBinding},
	} {
		t.Run(row.role, func(t *testing.T) {
			root, releasePath, _ := catalogGovernanceAttachmentWorkspace(t, catalogGovernanceAttachment{
				artifact: regoArtifact, export: row.export, role: row.role,
			}, nil, "offline: true\n")
			if row.extend != nil {
				row.extend(t, root)
			}
			writeWorkspaceConfig(t, root, "workspace:\n  catalog_dirs:\n    - ./catalog\noffline: true\nauto_pull: false\n"+
				"plugins:\n  governance:\n    - uri: "+regoArtifact+":"+own+"\n      role: "+row.role+"\n"+
				"      with:\n        sources:\n          - policy.rego\n")
			policyPath := filepath.Join(root, "policy.rego")
			writeFixture(t, policyPath, row.policy(transformSourceFirst))
			t.Chdir(root)
			ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})

			pull := func(moment string) {
				t.Helper()
				if err := pullRun(context.Background(), pullConfig{ws: ws, ReleasePath: releasePath}); err != nil {
					t.Fatalf("%s: pull: %v", moment, err)
				}
			}

			pull("the first source")
			assertInspectDrawsTheBuildsNodes(t, "after the first pull", ws, root, releasePath,
				transformSourceFirst, transformSourceSecond)

			writeFixture(t, policyPath, row.policy(transformSourceSecond))
			assertInspectDrawsTheBuildsNodes(t, "the source changed, not yet pulled", ws, root, releasePath,
				transformSourceFirst, transformSourceSecond)

			pull("the second source")
			assertInspectDrawsTheBuildsNodes(t, "after the second pull", ws, root, releasePath,
				transformSourceSecond, transformSourceFirst)
		})
	}
}

// assertInspectDrawsTheBuildsNodes builds the Release with --ir-graph and inspects it
// with --ir-graph, then holds both graphs' node to the value the pinned source
// decides — present, and the other value absent — and the two node sets to each
// other.
func assertInspectDrawsTheBuildsNodes(t *testing.T, moment string, ws *workspaceconfig.ResolvedWorkspace, root, releasePath, want, absent string) {
	t.Helper()

	var buildOut bytes.Buffer
	run := rootedBuildOptions(t, root, buildRunOptions(&buildFlags{irGraph: true}, &buildOut))
	if err := dispatchBuildWithDiag(context.Background(), ws, releasePath, nil, false,
		buildDiagOptions{quiet: true}, run, io.Discard, &buildOut); err != nil {
		t.Fatalf("%s: build --ir-graph: %v", moment, err)
	}
	var inspectOut bytes.Buffer
	opts := testInspectOptions(&inspectOut)
	opts.irGraph = true
	if err := dispatchInspect(context.Background(), ws, releasePath, nil, opts); err != nil {
		t.Fatalf("%s: inspect --ir-graph: %v", moment, err)
	}

	built := transformSourceNodes(t, moment+": build", buildOut.String())
	inspected := transformSourceNodes(t, moment+": inspect", inspectOut.String())
	for _, side := range []struct {
		command string
		nodes   map[string]any
	}{{"build", built}, {"inspect", inspected}} {
		node, err := json.Marshal(side.nodes["app"])
		if err != nil {
			t.Fatalf("%s: encode the %s graph's app node: %v", moment, side.command, err)
		}
		if !strings.Contains(string(node), `"`+want+`"`) {
			t.Errorf("%s: the app node %s --ir-graph draws carries no %q, the value the pinned source decides:\n%s",
				moment, side.command, want, node)
		}
		if strings.Contains(string(node), `"`+absent+`"`) {
			t.Errorf("%s: the app node %s --ir-graph draws carries %q, which the pinned source does not decide:\n%s",
				moment, side.command, absent, node)
		}
	}
	if !reflect.DeepEqual(built, inspected) {
		b, _ := json.Marshal(built)
		i, _ := json.Marshal(inspected)
		t.Errorf("%s: the nodes inspect --ir-graph draws differ from the nodes build --ir-graph produced:\nbuild:   %s\ninspect: %s",
			moment, b, i)
	}
}

// transformSourceNodes returns the node set of the graph leading a command's
// output stream.
func transformSourceNodes(t *testing.T, subject, stream string) map[string]any {
	t.Helper()
	graph, _ := decodeLeadingGraph(t, stream)
	spec, _ := graph["spec"].(map[string]any)
	nodes, ok := spec["nodes"].(map[string]any)
	if !ok || len(nodes) == 0 {
		t.Fatalf("%s: the graph carries no nodes", subject)
	}
	return nodes
}
