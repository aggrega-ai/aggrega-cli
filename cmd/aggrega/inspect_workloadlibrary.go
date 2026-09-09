// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// renderWorkloadLibraryFile renders a WorkloadLibrary authored as a standalone
// file. Each workload entry is shown with an Aggrega-synthesized usage line — the
// `workload: { $import: <Library>#/workloads/<Entry> }` form a Recipe writes to
// consume it. The reference uses the library's bare-name identity (the version
// comes from the per-Release lockfile; no registry host enters the reference).
//
//   - levelList: workload entry names only.
//   - levelDefault: header, description, per-entry usage line, labels / annotations.
//   - levelDetail: adds the full Go-template body per entry, verbatim.
func renderWorkloadLibraryFile(_ *workspaceconfig.ResolvedWorkspace, path string, data []byte, _ []string, opts inspectOptions) error {
	if opts.output == outputYAML {
		return emitYAMLRoundTrip(opts, path, data)
	}
	var lib types.WorkloadLibrary
	if err := yaml.Unmarshal(data, &lib); err != nil {
		return fmt.Errorf("parse WorkloadLibrary at %q: %w", path, err)
	}
	name := lib.Metadata.Name

	if opts.level == levelList {
		for entry := range lib.Spec.Workloads.All() {
			fmt.Fprintln(opts.out, entry)
		}
		return nil
	}

	renderIdentityBlock(opts, "WorkloadLibrary", identifierVersion(name, lib.Metadata.Version), lib.Metadata.Description)

	fmt.Fprintln(opts.out, "Workloads:")
	for entry, workload := range lib.Spec.Workloads.All() {
		fmt.Fprintf(opts.out, "  %s\n", entry)
		fmt.Fprintf(opts.out, "    Use: workload: { $import: %s#/workloads/%s }\n", name, entry)
		if opts.level == levelDetail {
			renderTemplateBodyIndented(opts, workload.Body)
		}
	}

	renderLibraryMetadata(opts, lib.Metadata.Labels, lib.Metadata.Annotations)
	return nil
}
