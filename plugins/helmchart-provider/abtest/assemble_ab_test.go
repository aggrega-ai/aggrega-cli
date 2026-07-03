// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package abtest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/helmchart"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/wasmengine"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// TestWasmEngineAssemblerUmbrellaByteIdentity proves the host-side
// wasmengine.AssemblerEngine reproduces the in-tree HelmEngine.AssembleBoundary
// byte-for-byte — the umbrella Chart.yaml AND values.yaml AND the nest-per-node
// signal — when both consume the same host renderer.BoundaryInfo. The candidate
// projects the BoundaryInfo onto the assemble_boundary wire (the host reads each
// vendored sub-chart's pristine Chart.yaml at compile time), loads the
// freshly-built guest through the production describe-gated loader with the
// AOT-purity import firewall ON, invokes the export, and folds the guest's output
// back into the BoundaryAssembler's (files, nestPerNode) return.
//
// Two facets the wire extension added are exercised so a candidate that dropped
// any of them would diverge from the oracle:
//
//   - the sub-chart projection: each sub-chart carries a REAL vendored Chart.yaml
//     on disk (via SubChartInfo.Source), so the host's pristine read and the
//     core's ReadChartMetaBytes name/version derivation are exercised — the
//     umbrella dependency name and version come from the vendored Chart.yaml, not
//     the spec version.
//   - the chart-name cascade: one sub-case sets BoundaryInfo.ChartName (the name
//     is used verbatim) and one leaves it empty (the name is derived from the
//     dispatch path plus the aggregate name).
//
// The sub-chart values carry integer-authored numeric values (replicaCount: 3,
// nested service.port: 8080, db port: 5432), nested maps, and strings, so the
// umbrella values.yaml exercises the JSON int->float64 widening across the wire:
// the in-tree oracle marshals Go ints, while the wire round-trip decodes JSON
// numbers to float64 in the guest. A byte-identical values.yaml proves the
// widening is not observable in the rendered output.
//
// The oracle-content assertions below guard non-vacuity: they fail if the oracle
// stopped producing the dependency name/version or the scoped numeric values,
// which would make the byte-identity comparison prove nothing.
func TestWasmEngineAssemblerUmbrellaByteIdentity(t *testing.T) {
	ctx := context.Background()
	wasmBytes, err := os.ReadFile(buildPluginWasm(t))
	if err != nil {
		t.Fatalf("read guest wasm: %v", err)
	}

	cases := []struct {
		name      string
		chartName string // BoundaryInfo.ChartName; "" exercises path+aggregate-derived naming
	}{
		{name: "chart-name-cascade", chartName: "custom-umbrella"},
		{name: "path-derived-name", chartName: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info := assembleBoundaryInfo(t, tc.chartName)

			// ORACLE — the in-tree helm-chart engine's BoundaryAssembler.
			oracle, oracleNest, err := (&helmchart.HelmEngine{}).AssembleBoundary(ctx, info)
			if err != nil {
				t.Fatalf("oracle AssembleBoundary: %v", err)
			}

			// Non-vacuity guards on the oracle output. The dependency name/version
			// must come from the vendored Chart.yaml, and the scoped numeric values
			// must be present, or the byte-identity comparison is vacuous.
			chartYAML := string(oracle["Chart.yaml"])
			valuesYAML := string(oracle["values.yaml"])
			if chartYAML == "" || valuesYAML == "" {
				t.Fatalf("oracle produced empty umbrella: Chart.yaml=%d bytes values.yaml=%d bytes", len(chartYAML), len(valuesYAML))
			}
			for _, want := range []string{"dependencies:", "api-chart", "1.2.3", "db-chart", "4.5.6", "alias: api", "alias: db"} {
				if !strings.Contains(chartYAML, want) {
					t.Fatalf("oracle Chart.yaml missing %q — the A/B would be vacuous:\n%s", want, chartYAML)
				}
			}
			// Assert the UNQUOTED numeric forms: the oracle renders Go int / float64
			// unquoted, so a byte-identical candidate proves the guest's json.Number
			// renormalization reproduces the same unquoted scalars (an integer, a
			// nested integer, and a non-integral float).
			for _, want := range []string{"replicaCount: 3", "port: 8080", "port: 5432", "weight: 1.5"} {
				if !strings.Contains(valuesYAML, want) {
					t.Fatalf("oracle values.yaml missing %q — the A/B would be vacuous:\n%s", want, valuesYAML)
				}
			}

			// CANDIDATE — the AssemblerEngine, which internally wire-round-trips the
			// same BoundaryInfo through the freshly-built guest via the production
			// loader (firewall ON). New wraps the raw helm-chart Go engine; the
			// adapter type-asserts it to renderer.SubChartProjector and drives the
			// host-side pristine read.
			asm := wasmengine.NewAssembler(cannedLookup(wasmBytes), "test://helm-chart-renderer", &helmchart.HelmEngine{}, &wasm.Loader{})
			candidate, candNest, err := asm.AssembleBoundary(ctx, info)
			if err != nil {
				t.Fatalf("candidate AssembleBoundary: %v", err)
			}

			assertByteIdentical(t, oracle, candidate)

			if oracleNest != candNest {
				t.Fatalf("nestPerNode differs: oracle %v, candidate %v", oracleNest, candNest)
			}
			if !candNest {
				t.Errorf("helm-chart nests per node; nest_per_node must be true, got %v", candNest)
			}
		})
	}
}

// assembleBoundaryInfo builds a two-sub-chart dispatch group. Each sub-chart has a
// REAL vendored Chart.yaml on disk (Source path) so the pristine read and the
// name/version derivation are exercised, and per-instance values carrying
// integer-authored numerics (top-level and nested), a non-integral float, nested
// maps, and strings so the umbrella values.yaml exercises the wire's numeric
// round-trip in both branches: the wire codec decodes JSON numerics to
// json.Number, which the guest renormalizes to int64 / float64 so go-yaml renders
// them unquoted, matching the in-tree engine's Go int / float64 values.
func assembleBoundaryInfo(t *testing.T, chartName string) renderer.BoundaryInfo {
	t.Helper()
	apiChart := writeVendoredChart(t, "api-chart", "1.2.3")
	dbChart := writeVendoredChart(t, "db-chart", "4.5.6")

	return renderer.BoundaryInfo{
		BoundaryPath:  "shared-stage",
		AggregateName: "shop",
		ChartName:     chartName,
		SubCharts: []renderer.SubChartInfo{
			{
				InstanceID: "api",
				Version:    "0.0.0", // superseded by the vendored Chart.yaml's version
				Source:     apiChart,
				Values: map[string]any{
					"replicaCount": 3,
					"weight":       1.5, // non-integral float exercises the float64 branch
					"image":        "api:1.0",
					"service": map[string]any{
						"port": 8080,
						"type": "ClusterIP",
					},
				},
			},
			{
				InstanceID: "db",
				Source:     dbChart,
				Values: map[string]any{
					"port":     5432,
					"password": "secret",
				},
			},
		},
		YAMLIndent: 2,
	}
}

// writeVendoredChart writes a minimal but real Chart.yaml (apiVersion, name,
// version) into a fresh temp directory and returns the directory path, so the
// host's pristine Chart.yaml read and the core's name/version derivation run
// against a real file on disk.
func writeVendoredChart(t *testing.T, name, version string) string {
	t.Helper()
	dir := t.TempDir()
	body := fmt.Sprintf("apiVersion: v2\nname: %s\nversion: %s\ndescription: vendored chart\n", name, version)
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write vendored Chart.yaml: %v", err)
	}
	return dir
}
