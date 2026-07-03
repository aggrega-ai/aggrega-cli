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

// ProjectSourcedChart reads a local Helm chart directory at compile time and
// packs its files into a SourceProjection the host hands a provider Wasm guest,
// so the guest reconstructs the sub-chart in memory and touches no filesystem. It
// walks the chart directory deterministically (lexical order per directory) and
// packs each regular file as a SourceFile keyed by its chart-root-relative,
// slash-separated path (e.g. "Chart.yaml", "templates/deployment.yaml"). A source
// that fails to stat, is not a directory, or carries no Chart.yaml halts loudly.
// This is what the host does at compile time; the render core
// (renderSourcedChartCore) then rebuilds the sub-chart's file map identically
// whether the chart was projected from a path (host) or from these bytes (guest).
func ProjectSourcedChart(chartPath string) (*renderer.SourceProjection, error) {
	info, err := os.Stat(chartPath)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: chartPath}, fmt.Sprintf("helmchart sourced: stat %q", chartPath))
	}
	if !info.IsDir() {
		return nil, cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: chartPath}, fmt.Sprintf("helmchart sourced: source %q is not a directory", chartPath), nil, nil)
	}

	var files []renderer.SourceFile
	haveChartYAML := false
	err = filepath.WalkDir(chartPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(chartPath, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: path}, fmt.Sprintf("read %q", path))
		}
		slashRel := filepath.ToSlash(rel)
		if slashRel == "Chart.yaml" {
			haveChartYAML = true
		}
		files = append(files, renderer.SourceFile{Path: slashRel, Content: data})
		return nil
	})
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: chartPath}, fmt.Sprintf("helmchart sourced: walk %q", chartPath))
	}
	if !haveChartYAML {
		return nil, cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: chartPath}, fmt.Sprintf("helmchart sourced: %q has no Chart.yaml", chartPath), nil, nil)
	}
	return &renderer.SourceProjection{Files: files}, nil
}

// ProjectSource implements renderer.SourceProjector by delegating to
// ProjectSourcedChart, which remains the single-source projection impl the
// in-tree sourced path and the provider Wasm guest both build on.
func (e *HelmEngine) ProjectSource(path string) (*renderer.SourceProjection, error) {
	return ProjectSourcedChart(path)
}
