// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1

package helm

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ProjectChartSource reads a Helm chart's files from the local source at compile
// time and packs them into a SourceProjection the host hands a provider Wasm
// guest, so the guest reconstructs the chart in memory (via loader.LoadFiles)
// and touches no filesystem. It resolves the chart root the same way the host
// sourced path does, then walks that root deterministically (lexical order per
// directory) and packs each regular file as a SourceFile keyed by its
// chart-root-relative, slash-separated path. This is what the host does at
// compile time; the render core (RenderLoadedChart) then renders identically
// whether the chart was loaded from a path (host) or from these bytes (guest).
func ProjectChartSource(chartPath string) (*renderer.SourceProjection, error) {
	chartRoot, err := resolveChartRoot(chartPath)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: chartPath}, fmt.Sprintf("helm: locate chart root in %q", chartPath))
	}

	var files []renderer.SourceFile
	walkErr := filepath.WalkDir(chartRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(chartRoot, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files = append(files, renderer.SourceFile{Path: filepath.ToSlash(rel), Content: data})
		return nil
	})
	if walkErr != nil {
		return nil, cerrors.Wrap(walkErr, cerrors.CatalogError, cerrors.SourceLocation{File: chartRoot}, fmt.Sprintf("helm: project chart source from %q", chartRoot))
	}

	return &renderer.SourceProjection{Files: files}, nil
}

// ProjectSource implements renderer.SourceProjector by delegating to
// ProjectChartSource, which remains the single-source projection impl the
// in-tree sourced path and the provider Wasm guest both build on.
func (e *HelmEngine) ProjectSource(path string) (*renderer.SourceProjection, error) {
	return ProjectChartSource(path)
}
