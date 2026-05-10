// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchart

import (
	"context"
	"fmt"
	"sort"

	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/cognitio/aggrega-cli/pkg/errors"
	"gopkg.in/yaml.v3"
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
	files, err := BuildUmbrella(info.BoundaryPath, info.AggregateName, info.SubCharts, info.ChartName)
	if err != nil {
		return nil, false, err
	}
	return files, true, nil
}

// umbrellaChartDependency mirrors a Helm umbrella Chart.yaml dependency entry.
type umbrellaChartDependency struct {
	Name       string `yaml:"name"`
	Version    string `yaml:"version"`
	Alias      string `yaml:"alias,omitempty"`
	Repository string `yaml:"repository"`
}

// umbrellaChart is the minimal umbrella Chart.yaml shape.
type umbrellaChart struct {
	APIVersion   string                    `yaml:"apiVersion"`
	Name         string                    `yaml:"name"`
	Version      string                    `yaml:"version"`
	Description  string                    `yaml:"description,omitempty"`
	Dependencies []umbrellaChartDependency `yaml:"dependencies,omitempty"`
}

// BuildUmbrella produces the umbrella Chart.yaml + values.yaml for a
// dispatch path. Returns a map of relative paths (e.g. "Chart.yaml",
// "values.yaml") → contents. The caller prefixes these with the dispatch
// path when placing them.
//
// chartNameOverride is the cascaded `aggrega.ai/chart-name` annotation
// value when present (set by the orchestrator from the resolver's
// per-boundary annotation cascade). When non-empty, BuildUmbrella uses
// it verbatim as the chart's name — the cascade-driven matching is the
// merged-path scenario's name source. When empty, BuildUmbrella falls
// back to the path-derived name (today's behavior).
func BuildUmbrella(boundaryName, aggregateName string, subCharts []renderer.SubChartInfo, chartNameOverride string) (map[string][]byte, error) {
	if boundaryName == "" {
		return nil, cerrors.New(cerrors.OpsError, cerrors.SourceLocation{}, "helmchart umbrella: empty boundary name", nil, nil)
	}

	// Sorted iteration for determinism
	sort.Slice(subCharts, func(i, j int) bool {
		return subCharts[i].InstanceID < subCharts[j].InstanceID
	})

	var name string
	if chartNameOverride != "" {
		name = sanitizeChartName(chartNameOverride)
	} else {
		name = sanitizeChartName(boundaryName)
		if aggregateName != "" {
			name = sanitizeChartName(aggregateName) + "-" + name
		}
	}

	deps := make([]umbrellaChartDependency, 0, len(subCharts))
	values := make(map[string]any, len(subCharts))
	for _, sc := range subCharts {
		alias := sanitizeChartName(sc.InstanceID)
		version := sc.Version
		if version == "" {
			version = defaultChartVersion
		}
		// Sourced sub-charts: dep `name` = user's chart name from Chart.yaml;
		// `alias` = instance_id so Helm scopes umbrella values under that key
		// and multiple instances of the same source chart don't collide.
		// Generative sub-charts: name == alias == instance_id (no user chart).
		depName := alias
		if s, ok := sc.Source.(string); ok && s != "" {
			name, err := readChartName(s)
			if err != nil {
				return nil, err
			}
			depName = name
		}
		deps = append(deps, umbrellaChartDependency{
			Name:       depName,
			Version:    version,
			Alias:      alias,
			Repository: "file://charts/" + alias,
		})
		if sc.Values != nil {
			values[alias] = sc.Values
		} else {
			values[alias] = map[string]any{}
		}
	}

	umbrella := umbrellaChart{
		APIVersion:   helmChartAPIVersion,
		Name:         name,
		Version:      defaultChartVersion,
		Description:  fmt.Sprintf("Aggrega umbrella chart for boundary %q", boundaryName),
		Dependencies: deps,
	}

	chartYAML, err := yaml.Marshal(&umbrella)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: boundaryName}, "helmchart umbrella: marshal Chart.yaml")
	}

	valuesYAML, err := yaml.Marshal(values)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: boundaryName}, "helmchart umbrella: marshal values.yaml")
	}

	return map[string][]byte{
		"Chart.yaml":  chartYAML,
		"values.yaml": valuesYAML,
	}, nil
}
