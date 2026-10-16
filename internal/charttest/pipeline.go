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

	"helm.sh/helm/v4/pkg/action"
	ci "helm.sh/helm/v4/pkg/chart"
	"helm.sh/helm/v4/pkg/chart/common"
	commonutil "helm.sh/helm/v4/pkg/chart/common/util"
	"helm.sh/helm/v4/pkg/chart/v2/loader"
	chartutil "helm.sh/helm/v4/pkg/chart/v2/util"
	"helm.sh/helm/v4/pkg/engine"
	releaseutil "helm.sh/helm/v4/pkg/release/v1/util"
)

// notesSuffix marks the rendered files that carry prose rather than a manifest.
// It is matched as a suffix, not compared to a base name, because a rendered
// file's key carries the path in front of the name.
const notesSuffix = "NOTES.txt"

// RenderThroughDependencyPipeline loads the chart at chartDir, refuses it on
// any of the conditions below, and otherwise returns every rendered file
// concatenated in template-name order.
//
// The value-and-alias pipeline is the one `helm template` drives, and so is
// each refusal below — every one of them is a call into the same library the
// command calls. They are a SUBSET of that command's refusals rather than all
// of them, so a green from this helper is a narrower claim than a
// `helm template` green, and the paragraph after the list says how much
// narrower.
//
// What it refuses:
//
//   - A dependency named in Chart.yaml with no sub-chart of that name loaded
//     from charts/, through action.CheckDependencies, which is what the command
//     calls after loading and before rendering. That is the CHECKING function,
//     and ProcessDependencies below is the PROCESSING one: the latter applies
//     aliases and conditions, and a chart whose declared sub-chart is absent
//     passes through it without complaint. Reading the processing call as the
//     dependency check is the mistake this paragraph exists to prevent.
//
//   - Values that violate the chart's own values.schema.json, through
//     ToRenderValues, which validates them rather than skipping validation.
//
//   - A rendered document that is not parseable YAML, through
//     releaseutil.SortManifests — the same sorter call the command parses its
//     own render with, and the reason a bogus render is caught before its
//     manifests reach a cluster. Every NOTES.txt is dropped before that parse,
//     as the command drops it: notes are prose, the sorter rejects prose, and
//     keeping them in the parse set would refuse charts the command accepts.
//
//     🔴 A PARSE error and nothing more. The sorter's only failure on a
//     document is the unmarshal into its own head type, so a well-formed
//     mapping carrying no kind at all is not refused — it is collected as a
//     generic manifest with a zero head. So this step does not establish that
//     what rendered is a Kubernetes resource, only that it is YAML, and a
//     caller wanting the stronger claim asserts the content itself.
//
// Refusals the command makes and this helper does not, so a chart carrying only
// these is accepted here and rejected by the command. The two below are
// measured; the list is a floor rather than an enumeration, because the
// command's full refusal set was never walked:
//
//   - A library-typed chart, which the command rejects as not installable. Its
//     check for that is an unexported function inside the command's own main
//     package, so no library call reaches it, and reproducing it would mean
//     keeping a hand-written second copy of it in step by eye.
//   - A chart whose kubeVersion range is incompatible with the capabilities it
//     is rendered against. The render values here carry the library's default
//     capability set and nothing compares the chart's declared range to it.
//
// A defect inside a chart nothing discovers is refused by neither this helper
// nor `helm template`, which is the case the package comment describes: the
// verdict is not a function of such a file's content at all.
//
// The value steps matter individually. ProcessDependencies applies the
// manifest's dependency aliases, and binds a dependency only when its name and
// version match the sub-chart on disk. CoalesceValues, reached inside
// ToRenderValues, scopes each alias-keyed value block into the sub-chart it
// names. Only then does the engine render. A bare engine.Render would skip both
// and prove nothing about either binding.
//
// The returned string is a concatenation rather than a map because every caller
// asks the same question of it — whether a given resource is in what the tool
// produced — and ordering it by template name keeps that answer deterministic.
// It carries the whole render map, notes included, rather than the command's
// standard output.
func RenderThroughDependencyPipeline(chartDir string) (string, error) {
	ch, err := loader.Load(chartDir)
	if err != nil {
		return "", err
	}
	// The dependencies Chart.yaml declares are read through the chart accessor, as
	// the command reads them before it checks them.
	accessor, err := ci.NewAccessor(ch)
	if err != nil {
		return "", err
	}
	if err := action.CheckDependencies(ch, accessor.MetaDependencies()); err != nil {
		return "", err
	}
	if err := chartutil.ProcessDependencies(ch, ch.Values); err != nil {
		return "", err
	}
	rv, err := commonutil.ToRenderValues(ch, ch.Values,
		common.ReleaseOptions{Name: "demo", Namespace: "default"}, common.DefaultCapabilities)
	if err != nil {
		return "", err
	}
	out, err := engine.Render(ch, rv)
	if err != nil {
		return "", err
	}
	if err := parseVerdict(out); err != nil {
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

// parseVerdict reports whether the rendered files PARSE, by asking the manifest
// sorter the command parses its own render with. Only the verdict is taken: the
// sorted result is discarded, so what this package returns to a caller stays the
// whole render map in template-name order.
//
// The name is the strongest honest one: the sorter's only failure on a document
// is the unmarshal into its own head type, so this says the render is YAML and
// does not say any of it is a resource.
//
// rendered is left unmodified — the caller assembles its output from it — so
// the notes are excluded through a copy rather than by deleting from it.
//
// The sorter's version-set parameter is passed empty, which is what the
// command's own call passes; the library ignores that parameter.
func parseVerdict(rendered map[string]string) error {
	manifests := make(map[string]string, len(rendered))
	for name, content := range rendered {
		if strings.HasSuffix(name, notesSuffix) {
			continue
		}
		manifests[name] = content
	}
	_, _, err := releaseutil.SortManifests(manifests, nil, releaseutil.InstallOrder)
	return err
}
