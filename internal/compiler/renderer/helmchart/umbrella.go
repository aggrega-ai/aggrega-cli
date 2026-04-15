// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchart

import (
	"context"
	"fmt"
	"sort"

	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
	"gopkg.in/yaml.v3"
)

// ComposeBoundary implements renderer.BoundaryComposer: it emits the umbrella
// Chart.yaml + values.yaml for the boundary. Per-node files are nested under
// `charts/<nodeID>/` by the orchestrator (nestPerNode = true).
func (e *HelmEngine) ComposeBoundary(_ context.Context, info renderer.BoundaryInfo) (map[string][]byte, bool, error) {
	files, err := BuildUmbrella(info.BoundaryPath, info.AggregateName, info.SubCharts)
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

// BuildUmbrella produces the umbrella Chart.yaml + values.yaml for a boundary.
// Returns a map of relative paths (e.g. "Chart.yaml", "values.yaml") → contents.
// The caller prefixes these with `boundaries/<boundary>/` when placing them.
func BuildUmbrella(boundaryName, aggregateName string, subCharts []renderer.SubChartInfo) (map[string][]byte, error) {
	if boundaryName == "" {
		return nil, fmt.Errorf("helmchart umbrella: empty boundary name")
	}

	// Sorted iteration for determinism
	sort.Slice(subCharts, func(i, j int) bool {
		return subCharts[i].InstanceID < subCharts[j].InstanceID
	})

	name := sanitizeChartName(boundaryName)
	if aggregateName != "" {
		name = sanitizeChartName(aggregateName) + "-" + name
	}

	deps := make([]umbrellaChartDependency, 0, len(subCharts))
	values := make(map[string]interface{}, len(subCharts))
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
			values[alias] = map[string]interface{}{}
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
		return nil, fmt.Errorf("helmchart umbrella: marshal Chart.yaml: %w", err)
	}

	valuesYAML, err := yaml.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("helmchart umbrella: marshal values.yaml: %w", err)
	}

	return map[string][]byte{
		"Chart.yaml":  chartYAML,
		"values.yaml": valuesYAML,
	}, nil
}
