// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
)

// ValuesValidator is a values-schema validation strategy for
// RenderLoadedChart. It receives the chart and the fully coalesced values the
// render consumes and returns an error when the values violate the chart's
// values.schema.json contract (including dependency charts' schemas).
// Validation is a gate, not a transform: a validator never alters the values,
// so the rendered bytes are identical across validation strategies — only
// acceptance can differ.
//
// A nil ValuesValidator selects the Helm SDK's own validation, which resolves
// any remote $ref in a chart schema with a live HTTP fetch. Callers that must
// render hermetically — the provider Wasm guest, whose sandbox grants no
// ambient network — pass an explicit validator (see
// NewHermeticValuesValidator) and the SDK's live-fetching validator never
// runs.
type ValuesValidator func(ch *chart.Chart, vals chartutil.Values) error

// RenderLoadedChart renders an already-loaded Helm chart against the Phase B
// values dictionary using the Helm Go SDK's template engine, returning flat
// rendered YAML files keyed by "<component-id>-<basename>.yaml". It is the
// filesystem-free render core: it receives the chart in memory and never reads
// from disk, so both the in-tree host engine (which loads the chart from a path)
// and the provider Wasm guest (which reconstructs the chart from the payload's
// source projection) share this one byte-identical-critical render path. Do not
// re-implement this logic in a caller — route through this function so the two
// render surfaces stay byte-for-byte identical.
//
// validate selects the values-schema validation strategy; the two compile
// targets differ only here. The host engine passes nil (the SDK's own
// validation); the guest passes the hermetic validator. Both strategies gate
// on the same coalesced values table, and neither alters it, so rendered
// output is byte-identical across strategies for any accepted input.
func RenderLoadedChart(
	_ context.Context,
	ch *chart.Chart,
	values map[string]any,
	nodeCtx renderer.NodeRenderContext,
	validate ValuesValidator,
) (map[string][]byte, error) {
	// Coalesce the Phase B dict with the chart's default values
	cvals, err := chartutil.CoalesceValues(ch, chartutil.Values(values))
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q: coalesce values", nodeCtx.NodeID))
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

	var renderVals chartutil.Values
	if validate == nil {
		// The SDK's own strategy: ToRenderValues validates against the chart's
		// values.schema.json internally, resolving any remote $ref live.
		renderVals, err = chartutil.ToRenderValues(ch, cvals, relOpts, caps)
	} else {
		// Explicit-validator strategy: skip the SDK's live-fetching validator
		// and gate on the supplied one instead. The table handed to the
		// validator is the same coalesced values table the SDK would have
		// validated and the render consumes, so the two strategies accept or
		// reject identical input.
		renderVals, err = chartutil.ToRenderValuesWithSchemaValidation(ch, cvals, relOpts, caps, true)
		if err == nil {
			if vals, ok := renderVals["Values"].(chartutil.Values); ok {
				err = validate(ch, vals)
			} else {
				err = fmt.Errorf("render values carry no coalesced values table to validate")
			}
		}
	}
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q: render values", nodeCtx.NodeID))
	}

	// Render
	rendered, err := engine.Engine{}.Render(ch, renderVals)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("helm: node %q: render", nodeCtx.NodeID))
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
