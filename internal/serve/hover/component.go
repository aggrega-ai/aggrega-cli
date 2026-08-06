// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package hover

// Component renders the hover content for a Component target. The
// rendered shape:
//
//   - **Component** `<URI>` `v<version>`
//   - <Description>
//   - ---
//   - **Schema** (excerpt) — first 10 lines of spec.schema, when set
//   - **Extends**: `<parent URI>` — when spec.extends is set
func Component(e Entity) string {
	name := scalarValueAtPath(e.Doc, "metadata", "name")
	version := scalarValueAtPath(e.Doc, "metadata", "version")
	desc := scalarValueAtPath(e.Doc, "metadata", "description")
	return Assemble(
		Header("Component", name, version),
		Description(desc),
		Separator(),
		SchemaExcerpt(e.Doc, 10),
		Extends(e.Doc),
	)
}
