// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"fmt"
	"go/ast"
	"sort"
	"strings"
)

// constraintPackageDir is the module-relative directory of the package that
// declares Check and the severities a check chooses between.
//
// A check declaration is recognised only when the file carrying it imports that
// package, or is that package. Matching a bare type name instead would let any
// unrelated generic type called Check declare a severity, and the resulting row
// would look exactly like a real one.
const constraintPackageDir = "internal/compiler/constraint"

// checkTypeName and warningSeverityName are the declaration this reads: the
// generic type a check is written as, and the severity value that says a
// violation is reported rather than halting.
//
// They are names this generator knows rather than shapes it infers, which is the
// same class of coupling every other extraction here rests on — Register, Meta,
// ShortCode and Message are read the same way. A declaration that moved out from
// under either name is reported by the caller as an empty advisory set rather
// than passed over in silence.
const (
	checkTypeName       = "Check"
	warningSeverityName = "SeverityWarning"
)

// checkSentinelField and checkSeverityField are the two fields of a check
// declaration this reads: the condition the check raises, and what a violation
// does to the build.
//
// Both are read from the same literal, and that is the whole reason a severity
// can be attributed to a condition at all. The registration beside a condition
// declares its wording and its codes and says nothing about consequence; the
// check declares the consequence and names the condition it belongs to. Neither
// half carries the correspondence on its own.
const (
	checkSentinelField = "Sentinel"
	checkSeverityField = "Severity"
)

// collectAdvisories records, into advisory, the handle of every condition a
// check in this file declares at warning severity.
//
// # What is being read, and why it is not read from the condition
//
// A condition's registration declares its wording, its descriptive code and its
// short code. It does not declare what a violation does to a build, and it must
// not: one condition can be reached from more than one place, and consequence is
// a property of the rule that raises it rather than of the wording it renders.
// The rule is where both facts sit together — a check names the condition it
// raises and declares whether a violation halts the build or is reported and
// passed over — so the correspondence is read from there or it is not read at
// all.
//
// # Every composite literal is inspected, and the position is not read
//
// A check is written as a package-level declaration in most of this module and
// as an element of a larger literal in some of it. Reading only the first shape
// would silently stop covering the second, and the omission would be invisible
// from the output: a table short by one row reads exactly like a module with one
// fewer advisory. So the whole file is walked and every composite literal is
// tested against the type it names, which is the fact that actually decides.
//
// 🔴 An element of a container literal names NO type of its own — Go elides it —
// so testing `lit.Type` alone answers false for exactly the second shape while
// looking like it covered both. The elided type is therefore carried down from
// the container that supplies it, which the pre-order walk makes possible
// because a container is always visited before its elements. Do not "simplify"
// this back to reading each literal's own type: the shape it stops recognising
// is the one this paragraph exists for, and nothing in the output distinguishes
// the two.
//
// The type is carried rather than resolved, so a container declared under a name
// of its own — a named slice type — supplies nothing, because resolving that
// name would mean type-checking the package, which is what this scan exists to
// avoid. No such declaration holds a check in this module; one appearing later
// is not silently miscounted, because the check inside it would then have to be
// written with its type spelled out and would be read by the first test above.
//
// # An unreadable sentinel is reported rather than skipped
//
// A check that declares warning severity and whose condition cannot be read is
// an advisory that would be silently absent from the published table. That is
// the one outcome worse than a missing row, because nothing distinguishes it
// from a module that has no such advisory, so it is returned as an error naming
// the declaration.
func collectAdvisories(advisory map[string]bool, file *ast.File, pkgDir string, imports map[string]string) error {
	local, isConstraintPkg := constraintPackageAlias(imports, pkgDir)
	if local == "" && !isConstraintPkg {
		return nil
	}

	var unreadable []string
	// The type an element literal inherits from the container that holds it,
	// recorded on the way down. ast.Inspect walks pre-order, so a container is
	// always reached before the elements whose type it supplies.
	inherited := map[*ast.CompositeLit]ast.Expr{}
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		typ := lit.Type
		if typ == nil {
			typ = inherited[lit]
		}
		if !namesCheckType(typ, local, isConstraintPkg) {
			for _, el := range elementLiterals(lit, containerElement(typ)) {
				inherited[el] = containerElement(typ)
			}
			return true
		}
		sentinel, severity := checkFields(lit)
		if !namesConstraintSymbol(severity, warningSeverityName, local, isConstraintPkg) {
			return true
		}
		h, readable := invokedHandle(sentinelCall(sentinel), pkgDir, imports)
		if !readable {
			unreadable = append(unreadable, pkgDir)
			return true
		}
		advisory[h] = true
		return true
	})
	if len(unreadable) == 0 {
		return nil
	}
	return fmt.Errorf("%d check %s in %s %s warning severity and %s a condition this reads no handle from; "+
		"such an advisory would be absent from the published table while reading exactly like a module that "+
		"declares none",
		len(unreadable), plural(len(unreadable), "declaration", "declarations"), pkgDir,
		plural(len(unreadable), "declares", "declare"), plural(len(unreadable), "names", "name"))
}

// containerElement returns the element type a slice, array or map literal
// supplies to elements that name no type of their own, or nil where the type is
// neither or is unknown.
//
// A map's VALUE is the element type: a key is written with the key type elided,
// not the value type, so reading the key half would carry the wrong type down.
func containerElement(typ ast.Expr) ast.Expr {
	switch v := typ.(type) {
	case *ast.ArrayType:
		return v.Elt
	case *ast.MapType:
		return v.Value
	}
	return nil
}

// elementLiterals returns the direct element literals of lit that name no type
// of their own, which are the ones the container's element type describes. It
// yields nothing when the container supplies no element type, so a struct
// literal's fields are never mistaken for container elements.
//
// A map element arrives as a key-value pair, and it is the value half that
// carries the element type. Reading the pair rather than only a bare literal is
// what keeps a map of checks readable; reading the key half would attribute the
// element type to a value the key type describes.
func elementLiterals(lit *ast.CompositeLit, element ast.Expr) []*ast.CompositeLit {
	if element == nil {
		return nil
	}
	var out []*ast.CompositeLit
	for _, el := range lit.Elts {
		if kv, ok := el.(*ast.KeyValueExpr); ok {
			el = kv.Value
		}
		if inner, ok := el.(*ast.CompositeLit); ok && inner.Type == nil {
			out = append(out, inner)
		}
	}
	return out
}

// sentinelCall returns the accessor invocation a check's sentinel field carries,
// or nil when the field is absent or is not an invocation.
//
// It is separated from the read above so that an absent field and a field
// carrying something other than an accessor call reach the same refusal. Both
// mean the same thing to a reader of the published table — an advisory with no
// row — and telling them apart in the report would name a distinction nobody
// repairs differently.
func sentinelCall(expr ast.Expr) *ast.CallExpr {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return &ast.CallExpr{Fun: &ast.BadExpr{}}
	}
	return call
}

// checkFields returns the expressions a check literal binds to its sentinel and
// its severity fields. A field the literal does not name yields nil.
//
// Only the keyed form is read. A check is written with its fields named
// throughout this module, and a positional literal of a generic struct whose
// field order this generator would have to know is a shape that does not occur —
// reading one would mean pinning a field order no declaration states.
func checkFields(lit *ast.CompositeLit) (sentinel, severity ast.Expr) {
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch key.Name {
		case checkSentinelField:
			sentinel = kv.Value
		case checkSeverityField:
			severity = kv.Value
		}
	}
	return sentinel, severity
}

// namesCheckType reports whether expr names the generic check type, for the two
// spellings a declaration is written in.
//
// A check takes two type arguments, so its type is always an instantiation
// rather than a bare name. Both instantiation forms the parser produces are
// unwrapped, because which one appears is a property of how many arguments were
// written and not of what is being declared — reading only the two-argument form
// would silently stop recognising a check the day one took a single argument.
func namesCheckType(expr ast.Expr, constraintPkg string, isConstraintPkg bool) bool {
	switch v := expr.(type) {
	case *ast.IndexExpr:
		return namesConstraintSymbolName(v.X, checkTypeName, constraintPkg, isConstraintPkg)
	case *ast.IndexListExpr:
		return namesConstraintSymbolName(v.X, checkTypeName, constraintPkg, isConstraintPkg)
	}
	return false
}

// namesConstraintSymbol reports whether expr names sym in the package that
// declares Check — `constraint.SeverityWarning` from outside it, a bare
// `SeverityWarning` from within. A nil expression names nothing, which is how an
// absent field reaches a false rather than a panic.
func namesConstraintSymbol(expr ast.Expr, sym, constraintPkg string, isConstraintPkg bool) bool {
	if expr == nil {
		return false
	}
	return namesConstraintSymbolName(expr, sym, constraintPkg, isConstraintPkg)
}

// namesConstraintSymbolName is the spelling test the two readers above share:
// the symbol qualified by the local name the file gave the constraint package,
// or unqualified when the file is that package.
func namesConstraintSymbolName(expr ast.Expr, sym, constraintPkg string, isConstraintPkg bool) bool {
	if isConstraintPkg {
		ident, ok := expr.(*ast.Ident)
		return ok && ident.Name == sym
	}
	if constraintPkg == "" {
		return false
	}
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != sym {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == constraintPkg
}

// constraintPackageAlias reports the local name the file uses for the package
// that declares Check, and whether the file IS that package — in which case the
// declarations are unqualified.
func constraintPackageAlias(imports map[string]string, pkgDir string) (string, bool) {
	if pkgDir == constraintPackageDir {
		return "", true
	}
	for local, dir := range imports {
		if dir == constraintPackageDir {
			return local, false
		}
	}
	return "", false
}

// advisoriesIn returns every condition declared at warning severity, ordered by
// short code.
//
// Re-exports are dropped. A re-export is another name for a condition already
// listed and reports the short code of the condition it forwards to, so keeping
// one would put two rows under a single code in a table whose whole job is to
// resolve that code.
func advisoriesIn(ids []Identity) []Identity {
	var out []Identity
	for _, id := range ids {
		if id.AliasOf != "" || !id.Advisory {
			continue
		}
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ShortCode < out[j].ShortCode })
	return out
}

// renderAdvisoryRegion returns the body of the advisory region: one row per
// condition a check declares at warning severity, carrying the short code a
// build prints and the sentence its message opens with.
//
// # Why this region exists beside the two that already resolve a short code
//
// The sentinel reference resolves any code a build prints, and the
// halting-conditions page resolves a code to the entry that teaches the
// authoring fix. Neither says which conditions advise. That fact is declared —
// a rule states whether a violation halts the build or is reported and passed
// over — and until this region it was published only as a hand-written sentence
// beside each entry, which is a transcription of the declaration and drifts from
// it the way every transcription does.
//
// So the three regions answer three questions, and membership is what separates
// them: every condition a build can report; the conditions one page documents;
// and the conditions that advise. A row is here because a check declared it so.
//
// # Its corpus is narrower than the advisory surface, and the page says so
//
// Only a condition raised through a declared check carries a severity. A finding
// raised directly by a pass of its own carries none, so it has no row here even
// where it advises — and some of what the page around this region teaches is
// reported with no condition behind it at all. Widening the corpus would mean
// deriving membership from the page's own prose, which is the transcription this
// region exists to retire.
//
// # No column names where the advisory is taught
//
// The conditions here are not all taught on the page that carries the region:
// one raised late in rendering is catalogued rather than taught. A column that
// was empty for some rows would read as a fact about those advisories rather
// than as the absence of a section, and filling it would need a second
// coverage-declaration mechanism on a second page.
//
// # Ordering
//
// Rows are ordered by short code, as text, which is also the numeric order
// because a short code is zero-padded to a fixed width. The short code is what a
// build prints, so it is the key a reader arrives holding.
func renderAdvisoryRegion(ids []Identity) (string, error) {
	rows := advisoriesIn(ids)
	if len(rows) == 0 {
		// The region would render as an empty table, which reads as a compiler
		// whose every condition halts rather than as a reader that stopped
		// recognising the declaration. A module with checks and no advisory among
		// them is not the state this tree is in, so an empty result here is the
		// instrument failure it looks like.
		return "", fmt.Errorf("no condition is declared at warning severity; " +
			"the check declaration form or the severity name has moved, and an empty table would read as a " +
			"compiler in which nothing advises")
	}

	// A duplicate short code would render two rows under one key and leave a
	// reader unable to tell which condition they are holding. Registration
	// already refuses one at package initialisation, so reaching here means the
	// extraction — not the declaration — has gone wrong.
	seen := make(map[string]string, len(rows))
	var b strings.Builder
	b.WriteString("| Short code | Opens with |\n| --- | --- |\n")
	for _, id := range rows {
		if prev, dup := seen[id.ShortCode]; dup {
			return "", fmt.Errorf("%s and %s both report the short code %s, so a lookup keyed on it "+
				"cannot name one condition", prev, id.Handle(), id.ShortCode)
		}
		seen[id.ShortCode] = id.Handle()
		if err := rejectTableSeparator(id, []cell{
			{"short code", id.ShortCode},
			{"message", id.Message},
		}); err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "| %s | %s |\n", codeSpan(id.ShortCode), codeSpan(id.Message))
	}
	return strings.TrimRight(b.String(), "\n"), nil
}
