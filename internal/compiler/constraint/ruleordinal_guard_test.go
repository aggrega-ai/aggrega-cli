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

// The rule-ordinal guard.
//
// A shape-rule violation carries an identifier naming the rule it broke, and
// that identifier reaches the line a terminal prints. A reader acts on it, so it
// has to say what the rule constrains: "values-mapping" tells them which part of
// the document to open, where a bare position in a list tells them nothing they
// can do anything with, and tells them less every time the list is reordered.
// That is the failure this guard intercepts — an ordinal written into the
// identifier field, which compiles, reads plausibly beside its neighbours, and
// reaches a user.
//
// It is here rather than beside the field because of what let the ordinal in.
// The two censuses that guard this area both read what a condition REGISTERS —
// its code and its message text — and this identifier is neither: it is supplied
// per occurrence, at the site that reports the failure. So both instruments were
// structurally unable to see it, and agreed on a clean answer while a user was
// reading an ordinal. A third instrument keyed on the shape of the value is what
// closes that, and closing it structurally is the point: the next ordinal would
// otherwise enter exactly the way this one did.
//
// The guard keys on the shape rather than on the words. It reports a bare string
// literal that is nothing but digits, whatever the digits are, wherever the
// identifier field is written. Keying on the known ordinals instead — a list of
// the numbers that were once used — would say nothing about the next number, and
// the next number is the whole population this guard exists for.
//
// A descriptive literal is NOT a finding. The identifiers are written as literals
// at their reporting sites by design: each is one word or short phrase naming
// what that site checks, there is no closed set for them to be members of, and a
// guard that fired on every one of them would be switched off within a day.
//
// # What this guard cannot see, and every finding it will therefore miss
//
// An unstated bound reads as full coverage, so each one is named.
//
// It reads the field NAME as syntax and resolves no types. A field spelled this
// way on some unrelated type is read the same way, and an ordinal written into
// one is reported — which is the right answer for the same reason, one vocabulary
// over. The converse is the real bound: it cannot confirm that a site reached for
// the identifier field of the type it meant.
//
// It sees only a value that reaches the field DIRECTLY. An ordinal handed to a
// helper that writes the field one call frame away is neither a keyed field nor a
// selector, so it is not a finding, and a caller is free to write one. So is an
// ordinal that arrives as a constant, a formatted string, a loop index converted
// to text, or a value read from a document: each is a legitimate expression here
// and none is inspected. What it refuses is the literal written in place, which
// is how every ordinal this module carried was in fact written.
//
// It cannot judge whether a descriptive identifier is a GOOD one. A literal
// naming the wrong part of the document, or naming a position in words rather
// than in digits — "first", "rule-one" — is clean here. What proves an identifier
// says something a reader can act on is the entity's own reference documentation
// and the review that keeps the two agreeing.
//
// It reads Go, and every Go file this module holds, tests included. A test that
// pins an ordinal into an expected diagnostic is how one stops being a slip and
// starts being a contract, so a test is in the corpus rather than outside it.
// Every non-Go surface the identifier crosses is outside: the reference
// documentation that heads each rule's entry, and the rendered diagnostics
// themselves, each of which carries its own check or none.
//
// Fixture and vendored trees are outside, and so is any directory holding its own
// go.mod: the walk stops there, because a nested module's source is not this
// module's to hold to this rule.

// ruleFieldName is the field this guard watches — the identifier a shape-rule
// violation carries to name the rule it broke. One name, matched as syntax, so
// the guard covers every type that carries the field without knowing any of
// them.
const ruleFieldName = "Rule"

// ruleOrdinalUse is one place a bare ordinal literal reaches the rule-identifier
// field, carrying the position, the literal, and the shape it was written in. The
// shape is reported because it is what a reader needs to find the line, never
// because the guard treats the shapes differently.
type ruleOrdinalUse struct {
	pos   token.Position
	value string
	form  string
}

// TestNoGoSourceWritesARuleOrdinal refuses a bare ordinal literal written into
// the rule-identifier field, so that "a rule names what it constrains, never its
// position in a list" is a property something checks rather than a convention
// whose next violation nobody notices.
//
// It refuses rather than reports. The population it refuses is empty, so nothing
// in the tree has to be cleaned before it blocks, and a guard that only reports
// is silent in a routine run: a passing Go package prints nothing.
func TestNoGoSourceWritesARuleOrdinal(t *testing.T) {
	t.Parallel()

	calibrateRuleOrdinalGuard(t)

	files, fset := collectGoFiles(t, []string{moduleRoot}, includeTestFiles)

	var found []ruleOrdinalUse
	read, sites := 0, 0
	for _, file := range files {
		read++
		uses, inspected := findRuleOrdinalUses(fset, file)
		found = append(found, uses...)
		sites += inspected
	}

	// Zero inspected is not zero found, and the two are indistinguishable from
	// the outside: a guard whose walk stopped reaching the tree, and a tree with
	// no violation in it, both report nothing. So an empty corpus is refused as
	// the instrument failure it is rather than served as a clean verdict. Both
	// halves are refused separately, because a walk can read the whole module and
	// still see no rule-identifier field at all — which is what a rename of the
	// field looks like from here.
	if read == 0 {
		t.Fatalf("no Go file was left in the corpus, so a clean result certifies nothing")
	}
	if sites == 0 {
		t.Fatalf("the corpus holds %d file(s) but not one %s field, so a clean result certifies "+
			"nothing; the field this guard watches has been renamed or has moved out of Go",
			read, ruleFieldName)
	}

	sort.Slice(found, func(i, j int) bool {
		if found[i].pos.Filename != found[j].pos.Filename {
			return found[i].pos.Filename < found[j].pos.Filename
		}
		return found[i].pos.Offset < found[j].pos.Offset
	})

	// The corpus size travels with the verdict. A line reading "0 ordinal(s)"
	// says nothing about how much was looked at, and those are the two facts a
	// reader of this output needs together.
	t.Logf("rule-ordinal guard: %d Go file(s) in the corpus; %d %s field site(s) inspected; "+
		"%d bare ordinal(s) found. This guard refuses an ordinal.",
		read, sites, ruleFieldName, len(found))

	if len(found) == 0 {
		return
	}

	lines := make([]string, 0, len(found))
	for _, u := range found {
		lines = append(lines, u.pos.String()+": "+u.form+" of "+strconv.Quote(u.value))
	}
	t.Errorf("%d bare ordinal literal(s) reach a %s field.\n"+
		"CONSEQUENCE: this identifier reaches the diagnostic a terminal prints. A position in a list "+
		"tells the reader nothing they can act on, and tells them something false as soon as the list "+
		"is reordered.\n"+
		"REMEDY: name what the rule constrains, in kebab-case, the way the entity's reference "+
		"documentation heads that rule's entry.\n"+
		"%s", len(found), ruleFieldName, strings.Join(lines, "\n"))
}

// calibrateRuleOrdinalGuard proves the detector fires on each shape it claims to
// cover, and proves it stays quiet on a descriptive identifier, before either
// verdict is believed.
//
// It runs on every invocation rather than once at authoring time. A detector that
// quietly stopped recognising a shape would report a clean tree, and a clean tree
// is what this guard reports when all is well — the two are indistinguishable
// from the outside, which is why a zero here is a claim about the instrument until
// something makes it fire.
func calibrateRuleOrdinalGuard(t *testing.T) {
	t.Helper()

	fires := "package p\n\ntype v struct{ " + ruleFieldName + " string }\n\n" +
		"func f(x *v) bool {\n" +
		"\t_ = v{" + ruleFieldName + ": \"1\"}\n" +
		"\tx." + ruleFieldName + " = \"12\"\n" +
		"\tif x." + ruleFieldName + " == \"7\" {\n\t\treturn true\n\t}\n" +
		"\treturn x." + ruleFieldName + " != \"0\"\n}\n"

	fset, parsed := parseCalibrationSource(t, fires)
	uses, sites := findRuleOrdinalUses(fset, parsed)
	byForm := map[string]int{}
	for _, use := range uses {
		byForm[use.form]++
	}
	for _, form := range []string{"keyed field", "assignment", "comparison"} {
		if byForm[form] == 0 {
			t.Fatalf("the detector does not report a %s of a bare ordinal to a %s field, so it is not a "+
				"working instrument and its verdict over this module means nothing", form, ruleFieldName)
		}
	}
	if sites == 0 {
		t.Fatalf("the detector counted no %s field site over source that writes four of them, so its "+
			"corpus figure would report an empty corpus over a populated one", ruleFieldName)
	}

	// The other direction, and it is the one that keeps the guard usable. Every
	// corrected site is a descriptive literal written in place, so a detector that
	// reported one would fire on all of them and be switched off — which takes the
	// real signal with it. A multi-digit value that is not only digits is clean
	// too: the finding is an ordinal, not the presence of a numeral.
	clean := "package p\n\ntype v struct{ " + ruleFieldName + " string }\n\n" +
		"func f(x *v, s string) bool {\n" +
		"\t_ = v{" + ruleFieldName + ": \"values-mapping\"}\n" +
		"\tx." + ruleFieldName + " = \"input-kinds\"\n" +
		"\tx." + ruleFieldName + " = \"http-2\"\n" +
		"\tx." + ruleFieldName + " = s\n" +
		"\tif x." + ruleFieldName + " == \"filename-collision\" {\n\t\treturn true\n\t}\n" +
		"\treturn s == \"1\"\n}\n"

	cleanFset, cleanParsed := parseCalibrationSource(t, clean)
	if cleanUses, cleanSites := findRuleOrdinalUses(cleanFset, cleanParsed); len(cleanUses) != 0 {
		t.Fatalf("the detector reported %d ordinal(s) over source whose every %s value names what the "+
			"rule constrains, so it would fire on corrected code: %v", len(cleanUses), ruleFieldName, cleanUses)
	} else if cleanSites == 0 {
		t.Fatalf("the detector counted no %s field site over source that writes five of them, so a clean "+
			"tree and an unreached tree would read the same", ruleFieldName)
	}
}

// findRuleOrdinalUses reports every bare string literal in file that is nothing
// but digits and reaches a field named [ruleFieldName], and returns alongside it
// the number of such field sites the walk inspected — the corpus figure, counted
// whether or not the value at the site was an ordinal.
//
// Three shapes reach the field and each is read: a key in a composite literal, an
// assignment through a selector, and a comparison against one. A struct field key
// is an identifier rather than a string, so a keyed match needs no type
// resolution; a selector match reads the selected name and deliberately ignores
// what it was selected from, which is what makes one guard cover every type that
// carries the field.
func findRuleOrdinalUses(fset *token.FileSet, file *ast.File) (uses []ruleOrdinalUse, sites int) {
	record := func(form string, expr ast.Expr) {
		sites++
		lit, ok := expr.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil || !isBareOrdinal(value) {
			return
		}
		uses = append(uses, ruleOrdinalUse{pos: fset.Position(lit.Pos()), value: value, form: form})
	}
	selectsRule := func(expr ast.Expr) bool {
		sel, ok := expr.(*ast.SelectorExpr)
		return ok && sel.Sel != nil && sel.Sel.Name == ruleFieldName
	}

	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CompositeLit:
			for _, elt := range x.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if key, ok := kv.Key.(*ast.Ident); ok && key.Name == ruleFieldName {
					record("keyed field", kv.Value)
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
				if selectsRule(lhs) {
					record("assignment", x.Rhs[i])
				}
			}
		case *ast.BinaryExpr:
			if x.Op != token.EQL && x.Op != token.NEQ {
				return true
			}
			if selectsRule(x.X) {
				record("comparison", x.Y)
			}
			if selectsRule(x.Y) {
				record("comparison", x.X)
			}
		}
		return true
	})
	return uses, sites
}

// isBareOrdinal reports whether value is a position in a list and nothing else:
// one or more ASCII digits, with no other character anywhere in it.
//
// The bound is deliberate in both directions. An empty value is not an ordinal —
// the field is optional, and an occurrence that names no rule omits it. And a
// value merely CONTAINING a digit is not one either: a descriptive identifier is
// free to carry a numeral that belongs to the thing it names, and refusing those
// would fire on correct code.
func isBareOrdinal(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
