// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

import (
	"go/ast"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The severity-literal guard.
//
// A severity is a member of a closed set, and every such set in this module is
// declared: a named constant per tier, in the package that owns the wire the
// tier travels on. A bare string literal written into a Severity field bypasses
// that declaration. It compiles, it reads correctly, and it goes on compiling
// after the set it belonged to is respelled — at which point the site is
// carrying a value that is no longer a member of anything, with nothing to say
// so. That is the failure this guard intercepts.
//
// The guard keys on the shape rather than on the words. It reports a bare string
// literal assigned to, or compared against, a field named Severity, whatever the
// literal says. Keying on the words instead would mean naming the tiers of one
// vocabulary here, and this module carries several severity vocabularies that
// share words with each other and with unrelated things — a log-level mapper
// spells two of them — so a word-keyed guard would refuse correct code in one
// vocabulary while saying nothing about a literal in the next one to arrive.
// Shape survives a vocabulary this file has never heard of.
//
// # The one literal this guard does not report, and why it is not a weakening
//
// A COMPARISON against the empty string is clean. It is the only carve-out, and
// it is bounded by the reason it rests on rather than by convenience.
//
// The failure above is that a literal keeps compiling after the set it belonged
// to is respelled, leaving a site carrying a value that is no longer a member of
// anything. The empty string is a member of no severity vocabulary in this module
// and cannot become one: it is the zero value of the field, which is the spelling
// of ABSENCE, so respelling any tier set leaves such a comparison exactly as
// correct as it was. A required-argument presence check is what the shape is for,
// and it is the idiomatic Go form of one.
//
// The remedy this guard prints — name the tier at the site — presupposes a tier
// is being named. Following it here would mean declaring a constant for the empty
// string, a constant naming nothing, which is worse than the literal it replaced.
//
// The carve-out is deliberately narrow in both directions, and the calibration
// pins both: a comparison against a real tier spelling is still reported, and an
// ASSIGNMENT of the empty string is still reported, because writing absence into
// a severity field is a value-carrying act rather than a test of one.
//
// # What this guard cannot see, and every finding it will therefore miss
//
// An unstated bound reads as full coverage, so each one is named.
//
// It reads the field NAME as syntax and resolves no types. It cannot tell one
// severity vocabulary from another and does not try: a site is clean once its
// value is a declared constant, whichever set that constant belongs to. So it
// cannot judge whether a site reached for the RIGHT vocabulary — a value going
// out on one wire spelled with the constant of another is invisible here, and
// answering it would need type resolution.
//
// It sees only a value that reaches a Severity field DIRECTLY. A literal handed
// to a helper that writes the field one call frame away is neither a keyed field
// nor a selector, so it is not a finding, and a caller is free to write one. The
// remedy there is the same constant, reached for at the call site.
//
// It cannot judge a constant's VALUE. A declared constant spelled wrongly is
// clean here; what proves a value is the test that asserts on the bytes going
// over the wire.
//
// It reads Go, and only the Go a production build compiles. Test files are
// outside the corpus, and so is every non-Go surface a severity crosses —
// schemas, catalog documents, the editor extension's own sources — each of which
// carries its own vocabulary with its own guard or none.
//
// Fixture and vendored trees are outside, and so is any directory holding its own
// go.mod: the walk stops there, because a nested module's source is not this
// module's to hold to this rule.

// severityFieldName is the field this guard watches. One name, matched as syntax:
// every severity vocabulary in this module surfaces on a field spelled this way,
// which is what lets one guard cover all of them without knowing any of their
// tiers.
const severityFieldName = "Severity"

// The three shapes a value reaches the field in, named once so the detector, the
// calibration and the reported line all spell them the same way.
const (
	severityFormKeyed      = "keyed field"
	severityFormAssignment = "assignment"
	severityFormComparison = "comparison"
)

// severityLiteralUse is one place a bare string literal reaches a severity field,
// carrying the position, the literal, and the shape it was written in. The shape
// is reported because it is what a reader needs to find the line, never because
// the guard treats the shapes differently.
type severityLiteralUse struct {
	pos   token.Position
	value string
	form  string
}

// severityLiteralCell is one cell of the grid the calibration drives: a shape
// paired with the literal written in it. The detector's behaviour varies along
// both axes — one cell is carved out — so the calibration keys on the pair rather
// than on the shape alone.
type severityLiteralCell struct {
	form  string
	value string
}

// TestNoGoSourceWritesASeverityLiteral refuses a bare string literal written into
// a severity field, so that "every severity value reaches its field through a
// declared constant" is a property something checks rather than a convention
// whose next violation nobody notices.
//
// It refuses rather than reports. The population it refuses is empty, so nothing
// in the tree has to be cleaned before it blocks, and a guard that only reports
// is silent in a routine run: a passing Go package prints nothing.
func TestNoGoSourceWritesASeverityLiteral(t *testing.T) {
	t.Parallel()

	calibrateSeverityLiteralGuard(t)

	files, fset := collectGoFiles(t, []string{moduleRoot}, excludeTestFiles)

	var found []severityLiteralUse
	read, sites := 0, 0
	for _, file := range files {
		read++
		uses, inspected := findSeverityLiteralUses(fset, file)
		found = append(found, uses...)
		sites += inspected
	}

	// Zero inspected is not zero found, and the two are indistinguishable from
	// the outside: a guard whose walk stopped reaching the tree, and a tree with
	// no violation in it, both report nothing. So an empty corpus is refused as
	// the instrument failure it is rather than served as a clean verdict. Both
	// halves are refused separately, because a walk can read the whole module and
	// still see no severity field at all — which is what a rename of the field
	// looks like from here.
	if read == 0 {
		t.Fatalf("no Go file was left in the corpus, so a clean result certifies nothing")
	}
	if sites == 0 {
		t.Fatalf("the corpus holds %d file(s) but not one %s field, so a clean result certifies "+
			"nothing; the field this guard watches has been renamed or has moved out of Go",
			read, severityFieldName)
	}

	sort.Slice(found, func(i, j int) bool {
		if found[i].pos.Filename != found[j].pos.Filename {
			return found[i].pos.Filename < found[j].pos.Filename
		}
		return found[i].pos.Offset < found[j].pos.Offset
	})

	// The corpus size travels with the verdict. A line reading "0 literal(s)"
	// says nothing about how much was looked at, and those are the two facts a
	// reader of this output needs together.
	t.Logf("severity-literal guard: %d non-test Go file(s) in the corpus; %d %s field site(s) "+
		"inspected; %d bare literal(s) found. This guard refuses a literal.",
		read, sites, severityFieldName, len(found))

	if len(found) == 0 {
		return
	}

	lines := make([]string, 0, len(found))
	for _, u := range found {
		lines = append(lines, u.pos.String()+": "+u.form+" of "+strconv.Quote(u.value))
	}
	t.Errorf("%d bare string literal(s) reach a %s field without passing through a declared constant.\n"+
		"CONSEQUENCE: a severity is a member of a closed set. A literal is not a member of anything, so "+
		"it keeps compiling after the set is respelled and the site silently carries a value that is no "+
		"longer a tier.\n"+
		"REMEDY: declare a constant per tier in the package that owns the wire the tier travels on, and "+
		"name the tier at the site.\n"+
		"%s", len(found), severityFieldName, strings.Join(lines, "\n"))
}

// calibrateSeverityLiteralGuard proves the detector fires on each shape it claims
// to cover, proves the one carve-out is exactly one cell wide, and proves it stays
// quiet where the value is a constant, before any of the three verdicts is
// believed.
//
// It runs on every invocation rather than once at authoring time. A detector that
// quietly stopped recognising a shape would report a clean tree, and a clean tree
// is what this guard reports when all is well — the two are indistinguishable
// from the outside, which is why a zero here is a claim about the instrument
// until something makes it fire.
//
// The behaviour is a GRID rather than a list — three shapes, times whether the
// literal is empty — and the carve-out sits in its interior, at one cell. So all
// six cells are driven here rather than the margins alone: naming the shapes on
// one axis and emptiness on the other would leave the carve-out's own cell, and
// every cell adjacent to it, unstated.
func calibrateSeverityLiteralGuard(t *testing.T) {
	t.Helper()

	fires := "package p\n\ntype d struct{ " + severityFieldName + " string }\n\n" +
		"func f(x *d, y *d) bool {\n" +
		"\t_ = d{" + severityFieldName + ": \"keyed\"}\n" +
		"\t_ = d{" + severityFieldName + ": \"\"}\n" +
		"\tx." + severityFieldName + " = \"assigned\"\n" +
		"\ty." + severityFieldName + " = \"\"\n" +
		"\tif x." + severityFieldName + " == \"compared\" {\n\t\treturn true\n\t}\n" +
		"\tif y." + severityFieldName + " == \"\" {\n\t\treturn false\n\t}\n" +
		"\treturn x." + severityFieldName + " != \"compared-ne\"\n}\n"

	fset, parsed := parseCalibrationSource(t, fires)
	uses, sites := findSeverityLiteralUses(fset, parsed)
	reported := map[severityLiteralCell]int{}
	for _, use := range uses {
		reported[severityLiteralCell{form: use.form, value: use.value}]++
	}
	for _, cell := range []severityLiteralCell{
		{severityFormKeyed, "keyed"},
		{severityFormKeyed, ""},
		{severityFormAssignment, "assigned"},
		{severityFormAssignment, ""},
		{severityFormComparison, "compared"},
		{severityFormComparison, "compared-ne"},
	} {
		if reported[cell] == 0 {
			t.Fatalf("the detector does not report a %s of the bare literal %q to a %s field, so it is not a "+
				"working instrument and its verdict over this module means nothing",
				cell.form, cell.value, severityFieldName)
		}
	}
	// The carve-out itself, asserted as an absence against the five presences
	// above. Together they pin it to one cell: were it widened to the whole
	// comparison shape, the two comparisons above would stop being reported and
	// the loop would have failed already.
	if n := reported[severityLiteralCell{severityFormComparison, ""}]; n != 0 {
		t.Fatalf("the detector reported %d %s(s) against the empty string, so the carve-out this guard "+
			"documents is not in force and a required-argument presence check would be refused as though "+
			"it named a tier", n, severityFormComparison)
	}
	if sites == 0 {
		t.Fatalf("the detector counted no %s field site over source that writes seven of them, so its "+
			"corpus figure would report an empty corpus over a populated one", severityFieldName)
	}
	// The carve-out withholds a finding and never a site: the presence check is a
	// real severity field site, and a corpus figure that skipped it would
	// understate how much was looked at.
	if sites != 7 {
		t.Fatalf("the detector counted %d %s field site(s) over source that writes seven of them; the "+
			"corpus figure travels with the verdict, so it may not skip the site whose finding is carved out",
			sites, severityFieldName)
	}

	// The other direction, and it is the one that keeps the guard usable. A
	// detector that reported a constant would fire at every corrected site, and a
	// guard that fires on correct code is switched off — which takes the real
	// signal with it. The declaration of a constant is itself neither a keyed
	// field nor a selector, so it must stay clean too.
	clean := "package p\n\ntype d struct{ " + severityFieldName + " string }\n\n" +
		"const tier = \"a-tier\"\n\n" +
		"func f(x *d, s string) bool {\n" +
		"\t_ = d{" + severityFieldName + ": tier}\n" +
		"\tx." + severityFieldName + " = tier\n" +
		"\tx." + severityFieldName + " = s\n" +
		"\tif x." + severityFieldName + " == tier {\n\t\treturn true\n\t}\n" +
		"\tif x." + severityFieldName + " == \"\" {\n\t\treturn true\n\t}\n" +
		"\treturn s == \"not a severity field\"\n}\n"

	cleanFset, cleanParsed := parseCalibrationSource(t, clean)
	if cleanUses, cleanSites := findSeverityLiteralUses(cleanFset, cleanParsed); len(cleanUses) != 0 {
		t.Fatalf("the detector reported %d literal(s) over source whose every %s value is a declared "+
			"constant, a variable, or a presence check against the empty string, so it would fire on "+
			"corrected code: %v", len(cleanUses), severityFieldName, cleanUses)
	} else if cleanSites == 0 {
		t.Fatalf("the detector counted no %s field site over source that writes several of them, so a clean "+
			"tree and an unreached tree would read the same", severityFieldName)
	}
}

// findSeverityLiteralUses reports every bare string literal in file that reaches a
// field named [severityFieldName], and returns alongside it the number of such
// field sites the walk inspected — the corpus figure, counted whether or not the
// value at the site was a literal.
//
// Three shapes reach the field and each is read: a key in a composite literal, an
// assignment through a selector, and a comparison against one. A struct field key
// is an identifier rather than a string, so a keyed match needs no type
// resolution; a selector match reads the selected name and deliberately ignores
// what it was selected from, which is what makes one guard cover every type that
// carries the field.
func findSeverityLiteralUses(fset *token.FileSet, file *ast.File) (uses []severityLiteralUse, sites int) {
	record := func(form string, expr ast.Expr) {
		sites++
		lit, ok := expr.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil {
			return
		}
		// The one carve-out, stated at the top of this file: comparing the field
		// against the empty string tests for ABSENCE, and absence is a member of
		// no tier set, so respelling one cannot falsify the site. The site is
		// still counted in the corpus figure — it is a real severity field site,
		// merely not a finding.
		if form == severityFormComparison && value == "" {
			return
		}
		uses = append(uses, severityLiteralUse{pos: fset.Position(lit.Pos()), value: value, form: form})
	}
	selectsSeverity := func(expr ast.Expr) bool {
		sel, ok := expr.(*ast.SelectorExpr)
		return ok && sel.Sel != nil && sel.Sel.Name == severityFieldName
	}

	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CompositeLit:
			for _, elt := range x.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if key, ok := kv.Key.(*ast.Ident); ok && key.Name == severityFieldName {
					record(severityFormKeyed, kv.Value)
				}
			}
		case *ast.AssignStmt:
			// A multi-value assignment pairs by position; an unbalanced one is a
			// call returning several values, where no single right-hand
			// expression belongs to the field.
			if len(x.Lhs) != len(x.Rhs) {
				return true
			}
			for i, lhs := range x.Lhs {
				if selectsSeverity(lhs) {
					record(severityFormAssignment, x.Rhs[i])
				}
			}
		case *ast.BinaryExpr:
			if x.Op != token.EQL && x.Op != token.NEQ {
				return true
			}
			if selectsSeverity(x.X) {
				record(severityFormComparison, x.Y)
			}
			if selectsSeverity(x.Y) {
				record(severityFormComparison, x.X)
			}
		}
		return true
	})
	return uses, sites
}
