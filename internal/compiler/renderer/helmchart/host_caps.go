// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1

package helmchart

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// HostCaps returns helm-chart's host-side capability holder, built from the
// provider package's standalone functions rather than methods on a concrete
// engine — so the host can resolve helm-chart's host capabilities independently
// of any engine identity, including when a node renders through a Wasm guest. The
// input kinds, boundary-assembly mode, and artifact-template delimiters are not
// held here; they are the guest's own describe() self-report. Artifact placement
// (templates/<name> path, provenance splice) is the guest's concern too.
func HostCaps() renderer.HostCapabilities {
	return renderer.HostCapabilities{
		Codec:            NewHelmChartCodec,
		ProjectSubCharts: projectSubCharts,
	}
}

// projectSubCharts reads each sourced sub-chart's pristine Chart.yaml from disk
// into a filesystem-free projection, carrying instance identity, version, and
// per-instance values through. It is the single home for the umbrella's pristine
// Chart.yaml read; the Wasm assembler route projects each sub-chart through it
// so the guest assembles the umbrella from pure data and touches no filesystem.
// A generative sub-chart (nil/empty source) carries no ChartYAML and takes the
// generative fallback in the assembly core.
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
