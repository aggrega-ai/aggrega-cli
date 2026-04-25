// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchart

import "strings"

const (
	helmChartAPIVersion = "v2"
	defaultChartVersion = "0.1.0"
)

// sanitizeChartName converts dot-notation instance IDs (e.g. "data.main-db")
// to Helm-compatible names (e.g. "data-main-db"). Used by umbrella.go to
// derive umbrella Chart.yaml names and per-sub-chart aliases from instance
// IDs that may contain dots from sub-aggregate flattening.
func sanitizeChartName(id string) string {
	r := strings.NewReplacer(".", "-", "_", "-")
	return r.Replace(id)
}
