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

// hostPathForArtifact is the single home for helm-chart's per-node-relative
// $import:-loaded artifact placement. Each artifact file lands at
// templates/<basename> within the per-node output; the orchestrator's
// per-boundary nesting layers boundaries/<bp>/charts/<nodeID>/ on top, so the
// emitted file is positioned alongside the sourced chart's templates and
// participates in Helm's downstream render pass.
func hostPathForArtifact(_ renderer.NodeRenderContext, basename string) string {
	return "templates/" + basename
}

// hostArtifactDelimiters is the single home for helm-chart's $import:-loaded
// artifact delimiter pair. Files use "[[", "]]" for Aggrega's pre-templating
// pass so Helm's "{{ ... }}" template syntax inside the file survives untouched
// into Helm's downstream render. Authors writing $import:-loaded artifact files
// for helm-chart use [[ .pillar ]] for Aggrega context and {{ .Values.x }} for
// Helm context.
func hostArtifactDelimiters() (string, string) {
	return "[[", "]]"
}

// HostCaps returns helm-chart's host-side capability holder, built from the
// provider package's standalone functions rather than methods on a concrete
// engine — so the host can resolve helm-chart's host capabilities independently
// of any engine identity, including when a node renders through a Wasm guest. The
// input kinds and boundary-assembly mode are not held here; they are the guest's
// own describe() self-report.
func HostCaps() renderer.HostCapabilities {
	return renderer.HostCapabilities{
		Codec:              NewHelmChartCodec,
		ProjectSource:      ProjectSourcedChart,
		ProjectSubCharts:   projectSubCharts,
		ExternalArtifacts:  true,
		PathForArtifact:    hostPathForArtifact,
		ArtifactDelimiters: hostArtifactDelimiters,
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
