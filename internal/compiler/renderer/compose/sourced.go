// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1

// Host-side sourced-mode rendering for the compose provider: it resolves and
// reads the base compose file from the local filesystem, then hands the bytes to
// the filesystem-free renderSourcedCore (sourced_core.go) so the host and the
// provider Wasm guest share one byte-identical render path. The base compose
// file path is interpreted relative to the working directory of the compile
// invocation; chart-relative path resolution is the catalog loader's job today
// and is unchanged.

package compose

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// renderSourced resolves and reads the base compose file at chartPath, then
// delegates to the filesystem-free renderSourcedCore, which deep-merges the
// values dict on top and emits an Intermediate=true compose-fragment for the
// Release-level merger to consume.
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

	return renderSourcedCore(raw, chartPath, values, nodeCtx)
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
