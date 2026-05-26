// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package hover

import "fmt"

// MappingLibrary renders the hover content for a MappingLibrary target.
// Surfaces the entry count for spec.mappings.
func MappingLibrary(e Entity) string {
	name := scalarValueAtPath(e.Doc, "metadata", "name")
	version := scalarValueAtPath(e.Doc, "metadata", "version")
	desc := scalarValueAtPath(e.Doc, "metadata", "description")
	mappings := mappingNodeAtPath(e.Doc, "spec", "mappings")
	deprecated := boolValueAtPath(e.Doc, "metadata", "deprecated")

	body := []string{}
	if mappings != nil {
		body = append(body, fmt.Sprintf("**Mappings**: %d entries", mappingPairCount(mappings)))
	}
	if dep := Deprecated(deprecated); dep != "" {
		body = append(body, dep)
	}

	return Assemble(
		Header("MappingLibrary", name, version),
		Description(desc),
		Separator(),
		joinLines(body),
	)
}
