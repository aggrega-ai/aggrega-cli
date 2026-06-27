// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package hover

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

// SchemaLibrary renders the hover content for a SchemaLibrary target.
// Surfaces entry counts for the two top-level fields.
func SchemaLibrary(e Entity) string {
	name := scalarValueAtPath(e.Doc, "metadata", "name")
	version := scalarValueAtPath(e.Doc, "metadata", "version")
	desc := scalarValueAtPath(e.Doc, "metadata", "description")
	schemas := mappingNodeAtPath(e.Doc, "spec", "schemas")
	templates := mappingNodeAtPath(e.Doc, "spec", "templates")
	deprecated := boolValueAtPath(e.Doc, "metadata", "deprecated")

	body := []string{}
	if schemas != nil {
		body = append(body, fmt.Sprintf("**Schemas**: %d entries", mappingPairCount(schemas)))
	}
	if templates != nil {
		body = append(body, fmt.Sprintf("**Templates**: %d entries", mappingPairCount(templates)))
	}
	if dep := Deprecated(deprecated); dep != "" {
		body = append(body, dep)
	}

	return Assemble(
		Header("SchemaLibrary", name, version),
		Description(desc),
		Separator(),
		joinLines(body),
	)
}

// mappingPairCount returns the number of key/value pairs in a YAML
// mapping node (len(Content)/2 — yaml.v3 stores keys and values as
// alternating Content entries).
func mappingPairCount(n *yaml.Node) int {
	return len(n.Content) / 2
}
