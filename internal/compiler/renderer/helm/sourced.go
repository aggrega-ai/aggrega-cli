// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
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

// renderSourced loads a Helm chart from disk and renders it against the
// Phase B values dictionary using the Helm Go SDK's template engine.
// Returns flat rendered YAML files keyed by "<nodeID>-<basename>.yaml".
func renderSourced(
	_ context.Context,
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

	// Coalesce the Phase B dict with the chart's default values
	cvals, err := chartutil.CoalesceValues(ch, chartutil.Values(values))
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: chartPath}, fmt.Sprintf("helm: node %q: coalesce values", nodeCtx.NodeID))
	}

	// Build release options. We are not installing — just rendering templates.
	// Namespace comes from the `aggrega.ai/namespace` annotation when present
	// (merged from boundary + boundary_component); otherwise "default".
	ns := renderer.NamespaceFromAnnotations(nodeCtx.Annotations)
	if ns == "" {
		ns = "default"
	}
	relOpts := chartutil.ReleaseOptions{
		Name:      sanitizeContainerName(nodeCtx.NodeID),
		Namespace: ns,
		Revision:  1,
		IsInstall: true,
	}
	caps := chartutil.DefaultCapabilities

	renderVals, err := chartutil.ToRenderValues(ch, cvals, relOpts, caps)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: chartPath}, fmt.Sprintf("helm: node %q: render values", nodeCtx.NodeID))
	}

	// Render
	rendered, err := engine.Engine{}.Render(ch, renderVals)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: chartPath}, fmt.Sprintf("helm: node %q: render", nodeCtx.NodeID))
	}

	// rendered is map[path]string. Path format: "<chart>/templates/<file>.yaml".
	// Flatten with nodeID prefix for output determinism. Each rendered document
	// is post-processed to merge aggrega.ai/* provenance labels into
	// metadata.labels so sourced mode matches sourceless-mode's labeling.
	aggregaLabels := buildAggregaLabels(nodeCtx)
	aggregaAnnotations := buildAggregaAnnotations(nodeCtx)
	hookWeight := helmHookWeight(nodeCtx)
	out := make(map[string][]byte, len(rendered))
	keys := make([]string, 0, len(rendered))
	for k := range rendered {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, path := range keys {
		content := rendered[path]
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		base := filepath.Base(path)
		// Drop partial-template files (Helm convention: underscore prefix)
		if strings.HasPrefix(base, "_") {
			continue
		}
		// Drop non-manifest rendered output: Helm renders NOTES.txt (plain-text
		// usage notes) and may emit other non-YAML files alongside the manifests.
		// Only YAML/JSON documents are Kubernetes objects to label and emit.
		switch strings.ToLower(filepath.Ext(base)) {
		case ".yaml", ".yml", ".json":
		default:
			continue
		}
		labeled, err := injectAggregaLabels([]byte(content+"\n"), aggregaLabels, nodeCtx.YAMLIndent)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q: inject labels into %q", nodeCtx.NodeID, base))
		}
		annotated, err := injectAggregaAnnotations(labeled, aggregaAnnotations, hookWeight, nodeCtx.YAMLIndent)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q: inject annotations into %q", nodeCtx.NodeID, base))
		}
		// The file-key prefix uses the sanitised component id, not the raw
		// NodeID: a boundary-scope FR's synthetic NodeID carries marker syntax
		// (`<boundary-fr>:<inst>@<boundary>`) whose `<>:@` are illegal in
		// filenames on some platforms. ProvenanceComponentID is the raw NodeID
		// for ordinary components (byte-identical key) and the sanitised
		// LabelComponentID for FR-emitted nodes.
		outKey := renderer.ProvenanceComponentID(nodeCtx) + "-" + base
		out[outKey] = annotated
	}

	return out, nil
}
