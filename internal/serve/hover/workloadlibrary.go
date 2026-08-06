// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package hover

import "fmt"

// WorkloadLibrary renders the hover content for a WorkloadLibrary target.
// Surfaces the entry count for spec.workloads.
func WorkloadLibrary(e Entity) string {
	name := scalarValueAtPath(e.Doc, "metadata", "name")
	version := scalarValueAtPath(e.Doc, "metadata", "version")
	desc := scalarValueAtPath(e.Doc, "metadata", "description")
	workloads := mappingNodeAtPath(e.Doc, "spec", "workloads")

	body := []string{}
	if workloads != nil {
		body = append(body, fmt.Sprintf("**Workloads**: %d entries", mappingPairCount(workloads)))
	}

	return Assemble(
		Header("WorkloadLibrary", name, version),
		Description(desc),
		Separator(),
		joinLines(body),
	)
}
