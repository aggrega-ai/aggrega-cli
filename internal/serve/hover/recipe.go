// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package hover

import "fmt"

// Recipe renders the hover content for a Recipe target. The rendered
// shape:
//
//   - **Recipe** `<URI>` `v<version>`
//   - <Description>
//   - ---
//   - **Implements**: `<component URI>`
//   - **Capability**: `<render-engine capability>`
//   - **Pipeline**: N steps
func Recipe(e Entity) string {
	name := scalarValueAtPath(e.Doc, "metadata", "name")
	version := scalarValueAtPath(e.Doc, "metadata", "version")
	desc := scalarValueAtPath(e.Doc, "metadata", "description")
	implements := scalarValueAtPath(e.Doc, "spec", "implements")
	capability := scalarValueAtPath(e.Doc, "spec", "capability")
	pipelineSteps := sequenceLenAtPath(e.Doc, "spec", "pipeline")

	body := []string{}
	if implements != "" {
		body = append(body, fmt.Sprintf("**Implements**: `%s`", implements))
	}
	if capability != "" {
		body = append(body, fmt.Sprintf("**Capability**: `%s`", capability))
	}
	if pipelineSteps > 0 {
		body = append(body, fmt.Sprintf("**Pipeline**: %d steps", pipelineSteps))
	}

	return Assemble(
		Header("Recipe", name, version),
		Description(desc),
		Separator(),
		joinLines(body),
	)
}

// joinLines concatenates non-empty section-internal lines with a
// single newline (markdown soft break). Used by renderers that emit a
// list of **Field**: value lines within one paragraph block.
func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	out := lines[0]
	for _, line := range lines[1:] {
		out += "\n" + line
	}
	return out
}
