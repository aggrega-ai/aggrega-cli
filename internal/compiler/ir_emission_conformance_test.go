// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Emitted-IR conformance — the output-side twin of the document-envelope
// gate. Every end-to-end fixture build's emitted IR document is validated
// against ir-v1beta1.schema.json at test time, making pkg/types/ir.go's
// "strictly mirrors ir-v1beta1.schema.json" claim executable. This is a
// TEST-time check only: runtime emit-validation is deliberately not part
// of the pipeline (the compiler trusts its own IR builder).
package compiler_test

import (
	"encoding/json"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// TestEmittedIRConformsToSchema builds each green e2e fixture through the
// real pipeline, marshals the emitted IRGraph, and validates it against
// the IR schema through the one validation component. The fixture set
// spans the distinct e2e trees (synthesis + sourced, sub-aggregates,
// per-edge config, capability-axis sockets, boundary features).
func TestEmittedIRConformsToSchema(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		releasePath  string
		catalogPaths []string
	}{
		{"capability-socket", "testdata/capability-socket/release-green.yaml", []string{"testdata/capability-socket/catalog"}},
		{"scope-feature-uses", "testdata/scope-feature-uses/release.yaml", []string{"testdata/scope-feature-uses/catalog/base"}},
		{"uses-edge-config", "testdata/uses-edge-config/release.yaml", []string{"testdata/uses-edge-config/catalog/base"}},
		{"uses-edge-defaults", "testdata/uses-edge-config/release-edge-defaults.yaml", []string{"testdata/uses-edge-config/catalog/base"}},
		{"uses-edge-subagg", "testdata/uses-edge-subagg/release.yaml", []string{"testdata/uses-edge-config/catalog/base", "testdata/uses-edge-subagg/catalog/base"}},
		{"uses-edge-subagg-adhoc-socket", "testdata/uses-edge-subagg/release-badsocket.yaml", []string{"testdata/uses-edge-config/catalog/base", "testdata/uses-edge-subagg/catalog/base"}},
		{"compose-bridge", "testdata/compose-bridge/release.yaml", []string{
			"testdata/compose-bridge/catalog/base",
			"../../../aggrega-hub/aggrega-catalog/feature/core/compose-bridge/1.0.0/feature.yaml",
			"../../../aggrega-hub/aggrega-catalog/feature-recipe/compose/bridge",
			"../../../aggrega-hub/aggrega-catalog/schemalibrary/base-components/1.0.0/library.yaml",
		}},
		{"determinism-fullcompile", "testdata/determinism/fullcompile/release.yaml", []string{"testdata/determinism/fullcompile/catalog/base", "testdata/determinism/fullcompile/catalog/recipes/helm"}},
		{"fullstack-helm-synthesized", "testdata/fullstack-app/releases/helm-synthesized.yaml", []string{"testdata/fullstack-app/catalog/base", "testdata/fullstack-app/catalog/recipes/helm-synthesized"}},
		{"fullstack-compose-synthesized", "testdata/fullstack-app/releases/compose-synthesized.yaml", []string{"testdata/fullstack-app/catalog/base", "testdata/fullstack-app/catalog/recipes/compose-synthesized"}},
		{"feature-matrix-compose-synthesized", "testdata/feature-matrix/releases/compose-synthesized.yaml", []string{"testdata/feature-matrix/catalog/base", "testdata/feature-matrix/catalog/recipes/compose-synthesized"}},
		// The one fixture that authors a per-authored-leaf marking at all three
		// of the units the graph carries one at — the node's own pillars, an
		// attachment's payloads, and a boundary attachment's infra. Every one of
		// those markings is omitted when empty, so a schema property no Go field
		// fills, or a Go field the schema does not declare, would go unremarked
		// on every other row here.
		{"authored-leaf-marking", "testdata/authored-leaf-marking/release.yaml", []string{"testdata/authored-leaf-marking/catalog/base"}},
		// The one fixture carrying an attachment written on a connection. It is keyed
		// on the consuming node by its qualified name, which holds a dot, and it
		// carries the connection it belongs to — so the attachment-name key pattern
		// and the connection field are both exercised here and on no other row.
		{"connection-attachment", "testdata/connection-attachment/release.yaml", []string{"testdata/connection-attachment/catalog/base"}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			result := pipelinetest.Build(t, pipelinetest.Input{
				ReleasePath:  c.releasePath,
				CatalogPaths: c.catalogPaths,
			})
			if result.IRGraph == nil {
				t.Fatalf("%s: pipeline produced no IR graph", c.name)
			}
			assertIRConforms(t, c.name+" as built", result.IRGraph)

			// The withheld shape is the one a command hands back, and it is a
			// DIFFERENT document: the withholding replaces authored payload leaves
			// with a marker object, so a schema that admitted the values and not
			// the marker would accept every row above and reject everything a
			// person can actually obtain.
			assertIRConforms(t, c.name+" as a command emits it", redact.Graph(result.IRGraph))
		})
	}
}

// assertIRConforms marshals a graph the way every surface that emits one does
// and validates it against the IR schema through the one validation component.
func assertIRConforms(t *testing.T, subject string, graph *types.IRGraph) {
	t.Helper()
	raw, err := json.Marshal(graph)
	if err != nil {
		t.Fatalf("%s: marshal IR: %v", subject, err)
	}
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		t.Fatalf("%s: re-decode IR: %v", subject, err)
	}
	findings, err := envelope.ValidateInstance(envelope.KindIR, instance)
	if err != nil {
		t.Fatalf("%s: IR schema engine error: %v", subject, err)
	}
	if len(findings) > 0 {
		t.Errorf("%s: emitted IR violates ir-v1beta1.schema.json (%d finding(s)); the ir.go hand-mirror has drifted from the schema:\n%s\n--- first findings ---\n%+v",
			subject, len(findings), raw, findings[:min(4, len(findings))])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
