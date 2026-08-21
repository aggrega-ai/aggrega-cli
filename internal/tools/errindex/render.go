// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// cell pairs a declared string with the name a refusal calls it by, so the
// separator check below can report which of a row's columns broke it.
type cell struct{ what, text string }

// rejectTableSeparator refuses a declared string that would break the table row
// it is about to be rendered into.
//
// It is one function rather than one check per column, and one function rather
// than one per region: a separator breaks a row identically whichever cell
// carries it and whichever table the cell is in, so a second copy of the refusal
// would only be a second place for the two to disagree.
func rejectTableSeparator(id Identity, cells []cell) error {
	for _, c := range cells {
		if strings.Contains(c.text, "|") {
			return fmt.Errorf("%s: the registered %s contains a table separator, "+
				"which no escaping renders correctly inside a cell; reword the %s",
				id.Handle(), c.what, c.what)
		}
	}
	return nil
}

// renderRegion returns the body of the generated region: one section per
// package a reader can import, each listing the names that package exports and,
// for each, the code it is registered under and the message it renders.
//
// The code is a column of its own because it is the identifier a consumer
// outside Go holds. It travels on the structured diagnostic rather than through
// an import, so a consumer reading one has no way back to the condition unless
// the correspondence is published. Most rows do read as a restatement of the
// section and the name beside them, and that is exactly what makes publishing
// the rest necessary: a reader who inferred the code from the row would be right
// often enough to stop checking, and wrong on a re-export, whose code names the
// condition it forwards to, and on a rule whose code names the rule rather than
// the accessor. A re-export carries the forwarded code, so every row in every
// section reports one.
//
// Only importable packages appear. An entry naming a package Go makes
// unreachable from outside the module advertises a match no reader can write,
// and a reference page's left-hand column is a promise that the name can be
// dereferenced.
//
// The column carries the call rather than the bare name, because the bare name
// is what a reader cannot use: an identity is reached through the accessor that
// publishes it, so the name on its own is a function value and does not compile
// where an error is expected. Printing the call is what keeps the promise above
// literally true for every row.
//
// Grouping is by the package that DECLARES the name — the package a reader
// imports to match it — and not by where the condition is raised. A name is
// frequently declared in one package and raised from several others, so a
// by-origin grouping is the only one that is true for every entry and the only
// one a reader can act on.
func renderRegion(ids []Identity) (string, error) {
	byPkg := map[string][]Identity{}
	importablePkg := map[string]bool{}
	for _, id := range ids {
		if !id.Importable {
			continue
		}
		importablePkg[id.Pkg] = true
		byPkg[id.Pkg] = append(byPkg[id.Pkg], id)
	}
	pkgs := make([]string, 0, len(byPkg))
	for p := range byPkg {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)

	var b strings.Builder
	for i, p := range pkgs {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "### %s\n\n", p)
		b.WriteString("| Sentinel | Code | Message |\n| --- | --- | --- |\n")

		rows := byPkg[p]
		sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
		var reExports []Identity
		for _, id := range rows {
			if err := rejectTableSeparator(id, []cell{
				{"code", id.Code},
				{"message", id.Message},
			}); err != nil {
				return "", err
			}
			fmt.Fprintf(&b, "| %s | %s | %s |\n",
				codeSpan(id.Name+"()"), codeSpan(id.Code), codeSpan(id.Message))
			// A re-export is named alongside the handle it forwards to, so a
			// reader holding one name knows the other matches too — but only
			// when that other name is one they can reach. Where the origin sits
			// in a package the module does not export, the row stands alone:
			// the name here is matchable and complete on its own, and printing
			// an unreachable handle beside it would advertise a package no
			// reader can import.
			if id.AliasOf != "" && importablePkg[handlePkg(id.AliasOf)] {
				reExports = append(reExports, id)
			}
		}
		if len(reExports) > 0 {
			b.WriteString("\n")
			for _, id := range reExports {
				fmt.Fprintf(&b, "- %s is the same value as %s, so a match against either name succeeds.\n",
					codeSpan(id.Name+"()"), codeSpan(id.AliasOf+"()"))
			}
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// renderShortCodeRegion returns the body of the short-code region: one row per
// declared condition, ordered by short code, carrying that code, the descriptive
// code naming the same condition, and the message it renders.
//
// # Why this table's corpus is wider than the by-package one
//
// [renderRegion] is keyed on a name a reader can import, so it can only carry a
// condition that HAS such a name — a condition declared where the module makes it
// unreachable, and re-exported nowhere reachable, has nothing to put in its
// left-hand column. This table is keyed on the short code instead, and every
// condition declares one because registration refuses a condition without one.
// So the two corpora differ by construction rather than by policy, and this one
// is the whole declared set.
//
// That difference is the reason the wider table has to exist. A short code says
// nothing about its condition on its own, so a reader holding one can do nothing
// with it but look it up; a lookup surface covering only the conditions that also
// happen to have an importable accessor would be silent for a large minority of
// the codes it is supposed to resolve, which defeats the purpose of publishing it.
//
// # No category column, and it is not an omission
//
// A condition has no category to put in one. Which category a diagnostic reports
// is derived per occurrence from the entity kind the failure is about, so one
// condition reached from two kinds reports two categories and any single value
// here would be false for it. The page says so in the reader's terms and points
// at where the category vocabulary is taught.
//
// # Ordering
//
// Rows are ordered by short code, as text. A short code is zero-padded to a
// fixed width precisely so that a text sort is also a numeric one, so this needs
// no numeric parse and cannot disagree with one.
//
// Re-exports are excluded. A re-export is another name for a condition already
// listed, not a second condition, and it reports the short code of the condition
// it forwards to — so including one would put two rows under a single code and
// make the key ambiguous in the one table whose whole job is to resolve it.
func renderShortCodeRegion(ids []Identity) (string, error) {
	rows := make([]Identity, 0, len(ids))
	for _, id := range ids {
		if id.AliasOf != "" {
			continue
		}
		rows = append(rows, id)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ShortCode < rows[j].ShortCode })

	// A duplicate short code would render two rows under one key and leave a
	// reader unable to tell which condition they are holding. Registration
	// already refuses one at package initialisation, so reaching here means the
	// extraction — not the declaration — has gone wrong, and reporting it is
	// cheaper than publishing an ambiguous table.
	seen := make(map[string]string, len(rows))
	var b strings.Builder
	b.WriteString("| Code | Condition | Message |\n| --- | --- | --- |\n")
	for _, id := range rows {
		if prev, dup := seen[id.ShortCode]; dup {
			return "", fmt.Errorf("%s and %s both report the short code %s, so a lookup keyed on it "+
				"cannot name one condition", prev, id.Handle(), id.ShortCode)
		}
		seen[id.ShortCode] = id.Handle()
		if err := rejectTableSeparator(id, []cell{
			{"short code", id.ShortCode},
			{"code", id.Code},
			{"message", id.Message},
		}); err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n",
			codeSpan(id.ShortCode), codeSpan(id.Code), codeSpan(id.Message))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// renderIndex returns the machine-readable identity index, one record of five
// tab-separated fields per line: a kind, the declaring package, the name, either
// the declared descriptive code or the qualified handle a re-export forwards to,
// and the short code.
//
// It exists so that a consumer outside this module reads the identity set from
// the one place that extracts it, rather than growing a second recogniser of the
// same declaration syntax. Two recognisers of one population agree until the day
// a declaration takes a shape only one of them reads, and the agreement up to
// that day is exactly what makes the divergence invisible.
//
// The short code is a field of its own rather than folded into the fourth,
// because the two identifiers are independent — neither is derivable from the
// other — so a consumer wanting one must not have to split the other. It is
// LAST so that the four fields a consumer already reads keep their positions;
// the width still changed, and every consumer pins the width deliberately, so
// each one moved with this. A re-export reports the short code it forwards to,
// for the same reason it reports the forwarded descriptive code: the value it
// returns IS that condition's value.
func renderIndex(ids []Identity) string {
	var b strings.Builder
	for _, id := range ids {
		kind, detail := "origin", id.Code
		if id.AliasOf != "" {
			kind, detail = "alias", id.AliasOf
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\t%s\n", kind, id.Pkg, id.Name, detail, id.ShortCode)
	}
	return b.String()
}

// handlePkg returns the package half of a qualified handle. A package directory
// carries slashes and never a dot, so the last dot is the separator.
func handlePkg(handle string) string {
	if i := strings.LastIndex(handle, "."); i >= 0 {
		return handle[:i]
	}
	return handle
}

// codeSpan wraps s in the shortest backtick fence that s does not itself
// contain, padding when s begins or ends with a backtick.
//
// Registered messages quote authoring syntax, so several carry backticks of
// their own; a fixed single-backtick wrapper would end the span early and spill
// the rest of the message into the page as prose.
func codeSpan(s string) string {
	longest := 0
	run := 0
	for _, r := range s {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
			continue
		}
		run = 0
	}
	fence := strings.Repeat("`", longest+1)
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		return fence + " " + s + " " + fence
	}
	return fence + s + fence
}

// renderHaltRegion returns the body of the halting-conditions page's generated
// region: one row per condition that page documents, carrying the short code a
// build prints, the sentence the diagnostic opens with, and the entry that
// documents it.
//
// # Why this region exists at all
//
// A condition's wording is declared once, beside its identity, and every
// diagnostic that names the condition opens with that sentence. The page's own
// entries quote a whole emitted message — the sentence plus the document, the
// locator and the advice a particular occurrence supplied — which is what an
// author needs to recognise output they are looking at, and which no declaration
// can supply. So the entries are not a transcription of the declarations and
// cannot be produced from them.
//
// The one part that IS a transcription is the leading sentence, and this region
// is its home on this page: rendered from the declaration, so the page cannot
// state a sentence the build does not open with, and comparable byte for byte
// rather than by reading prose.
//
// # Its corpus is the page's, not the declared set
//
// Membership comes from the page's own coverage declarations, so the region lists
// exactly the conditions this page documents and nothing else. That is what keeps
// it from becoming a second, divergent copy of the whole-set table published on
// the sentinel reference: the two answer different questions — that one resolves
// any code a build prints, this one resolves a code to the entry that teaches the
// authoring fix.
//
// A condition documented by more than one entry is one row naming both. Two
// conditions documented by a single entry are two rows naming it, which is how a
// pair of conditions registering different wording under one entry becomes
// visible on the page instead of being averaged into one sentence.
//
// # Rows name their entry rather than linking to it
//
// The third column carries the entry's heading as text and not as an in-page
// link. Rendering a heading as a link means reproducing the anchor the site's
// markdown renderer derives from it, and that derivation belongs to the renderer:
// a second implementation of it here would agree until a heading took a shape the
// two read differently, and the disagreement would surface as a broken link on a
// published page. The heading is quoted exactly, so a reader searches the page
// for it and lands on the entry. Do not "improve" this into a generated anchor.
//
// # Ordering
//
// Rows are ordered by short code, as text, which is also the numeric order
// because a short code is zero-padded to a fixed width. The short code is what a
// build prints, so it is the key a reader arrives holding.
func renderHaltRegion(ids []Identity, entries []pageEntry) (string, error) {
	byHandle := make(map[string]Identity, len(ids))
	for _, id := range ids {
		byHandle[id.Handle()] = id
	}

	// Headings are collected per condition in page order, so a condition
	// documented twice names its entries in the order the page introduces them.
	headings := map[string][]string{}
	var order []string
	for _, e := range entries {
		for _, h := range e.Handles {
			id, ok := byHandle[h]
			if !ok {
				// Unreachable through the generator, which resolves every
				// declaration before this runs and refuses one naming nothing.
				// Reported rather than skipped so that a caller wiring this
				// renderer up on its own cannot get a quietly short region.
				return "", fmt.Errorf("%s declares coverage of %s, which no declaration matches", haltsRelPath, h)
			}
			// A declaration may name a re-export, which is the same condition
			// under a second name. It is credited to the condition it forwards
			// to, so naming either name places one row.
			origin := h
			if id.AliasOf != "" {
				origin = id.AliasOf
			}
			if e.Heading == "" {
				// A row whose third column is empty points a reader at nothing
				// while reading as a resolved correspondence, which is worse
				// than an absent row: it cannot be told apart from a working
				// one. The declaration needs an entry to sit under.
				return "", fmt.Errorf("%s declares coverage of %s under no heading, so the region has no entry to name",
					haltsRelPath, h)
			}
			if _, seen := headings[origin]; !seen {
				order = append(order, origin)
			}
			if !slices.Contains(headings[origin], e.Heading) {
				headings[origin] = append(headings[origin], e.Heading)
			}
		}
	}
	if len(order) == 0 {
		return "", fmt.Errorf("%s declares coverage of no condition, so there is no region to render", haltsRelPath)
	}

	rows := make([]Identity, 0, len(order))
	for _, h := range order {
		id, ok := byHandle[h]
		if !ok {
			return "", fmt.Errorf("%s resolves to %s, which no declaration matches", haltsRelPath, h)
		}
		rows = append(rows, id)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ShortCode < rows[j].ShortCode })

	var b strings.Builder
	b.WriteString("| Short code | Opens with | Documented under |\n| --- | --- | --- |\n")
	for _, id := range rows {
		named := headings[id.Handle()]
		if err := rejectTableSeparator(id, []cell{
			{"short code", id.ShortCode},
			{"message", id.Message},
		}); err != nil {
			return "", err
		}
		for _, h := range named {
			if strings.Contains(h, "|") {
				return "", fmt.Errorf("the entry heading %q contains a table separator, "+
					"which no escaping renders correctly inside a cell; reword the heading", h)
			}
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n",
			codeSpan(id.ShortCode), codeSpan(id.Message), strings.Join(named, "; "))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}
