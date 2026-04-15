// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchart

import (
	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
	"gopkg.in/yaml.v3"
)

const (
	helmChartAPIVersion = "v2"
	defaultChartVersion = "0.1.0"
	defaultChartName    = "application"
)

// generateChartYAML produces the sub-chart Chart.yaml content.
// The sub-chart name matches the node's instance ID so it maps 1:1 with the
// umbrella chart's dependency alias.
func generateChartYAML(nodeCtx renderer.NodeRenderContext) ([]byte, error) {
	name := sanitizeChartName(nodeCtx.NodeID)
	if name == "" {
		name = defaultChartName
	}
	version := nodeCtx.ResolvedVersion
	if version == "" {
		version = defaultChartVersion
	}
	chart := Chart{
		APIVersion: helmChartAPIVersion,
		Name:       name,
		Version:    version,
	}
	return yaml.Marshal(&chart)
}

// sanitizeChartName converts dot-notation instance IDs (e.g. "data.main-db")
// to Helm-compatible names (e.g. "data-main-db").
func sanitizeChartName(id string) string {
	return sanitizeContainerName(id)
}
