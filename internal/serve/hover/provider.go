// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package hover

import "fmt"

// Provider renders the hover content for a Provider target. The
// rendered shape:
//
//   - **Provider** `<URI>` `v<version>`
//   - <Description>
//   - ---
//   - **Plugin**: `<plugin URI>` — when spec.plugin is set
//   - **Workload schema**: yes/no
//   - **Deprecated**: yes — when set
func Provider(e Entity) string {
	name := scalarValueAtPath(e.Doc, "metadata", "name")
	version := scalarValueAtPath(e.Doc, "metadata", "version")
	desc := scalarValueAtPath(e.Doc, "metadata", "description")
	plugin := scalarValueAtPath(e.Doc, "spec", "plugin")
	workloadSchema := scalarValueAtPath(e.Doc, "spec", "workloadSchema")
	deprecated := boolValueAtPath(e.Doc, "metadata", "deprecated")

	body := []string{}
	if plugin != "" {
		body = append(body, fmt.Sprintf("**Plugin**: `%s`", plugin))
	}
	body = append(body, fmt.Sprintf("**Workload schema**: %s", yesNo(workloadSchema != "")))
	if dep := Deprecated(deprecated); dep != "" {
		body = append(body, dep)
	}

	return Assemble(
		Header("Provider", name, version),
		Description(desc),
		Separator(),
		joinLines(body),
	)
}

// yesNo maps a bool to the literal "yes" / "no" for boolean-field
// rendering.
func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
