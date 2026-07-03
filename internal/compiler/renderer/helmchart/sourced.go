// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1

package helmchart

import (
	"fmt"
	"os"
	"path/filepath"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// readChartMeta parses a local chart's Chart.yaml and returns its `name` and
// `version` fields. Used to mirror a vendored sub-chart in BOTH name and version —
// the alias identifies the instance, but the version must match the physically-
// copied chart or Helm will not bind the dependency (and the umbrella's
// per-instance values never reach the sub-chart). An empty name or version, a
// parse failure, or a multi-document Chart.yaml halts loudly, so a future drift
// cannot silently drop the alias.
//
// It reads the pristine Chart.yaml from disk and hands the bytes to
// readChartMetaBytes for the single-sourced decode-and-guardrail logic.
func readChartMeta(chartPath string) (name, version string, err error) {
	data, err := os.ReadFile(filepath.Join(chartPath, "Chart.yaml"))
	if err != nil {
		return "", "", cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: chartPath}, fmt.Sprintf("helmchart sourced: read Chart.yaml in %q", chartPath))
	}
	return readChartMetaBytes(data, chartPath)
}
