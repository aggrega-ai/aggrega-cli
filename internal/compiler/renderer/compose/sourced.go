// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Sourced-mode rendering for the compose provider. A Recipe with
// `source: <path>` plus `valuesMapping:` produces:
//
//  1. The base compose file at <path> is loaded as YAML.
//  2. The valuesMapping output (the values dict already evaluated by Phase
//     B.1) is treated as a compose-overlay document.
//  3. The two are deep-merged per docker-compose's native overlay semantics:
//     scalars: overlay wins, maps merge, lists are REPLACED (not appended) —
//     this matches `docker compose -f base.yaml -f overlay.yaml config`
//     behaviour.
//
// The merged compose document is emitted as one Intermediate=true
// EmittedFile (`<nodeID>.compose-fragment.yaml`) so it joins the Release-
// level merge alongside synthesised-mode fragments. The base compose file
// path is interpreted relative to the working directory of the compile
// invocation; chart-relative path resolution is the catalog loader's job
// today and is unchanged.

package compose

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// renderSourced loads the base compose file at chartPath, deep-merges the
// values dict on top, and emits an Intermediate=true compose-fragment for
// the Release-level merger to consume.
//
// chartPath may be either a YAML file path (the legacy Tier-1 shape) or
// a directory path (the post-orchestrator-extraction shape, where the
// source-artifact cache holds the YAML fragment inside the extracted/
// subdirectory). When chartPath names a directory, renderSourced reads
// the single .yaml or .yml entry inside; multiple YAML entries halt
// with a CatalogError so authors of multi-file fragments hit a clear
// signal rather than silently picking one.
func renderSourced(chartPath string, values map[string]any, nodeCtx renderer.NodeRenderContext) (map[string][]byte, error) {
	resolvedPath, err := resolveComposeSourcePath(chartPath)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: chartPath}, fmt.Sprintf("compose: node %q: read source %q", nodeCtx.NodeID, chartPath))
	}
	raw, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: resolvedPath}, fmt.Sprintf("compose: node %q: read source %q", nodeCtx.NodeID, resolvedPath))
	}

	var base map[string]any
	if err := yaml.Unmarshal(raw, &base); err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: chartPath}, fmt.Sprintf("compose: node %q: parse source %q", nodeCtx.NodeID, chartPath))
	}
	if base == nil {
		base = map[string]any{}
	}

	merged := deepMergeOverlay(base, values)
	origins := buildSingleOriginMap(merged, nodeCtx)
	injectAggregaLabelsIntoServices(merged, origins)

	body, err := yaml.Marshal(merged)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("compose: node %q: marshal merged source", nodeCtx.NodeID))
	}

	// Output path mirrors synthesised-mode: <nodeID>.compose-fragment.yaml.
	// Both flow into the same Release-level merger via Intermediate=true.
	// We return as a renderer.Engine entry point (map[string][]byte), but
	// the orchestrator's per-node Phase B sub-passes wrap the file into an
	// EmittedFile and tag origin/intermediate. Sourced-mode origin is
	// EmitOriginSource per the existing convention.
	//
	// However: the orchestrator's current Render() dispatch tags every
	// output as EmitOriginSource AND Intermediate=false. To make sourced-
	// mode compose flow through the Release-level merger, the file MUST
	// carry Intermediate=true. The orchestrator does not currently
	// inspect Render's output names for this signal. The clean path: the
	// orchestrator's per-engine dispatch checks "if engine is a
	// ReleaseComposer, mark its Render output as Intermediate=true."
	//
	// That orchestrator-side change is implemented at the Render dispatch
	// site. For now this file emits the body under the standard fragment
	// name; the orchestrator picks up the file and tags it.
	return map[string][]byte{nodeCtx.NodeID + ".compose-fragment.yaml": body}, nil
}

// resolveComposeSourcePath returns the path of the YAML file that
// should be loaded for sourced-mode rendering. Two input shapes
// are accepted:
//
//   - chartPath is a regular file: returned as-is (legacy Tier-1
//     shape — chartPath points directly at the compose YAML).
//   - chartPath is a directory: scans for `.yaml` and `.yml` entries
//     and returns the single matching file. Multiple matches are
//     ambiguous and halt with an error so authors of multi-file
//     fragments see a clear signal rather than silent first-wins.
//     Zero matches also halt with an error.
//
// The directory shape is what the build-time orchestrator hands to
// the engine after extracting a compose-https-fragment cache slot
// — the extracted/ subdirectory contains the single YAML the
// fragment carried.
func resolveComposeSourcePath(chartPath string) (string, error) {
	info, err := os.Stat(chartPath)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return chartPath, nil
	}
	entries, err := os.ReadDir(chartPath)
	if err != nil {
		return "", err
	}
	var yamlEntries []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml") {
			yamlEntries = append(yamlEntries, filepath.Join(chartPath, name))
		}
	}
	switch len(yamlEntries) {
	case 0:
		return "", fmt.Errorf("directory contains no .yaml/.yml entries")
	case 1:
		return yamlEntries[0], nil
	default:
		return "", fmt.Errorf("directory contains %d .yaml/.yml entries; expected exactly one", len(yamlEntries))
	}
}

// deepMergeOverlay merges overlay onto base using docker-compose overlay
// semantics:
//
//   - When both base[k] and overlay[k] are maps, recurse.
//   - When overlay[k] is a non-map value (scalar, list, nil), it replaces
//     base[k] entirely.
//   - When base lacks k, overlay[k] is added.
//   - When overlay lacks k, base[k] is preserved.
//
// Returns the merged map. Inputs may be nil; nil base treated as empty.
func deepMergeOverlay(base, overlay map[string]any) map[string]any {
	if base == nil {
		base = map[string]any{}
	}
	out := make(map[string]any, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, ov := range overlay {
		bv, hasBase := out[k]
		if !hasBase {
			out[k] = ov
			continue
		}
		bm, baseIsMap := asStringMap(bv)
		om, overlayIsMap := asStringMap(ov)
		if baseIsMap && overlayIsMap {
			out[k] = deepMergeOverlay(bm, om)
			continue
		}
		// All other cases: overlay replaces base (lists, scalars, mixed).
		out[k] = ov
	}
	return out
}

// asStringMap normalises a YAML-decoded interface to map[string]any.
// yaml.v3 decodes maps as either map[string]any (when keys are
// always strings) or map[any]any (when keys are mixed). The
// merger needs string-keyed maps; this helper coerces the latter.
func asStringMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, val := range m {
			ks, ok := k.(string)
			if !ok {
				return nil, false
			}
			out[ks] = val
		}
		return out, true
	default:
		return nil, false
	}
}
