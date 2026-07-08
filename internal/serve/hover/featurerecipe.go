// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package hover

import "fmt"

// FeatureRecipe renders the hover content for a FeatureRecipe target.
// Same shape as Recipe — the Implements target is a Feature URI here.
func FeatureRecipe(e Entity) string {
	name := scalarValueAtPath(e.Doc, "metadata", "name")
	version := scalarValueAtPath(e.Doc, "metadata", "version")
	desc := scalarValueAtPath(e.Doc, "metadata", "description")
	implements := scalarValueAtPath(e.Doc, "spec", "implements")
	target := scalarValueAtPath(e.Doc, "spec", "target")
	pipelineSteps := sequenceLenAtPath(e.Doc, "spec", "pipeline")
	deprecated := boolValueAtPath(e.Doc, "metadata", "deprecated")

	body := []string{}
	if implements != "" {
		body = append(body, fmt.Sprintf("**Implements**: `%s`", implements))
	}
	if target != "" {
		body = append(body, fmt.Sprintf("**Target**: `%s`", target))
	}
	if pipelineSteps > 0 {
		body = append(body, fmt.Sprintf("**Pipeline**: %d steps", pipelineSteps))
	}
	if dep := Deprecated(deprecated); dep != "" {
		body = append(body, dep)
	}

	return Assemble(
		Header("FeatureRecipe", name, version),
		Description(desc),
		Separator(),
		joinLines(body),
	)
}
