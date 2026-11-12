// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package hover

import "fmt"

// ArtifactLibrary renders the hover content for an ArtifactLibrary target.
// Surfaces the bundle count for spec.artifacts.
func ArtifactLibrary(e Entity) string {
	name := scalarValueAtPath(e.Doc, "metadata", "name")
	version := scalarValueAtPath(e.Doc, "metadata", "version")
	desc := scalarValueAtPath(e.Doc, "metadata", "description")
	artifacts := mappingNodeAtPath(e.Doc, "spec", "artifacts")

	body := []string{}
	if artifacts != nil {
		body = append(body, fmt.Sprintf("**Artifacts**: %d bundles", mappingPairCount(artifacts)))
	}

	return Assemble(
		Header("ArtifactLibrary", name, version),
		Description(desc),
		Separator(),
		joinLines(body),
	)
}
