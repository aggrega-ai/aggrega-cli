// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchart

import "strings"

const (
	helmChartAPIVersion = "v2"
	defaultChartVersion = "0.1.0"
)

// sanitizeChartName converts an identifier into a Helm-valid chart name. Dots
// (e.g. from sub-aggregate-flattened instance IDs like "data.main-db"),
// underscores, and path separators all become dashes — so "data.main-db" becomes
// "data-main-db" and a boundary path like "dist/backend/helm-chart" becomes
// "dist-backend-helm-chart". Path separators matter because umbrella.go derives
// the umbrella Chart.yaml name (and the per-sub-chart aliases) from the boundary
// path, and Helm rejects a chart name containing "/". Replacing rather than
// collapsing preserves the full path structure, so distinct boundaries keep
// distinct names.
func sanitizeChartName(id string) string {
	r := strings.NewReplacer(".", "-", "_", "-", "/", "-")
	return r.Replace(id)
}
