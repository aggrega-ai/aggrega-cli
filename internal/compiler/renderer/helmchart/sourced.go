// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmchart

import (
	"fmt"
	"os"
	"path/filepath"

	cerrors "github.com/cognitio/aggrega-cli/pkg/errors"
	"gopkg.in/yaml.v3"
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

// readChartName parses a local chart's Chart.yaml and returns its `name` field.
// Used by the umbrella builder so the dependency entry carries the chart's real
// name while the alias identifies the instance.
func readChartName(chartPath string) (string, error) {
	data, err := os.ReadFile(filepath.Join(chartPath, "Chart.yaml"))
	if err != nil {
		return "", cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: chartPath}, fmt.Sprintf("helmchart sourced: read Chart.yaml in %q", chartPath))
	}
	var header struct {
		Name string `yaml:"name"`
	}
	if err := yaml.Unmarshal(data, &header); err != nil {
		return "", cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: chartPath}, fmt.Sprintf("helmchart sourced: parse Chart.yaml in %q", chartPath))
	}
	if header.Name == "" {
		return "", cerrors.New(cerrors.CatalogError, cerrors.SourceLocation{File: chartPath}, fmt.Sprintf("helmchart sourced: Chart.yaml in %q has empty name", chartPath), nil, nil)
	}
	return header.Name, nil
}
