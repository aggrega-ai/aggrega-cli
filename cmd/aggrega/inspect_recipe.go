// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// renderRecipeFile renders a Recipe authored as a standalone file. The layout
// is mode-prominent: one Sourced / Synthesized section per input mode the Recipe
// declares, each with its own body, followed by the valuesMapping summary and
// the artifacts listing. spec.implements renders as authored (isolation
// default); --show-implements resolves the bound Component against the catalog
// via the shared lookup and surfaces the presence mandates it declares.
//
//   - levelList: identifier, implements, provider.
//   - levelDefault: implements, provider, mode sections, valuesMapping summary,
//     artifacts listing.
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

// renderRecipeMode prints one section per input mode the Recipe declares, not
// one section for the whole Recipe. A Recipe's input modes compose by addition —
// a source: to render, a workload: to synthesize and an artifacts: map all
// contribute output, and any combination of them is legal — so the sections are
// a set: Sourced shows the source as authored, Synthesized shows a one-line
// workload summary, and both appear when both are declared. A Recipe declaring
// neither a source: nor a workload: relies on its artifacts: entries and is
// reported as artifacts-only.
//
// The sections are ordered source → workload → artifacts, the same order the
// build applies when it resolves a Recipe to a single mode name and when it
// reports which declared mode a provider cannot honour. Ordering them alike is
// what lets a reader compare this output against a build without re-deriving
// which mode takes precedence.
//
// That order is stated here as well as in the renderer, and the duplication is
// deliberate. The renderer's enumerator answers a different question — which
// modes need a provider capability, which deliberately excludes an artifacts:
// map declared alongside a source: or a workload:, because those files ride
// through as composed output. Reporting that answer here would drop the
// artifacts section from exactly the mixed-mode Recipe this section exists to
// describe, so the two are kept separate on purpose rather than for want of a
// shared home. The artifacts entries themselves are listed by the caller.
func renderRecipeMode(opts inspectOptions, workload types.WorkloadTemplate, source any) {
	declared := false
	if source != nil {
		fmt.Fprintln(opts.out, "## Sourced")
		fmt.Fprintf(opts.out, "  source: %s\n", sourceSummary(source))
		declared = true
	}
	if !workload.IsEmpty() {
		fmt.Fprintln(opts.out, "## Synthesized")
		fmt.Fprintf(opts.out, "  workload: %s\n", importableSummary(workload.Inline, workload.Import))
		declared = true
	}
	if !declared {
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
