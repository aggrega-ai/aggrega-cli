// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package hover

// Feature renders the hover content for a Feature target. Identical
// shape to Component (Feature carries the same spec.schema +
// spec.extends fields).
func Feature(e Entity) string {
	name := scalarValueAtPath(e.Doc, "metadata", "name")
	version := scalarValueAtPath(e.Doc, "metadata", "version")
	desc := scalarValueAtPath(e.Doc, "metadata", "description")
	return Assemble(
		Header("Feature", name, version),
		Description(desc),
		Separator(),
		SchemaExcerpt(e.Doc, 10),
		Extends(e.Doc),
	)
}
