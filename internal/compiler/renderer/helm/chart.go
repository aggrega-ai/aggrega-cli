// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import (
	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
	"gopkg.in/yaml.v3"
)

const (
	helmChartAPIVersion = "v2"
	defaultChartVersion = "0.1.0"
	defaultChartName    = "application"
)

// generateChartYAML produces the Chart.yaml content.
func generateChartYAML(nodeCtx renderer.NodeRenderContext) ([]byte, error) {
	name := nodeCtx.Provenance.AggregateName
	if name == "" {
		name = defaultChartName
	}
	chart := Chart{
		APIVersion: helmChartAPIVersion,
		Name:       name,
		Version:    defaultChartVersion,
	}
	return yaml.Marshal(&chart)
}
