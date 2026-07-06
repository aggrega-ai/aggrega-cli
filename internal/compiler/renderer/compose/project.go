// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build !wasip1

package compose

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// ProjectComposeSource reads the base compose file from the local source at
// compile time and packs it into a SourceProjection the host hands a provider
// Wasm guest, so the guest reconstructs the source in memory and touches no
// filesystem. It resolves the base compose file the same way the host sourced
// path does (resolveComposeSourcePath), reads it, and returns a one-file
// SourceProjection keyed by the file's base name. This is what the host does at
// compile time; the render core (RenderSourcedCore) then renders identically
// whether the source was read from a path (host) or from these bytes (guest).
func ProjectComposeSource(composePath string) (*renderer.SourceProjection, error) {
	resolvedPath, err := resolveComposeSourcePath(composePath)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: composePath}, fmt.Sprintf("compose: locate source in %q", composePath))
	}
	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: resolvedPath}, fmt.Sprintf("compose: project source from %q", resolvedPath))
	}

	return &renderer.SourceProjection{
		Files: []renderer.SourceFile{
			{Path: filepath.Base(resolvedPath), Content: data},
		},
	}, nil
}

// resolveComposeSourcePath returns the path of the YAML file that should be
// loaded for sourced-mode rendering. Two input shapes are accepted:
//
//   - chartPath is a regular file: returned as-is (legacy Tier-1 shape —
//     chartPath points directly at the compose YAML).
//   - chartPath is a directory: scans for `.yaml` and `.yml` entries and
//     returns the single matching file. Multiple matches are ambiguous and halt
//     with an error so authors of multi-file fragments see a clear signal rather
//     than silent first-wins. Zero matches also halt with an error.
//
// The directory shape is what the build-time orchestrator hands to the render
// path after extracting a compose-https-fragment cache slot — the extracted/
// subdirectory contains the single YAML the fragment carried.
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
