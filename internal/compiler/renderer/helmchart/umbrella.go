// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1

package helmchart

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// AssembleBoundary implements renderer.BoundaryAssembler: it emits the umbrella
// Chart.yaml + values.yaml for the dispatch path. Per-node files are nested
// under `charts/<nodeID>/` by the orchestrator (nestPerNode = true).
//
// Merged-path case: when info.ContributingBoundaries lists more than one
// boundary, the umbrella aggregates contributors from every boundary into
// one Chart.yaml. The orchestrator's CHART_NAME_MISMATCH guardrail
// enforces consistent `aggrega.ai/chart-name` values across contributors
// before AssembleBoundary runs; when info.ChartName is set, the umbrella
// uses it as the chart's name. Otherwise the umbrella derives the name
// from the dispatch path (today's behavior).
func (e *HelmEngine) AssembleBoundary(_ context.Context, info renderer.BoundaryInfo) (map[string][]byte, bool, error) {
	files, err := BuildUmbrella(info.BoundaryPath, info.AggregateName, info.SubCharts, info.ChartName, info.YAMLIndent)
	if err != nil {
		return nil, false, err
	}
	return files, true, nil
}

// BuildUmbrella produces the umbrella Chart.yaml + values.yaml for a
// dispatch path. Returns a map of relative paths (e.g. "Chart.yaml",
// "values.yaml") → contents. The caller prefixes these with the dispatch
// path when placing them.
//
// It is the host adapter over AssembleUmbrellaCore: it projects each sub-chart
// through projectSubCharts — the single home for the umbrella's pristine
// Chart.yaml read — into filesystem-free CoreSubCharts, then delegates the
// assembly to the core. The core reads no disk, so the umbrella output is
// identical whether the sub-charts' Chart.yaml bytes come from the local source
// (host) or from a provider Wasm guest's payload.
//
// chartNameOverride is the cascaded `aggrega.ai/chart-name` annotation
// value when present (set by the orchestrator from the resolver's
// per-boundary annotation cascade). When non-empty, the chart's name is
// that value verbatim — the cascade-driven matching is the merged-path
// scenario's name source. When empty, the name is derived from the
// dispatch path (today's behavior).
func BuildUmbrella(boundaryName, aggregateName string, subCharts []renderer.SubChartInfo, chartNameOverride string, indent int) (map[string][]byte, error) {
	projs, err := projectSubCharts(subCharts)
	if err != nil {
		return nil, err
	}
	subs := make([]CoreSubChart, 0, len(projs))
	for _, p := range projs {
		subs = append(subs, CoreSubChart{
			InstanceID: p.InstanceID,
			Version:    p.Version,
			Values:     p.Values,
			ChartYAML:  p.ChartYAML,
		})
	}
	return AssembleUmbrellaCore(boundaryName, aggregateName, subs, chartNameOverride, indent)
}

// ProjectSubCharts implements renderer.SubChartProjector: it projects each
// sub-chart's identity, per-instance values, and the pristine bytes of its
// vendored Chart.yaml (read from the local filesystem at compile time) into
// filesystem-free projections, so a provider Wasm guest assembles the umbrella
// from pure data and touches no filesystem.
func (e *HelmEngine) ProjectSubCharts(subCharts []renderer.SubChartInfo) ([]renderer.SubChartProjection, error) {
	return projectSubCharts(subCharts)
}

// projectSubCharts reads each sourced sub-chart's pristine Chart.yaml from disk
// into a filesystem-free projection, carrying instance identity, version, and
// per-instance values through. It is the single home for the umbrella's pristine
// Chart.yaml read: both the in-tree host assembly (BuildUmbrella) and the wire
// projection (HelmEngine.ProjectSubCharts) run this one read, so the two paths
// cannot diverge on how a vendored chart's bytes are sourced. A generative
// sub-chart (nil/empty source) carries no ChartYAML and takes the generative
// fallback in the core.
func projectSubCharts(subCharts []renderer.SubChartInfo) ([]renderer.SubChartProjection, error) {
	projs := make([]renderer.SubChartProjection, 0, len(subCharts))
	for _, sc := range subCharts {
		proj := renderer.SubChartProjection{
			InstanceID: sc.InstanceID,
			Version:    sc.Version,
			Values:     sc.Values,
		}
		if s, ok := sc.Source.(string); ok && s != "" {
			data, err := os.ReadFile(filepath.Join(s, "Chart.yaml"))
			if err != nil {
				return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: s}, fmt.Sprintf("helmchart sourced: read Chart.yaml in %q", s))
			}
			proj.ChartYAML = data
		}
		projs = append(projs, proj)
	}
	return projs, nil
}
