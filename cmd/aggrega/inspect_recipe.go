// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"go.yaml.in/yaml/v3"
)

// renderRecipeFile renders a Recipe authored as a standalone file. The layout
// is mode-prominent: a Synthesized or Sourced section names the input kind, with
// mode-specific body. spec.implements renders as authored (isolation default);
// --show-implements resolves the bound Component against the catalog via the
// shared lookup and surfaces the presence mandates it declares.
//
//   - levelList: identifier, implements, provider.
//   - levelDefault: implements, provider, mode section, valuesMapping summary.
//   - levelDetail: adds workload body, provides / locals / assertions / pipeline.
func renderRecipeFile(rw *workspaceconfig.ResolvedWorkspace, path string, data []byte, catalogDirs []string, opts inspectOptions) error {
	if opts.output == outputYAML {
		return emitYAMLRoundTrip(opts, path, data)
	}
	var rec types.Recipe
	if err := yaml.Unmarshal(data, &rec); err != nil {
		return fmt.Errorf("parse Recipe at %q: %w", path, err)
	}
	identifier := identifierVersion(rec.Metadata.Name, rec.Metadata.Version)

	if opts.level == levelList {
		fmt.Fprintln(opts.out, opts.colorize.uri(identifier))
		fmt.Fprintf(opts.out, "implements: %s\n", rec.Spec.Implements)
		fmt.Fprintf(opts.out, "capability: %s\n", rec.Spec.Capability)
		return nil
	}

	renderHeader(opts, "Recipe", identifier)
	fmt.Fprintf(opts.out, "Implements: %s\n", opts.colorize.uri(rec.Spec.Implements))
	fmt.Fprintf(opts.out, "Capability: %s\n", rec.Spec.Capability)

	renderRecipeMode(opts, rec.Spec.Workload, rec.Spec.Source)
	fmt.Fprintf(opts.out, "valuesMapping: %s\n", valuesMappingSummary(rec.Spec.ValuesMapping))

	if rec.Spec.Artifacts.Len() > 0 {
		fmt.Fprintf(opts.out, "Artifacts: %d\n", rec.Spec.Artifacts.Len())
		for name := range rec.Spec.Artifacts.All() {
			fmt.Fprintf(opts.out, "  - %s\n", name)
		}
	}

	if opts.level == levelDetail {
		if !rec.Spec.Workload.IsEmpty() && rec.Spec.Workload.Inline != "" {
			fmt.Fprintln(opts.out, "Workload:")
			renderTemplateBodyIndented(opts, rec.Spec.Workload.Inline)
		}
		renderNamedTemplateKeys(opts, "Provides:", rec.Spec.Provides)
		renderNamedTemplateKeys(opts, "Locals:", rec.Spec.Locals)
		renderAssertionKeys(opts, rec.Spec.Assertions)
		renderPipeline(opts, rec.Spec.Pipeline)
	}

	if opts.showImplements {
		comp, err := resolveImplementsComponent(rw, catalogDirs, rec.Spec.Implements)
		if err != nil {
			return err
		}
		fmt.Fprintf(opts.out, "Implements Component: %s\n",
			opts.colorize.uri(identifierVersion(comp.Metadata.Name, comp.Metadata.Version)))
		for _, m := range types.RequiredAttachmentTypes(comp.Metadata.Annotations) {
			fmt.Fprintf(opts.out, "  requires attachment (%s): %s\n", m.Label, m.Type)
		}
	}
	return nil
}

// renderRecipeMode prints the Synthesized / Sourced mode section. Synthesized
// (a workload: block) shows a one-line workload summary; Sourced (a source:
// reference) shows the source as authored. A Recipe carrying neither relies on
// artifacts and is reported as artifacts-only.
func renderRecipeMode(opts inspectOptions, workload types.WorkloadTemplate, source any) {
	switch {
	case !workload.IsEmpty():
		fmt.Fprintln(opts.out, "## Synthesized")
		fmt.Fprintf(opts.out, "  workload: %s\n", importableSummary(workload.Inline, workload.Import))
	case source != nil:
		fmt.Fprintln(opts.out, "## Sourced")
		fmt.Fprintf(opts.out, "  source: %s\n", sourceSummary(source))
	default:
		fmt.Fprintln(opts.out, "## Artifacts-only")
	}
}

// sourceSummary describes a Recipe's source field, which is either a Universal
// URI string or an open structured object whose keys are exposed as .source.*.
func sourceSummary(source any) string {
	if s, ok := source.(string); ok {
		return s
	}
	return "(structured source)"
}

// renderNamedTemplateKeys prints the keys of a string-valued OrderedMap (the
// provides / locals contractual surfaces) under a label, in author order.
func renderNamedTemplateKeys(opts inspectOptions, label string, m types.OrderedMap[string]) {
	if m.Len() == 0 {
		return
	}
	fmt.Fprintln(opts.out, label)
	for k := range m.All() {
		fmt.Fprintf(opts.out, "  - %s\n", k)
	}
}

// renderAssertionKeys prints the named assertion identifiers in author order.
func renderAssertionKeys(opts inspectOptions, m types.OrderedMap[string]) {
	if m.Len() == 0 {
		return
	}
	fmt.Fprintln(opts.out, "Assertions:")
	for k := range m.All() {
		fmt.Fprintf(opts.out, "  - %s\n", k)
	}
}

// renderPipeline prints the Wasm pipeline steps in declared order.
func renderPipeline(opts inspectOptions, steps []types.PipelineStep) {
	if len(steps) == 0 {
		return
	}
	fmt.Fprintln(opts.out, "Pipeline:")
	for _, step := range steps {
		fmt.Fprintf(opts.out, "  - %s: %s/%s\n", step.ID, step.Plugin, step.Function)
	}
}
