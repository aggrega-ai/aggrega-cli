// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package hover

import "fmt"

// FeatureRecipe renders the hover content for a FeatureRecipe target.
// Same shape as Recipe — the Implements target is a Feature URI here.
func FeatureRecipe(e Entity) string {
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
		Header("FeatureRecipe", name, version),
		Description(desc),
		Separator(),
		joinLines(body),
	)
}
