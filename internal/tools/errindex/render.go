// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"sort"
	"strings"
)

// renderRegion returns the body of the generated region: one section per
// package a reader can import, each listing the names that package exports and
// the message each renders.
//
// Only importable packages appear. An entry naming a package Go makes
// unreachable from outside the module advertises a match no reader can write,
// and a reference page's left-hand column is a promise that the name can be
// dereferenced.
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
		b.WriteString("| Sentinel | Message |\n| --- | --- |\n")

		rows := byPkg[p]
		sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
		var reExports []Identity
		for _, id := range rows {
			if strings.Contains(id.Message, "|") {
				return "", fmt.Errorf("%s: the registered message contains a table separator, "+
					"which no escaping renders correctly inside a cell; reword the message", id.Handle())
			}
			fmt.Fprintf(&b, "| %s | %s |\n", codeSpan(id.Name), codeSpan(id.Message))
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
					codeSpan(id.Name), codeSpan(id.AliasOf))
			}
		}
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

// renderIndex returns the machine-readable identity index, one record per line:
// a kind, the declaring package, the name, and either the declared code value or
// the qualified handle a re-export forwards to.
//
// It exists so that a consumer outside this module reads the identity set from
// the one place that extracts it, rather than growing a second recogniser of the
// same declaration syntax. Two recognisers of one population agree until the day
// a declaration takes a shape only one of them reads, and the agreement up to
// that day is exactly what makes the divergence invisible.
func renderIndex(ids []Identity) string {
	var b strings.Builder
	for _, id := range ids {
		kind, detail := "origin", id.Code
		if id.AliasOf != "" {
			kind, detail = "alias", id.AliasOf
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s\n", kind, id.Pkg, id.Name, detail)
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
