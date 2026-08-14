// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"strings"
)

// yes and no are the grid's capability marks. The page's curated legend below
// the region reads these two marks by name, so they are part of the contract
// between the region and the prose around it.
const (
	yes = "✅"
	no  = "❌"
)

// renderRegion renders the capability grid and the note fixing its scope.
//
// The column set matches the one the page's curated introduction describes, so
// the introduction stays true without being generated: the three input kinds a
// provider declares, plus the alongside-artifacts case the introduction singles
// out. Rows follow the order the guests are declared in, so a row moves only
// when a declaration moves.
//
// Cells carry marks and, where a provider declares one, the delimiter pair an
// author types in its artifact files. No cell names an implementation detail a
// Recipe author cannot act on.
func renderRegion(providers []provider) (string, error) {
	var b strings.Builder

	b.WriteString("| Provider | `source:` + `valuesMapping:` | `workload:` | `artifacts:` (alongside) | artifacts-only (standalone) |\n")
	b.WriteString("| --- | --- | --- | --- | --- |\n")

	for _, p := range providers {
		if strings.Contains(p.Name, "|") {
			return "", fmt.Errorf("provider %q contains a table separator, which no escaping renders correctly inside a cell", p.Name)
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
			p.Name,
			mark(p.Sourced),
			mark(p.Synthesized),
			alongsideCell(p),
			mark(p.ArtifactsStandalone))
	}

	// The scope note is generated rather than curated because it describes what
	// the rows above cover, and a hand-written scope claim beside a generated
	// list is the drift this region exists to remove.
	b.WriteString("\n*This grid covers the providers distributed with the compiler. ")
	b.WriteString("A provider supplied separately declares the input kinds it accepts in the ")
	b.WriteString("same way, and the compiler reads that declaration by the same route — it is ")
	b.WriteString("not listed here, because a published page cannot enumerate providers it has ")
	b.WriteString("never seen.*")

	return b.String(), nil
}

// mark renders one capability claim.
func mark(supported bool) string {
	if supported {
		return yes
	}
	return no
}

// alongsideCell renders the alongside-artifacts claim, naming the delimiter pair
// where the provider declares one of its own. The pair is what an author types
// in an imported artifact file, so it belongs in the cell; a provider taking the
// default pair adds nothing, and the cell stays a bare mark.
func alongsideCell(p provider) string {
	if !p.AcceptsAlongsideArtifacts() {
		return no
	}
	if p.DelimLeft == "" || p.DelimRight == "" {
		return yes
	}
	return fmt.Sprintf("%s templated with `%s` `%s` delimiters", yes, p.DelimLeft, p.DelimRight)
}

// renderList prints the extracted claims as tab-separated records, for reading
// what the guests declare without touching the page.
func renderList(providers []provider) string {
	var b strings.Builder
	b.WriteString("provider\tsourced\tsynthesized\tartifacts_standalone\tartifacts_alongside\tdelimiters\n")
	for _, p := range providers {
		delims := "-"
		if p.DelimLeft != "" && p.DelimRight != "" {
			delims = p.DelimLeft + " " + p.DelimRight
		}
		fmt.Fprintf(&b, "%s\t%t\t%t\t%t\t%t\t%s\n",
			p.Name, p.Sourced, p.Synthesized, p.ArtifactsStandalone,
			p.AcceptsAlongsideArtifacts(), delims)
	}
	return b.String()
}
