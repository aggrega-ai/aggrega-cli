// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchart

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"go.yaml.in/yaml/v3"
)

// copySourcedChart walks a local Helm chart directory and returns its files
// keyed by their path relative to the chart root (e.g. "Chart.yaml",
// "values.yaml", "templates/deployment.yaml"). The orchestrator prefixes
// these with `boundaries/<boundary>/charts/<instance_id>/` when placing them
// under the umbrella.
//
// This is used by the helm-chart provider in sourced mode: the user's chart
// becomes the sub-chart verbatim, and the umbrella builder aliases it to the
// component's instance_id so values propagation still works when the same
// source chart is reused by multiple components.
func copySourcedChart(chartPath string) (map[string][]byte, error) {
	info, err := os.Stat(chartPath)
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: chartPath}, fmt.Sprintf("helmchart sourced: stat %q", chartPath))
	}
	if !info.IsDir() {
		return nil, cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: chartPath}, fmt.Sprintf("helmchart sourced: source %q is not a directory", chartPath), nil, nil)
	}

	out := map[string][]byte{}
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
		out[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		return nil, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: chartPath}, fmt.Sprintf("helmchart sourced: walk %q", chartPath))
	}
	if _, ok := out["Chart.yaml"]; !ok {
		return nil, cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: chartPath}, fmt.Sprintf("helmchart sourced: %q has no Chart.yaml", chartPath), nil, nil)
	}
	return out, nil
}

// readChartMeta parses a local chart's Chart.yaml and returns its `name` and
// `version` fields. Used by the umbrella builder so the dependency entry mirrors
// the vendored chart in BOTH name and version — the alias identifies the
// instance, but the version must match the physically-copied chart or Helm will
// not bind the dependency (and the umbrella's per-instance values never reach the
// sub-chart). An empty name or version halts loudly, so a future drift cannot
// silently drop the alias.
func readChartMeta(chartPath string) (name, version string, err error) {
	data, err := os.ReadFile(filepath.Join(chartPath, "Chart.yaml"))
	if err != nil {
		return "", "", cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: chartPath}, fmt.Sprintf("helmchart sourced: read Chart.yaml in %q", chartPath))
	}
	var header struct {
		Name    string `yaml:"name"`
		Version string `yaml:"version"`
	}
	// A chart's Chart.yaml is a single YAML document. Decode the first document,
	// then require the stream to be exhausted: a single yaml.Unmarshal would
	// silently keep only the first document of a multi-document file, so a
	// malformed multi-document Chart.yaml must halt loudly (Loud Guardrails),
	// mirroring the compose sourced single-document enforcement. An empty file
	// (io.EOF on the first Decode) leaves header zero-valued and falls through
	// to the empty-name halt below, unchanged.
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&header); err != nil && !errors.Is(err, io.EOF) {
		return "", "", cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: chartPath}, fmt.Sprintf("helmchart sourced: parse Chart.yaml in %q", chartPath))
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return "", "", cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: chartPath}, fmt.Sprintf("helmchart sourced: Chart.yaml in %q contains multiple YAML documents; a chart's Chart.yaml must be a single document", chartPath), nil, nil)
	}
	if header.Name == "" {
		return "", "", cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: chartPath}, fmt.Sprintf("helmchart sourced: Chart.yaml in %q has empty name", chartPath), nil, nil)
	}
	if header.Version == "" {
		return "", "", cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: chartPath}, fmt.Sprintf("helmchart sourced: Chart.yaml in %q has empty version", chartPath), nil, nil)
	}
	return header.Name, header.Version, nil
}
