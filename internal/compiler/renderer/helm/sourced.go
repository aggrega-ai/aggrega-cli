// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/cognitio/aggrega-cli/pkg/errors"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
)

// renderSourced loads a Helm chart from disk and renders it against the
// Phase B values dictionary using the Helm Go SDK's template engine.
// Returns flat rendered YAML files keyed by "<nodeID>-<basename>.yaml".
func renderSourced(
	_ context.Context,
	chartPath string,
	values map[string]interface{},
	nodeCtx renderer.NodeRenderContext,
) (map[string][]byte, error) {
	// Load chart directory
	ch, err := loader.Load(chartPath)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: chartPath}, fmt.Sprintf("helm: node %q: load chart %q", nodeCtx.NodeID, chartPath))
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
	aggreraLabels := buildAggreraLabels(nodeCtx)
	aggreraAnnotations := buildAggreraAnnotations(nodeCtx)
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
		labeled, err := injectAggreraLabels([]byte(content+"\n"), aggreraLabels)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q: inject labels into %q", nodeCtx.NodeID, base))
		}
		annotated, err := injectAggreraAnnotations(labeled, aggreraAnnotations, hookWeight)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q: inject annotations into %q", nodeCtx.NodeID, base))
		}
		outKey := nodeCtx.NodeID + "-" + base
		out[outKey] = annotated
	}

	return out, nil
}
