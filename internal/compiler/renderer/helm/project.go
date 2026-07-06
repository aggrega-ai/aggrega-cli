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

// resolveChartRoot returns the directory loader.Load expects to find Chart.yaml
// in. A local-path or archive source already points at the chart, so it is
// returned unchanged. A cache-extracted Helm chart, however, untars under a
// single top-level <chartname>/ directory (the OCI/tgz convention), so the
// extracted directory is one level above Chart.yaml; this descends to it.
//
// The descent is a Loud Guardrail: exactly one top-level subdirectory must
// carry Chart.yaml. Zero is a malformed archive; more than one is ambiguous —
// both halt rather than guess. os.ReadDir returns sorted entries, so the
// ambiguity message and the single-match result are deterministic.
func resolveChartRoot(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return path, nil // archive (.tgz) — loader.Load handles it directly
	}
	if _, err := os.Stat(filepath.Join(path, "Chart.yaml")); err == nil {
		return path, nil // chart root: Chart.yaml is here
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return "", err
	}
	var roots []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(path, e.Name(), "Chart.yaml")); err == nil {
			roots = append(roots, e.Name())
		}
	}
	switch len(roots) {
	case 0:
		return "", fmt.Errorf("no Chart.yaml at the extracted archive root or in any top-level subdirectory")
	case 1:
		return filepath.Join(path, roots[0]), nil
	default:
		return "", fmt.Errorf("ambiguous chart layout: top-level subdirectories %v each contain Chart.yaml; cannot choose a chart root", roots)
	}
}

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
