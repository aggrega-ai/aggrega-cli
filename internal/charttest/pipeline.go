// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package charttest renders an assembled chart tree the way `helm template`
// does, so a test can assert what a chart ACTUALLY renders rather than that its
// files are present on disk.
//
// The distinction is the package's whole reason to exist. A chart file sitting
// at a position the loader classifies as neither a template nor a sub-chart is
// inert data: it loads without error, contributes no manifest, and both `helm
// lint` and `helm template` report success over it. So an expectation that
// checks a path exists cannot separate a chart that renders from one that is
// merely present, and neither can the tool's exit code — the verdict is not a
// function of such a file's content at all. Rendering and then looking for the
// resource is what separates them.
package charttest

import (
	"sort"
	"strings"

	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/engine"
)

// RenderThroughDependencyPipeline loads the chart at chartDir and renders it
// through the SAME dependency pipeline `helm template` drives, returning every
// rendered manifest concatenated in template-name order.
//
// The four steps matter individually. ProcessDependenciesWithMerge applies the
// manifest's dependency aliases, and binds a dependency only when its name and
// version match the sub-chart on disk. CoalesceValues, reached inside
// ToRenderValues, scopes each alias-keyed value block into the sub-chart it
// names. Only then does the engine render. A bare engine.Render would skip the
// first two and prove nothing about either binding.
//
// The returned string is a concatenation rather than a map because every caller
// asks the same question of it — whether a given resource is in what the tool
// produced — and ordering it by template name keeps that answer deterministic.
func RenderThroughDependencyPipeline(chartDir string) (string, error) {
	ch, err := loader.Load(chartDir)
	if err != nil {
		return "", err
	}
	if err := chartutil.ProcessDependenciesWithMerge(ch, ch.Values); err != nil {
		return "", err
	}
	rv, err := chartutil.ToRenderValues(ch, ch.Values,
		chartutil.ReleaseOptions{Name: "demo", Namespace: "default"}, chartutil.DefaultCapabilities)
	if err != nil {
		return "", err
	}
	out, err := engine.Render(ch, rv)
	if err != nil {
		return "", err
	}
	keys := make([]string, 0, len(out))
	for k := range out {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(out[k])
		b.WriteString("\n")
	}
	return b.String(), nil
}
