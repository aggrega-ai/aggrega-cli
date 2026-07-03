// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1

package helm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"helm.sh/helm/v3/pkg/chart/loader"
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

// renderSourced loads a Helm chart from disk and renders it against the Phase B
// values dictionary. It is the host-side sourced path: resolve the chart root,
// load the chart from the filesystem, then hand it to the filesystem-free
// RenderLoadedChart core so the host and the provider Wasm guest share one
// byte-identical render path. Returns flat rendered YAML files keyed by
// "<component-id>-<basename>.yaml".
func renderSourced(
	ctx context.Context,
	chartPath string,
	values map[string]any,
	nodeCtx renderer.NodeRenderContext,
) (map[string][]byte, error) {
	// Resolve the chart root: a cache-extracted chart sits one level below the
	// extracted directory (under its <chartname>/ dir); a local-path or archive
	// source already points at the chart.
	chartRoot, err := resolveChartRoot(chartPath)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: chartPath}, fmt.Sprintf("helm: node %q: locate chart root in %q", nodeCtx.NodeID, chartPath))
	}
	ch, err := loader.Load(chartRoot)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: chartRoot}, fmt.Sprintf("helm: node %q: load chart %q", nodeCtx.NodeID, chartRoot))
	}

	// nil validator: the host engine keeps the SDK's own values-schema
	// validation, including its live resolution of remote schema references.
	return RenderLoadedChart(ctx, ch, values, nodeCtx, nil)
}
