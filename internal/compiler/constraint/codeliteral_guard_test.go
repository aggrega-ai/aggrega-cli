// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package constraint

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The code-literal guard.
//
// A halting condition carries two identifiers and they are not interchangeable.
// The Go handle is the exported accessor its declaring package publishes, and it
// is what Go matches on. The code is the identifier the same condition is
// registered under, and it is what a consumer that does not link this module
// reads — an editor, a script, anything not written in Go. The contract that
// separates them is stated on the identity type in pkg/errors: the code is an
// output of an error and never an input to matching, and writing the same string
// back produces a string rather than an identity.
//
// Nothing in the language holds a Go caller to that. A caller may write the code
// string into a comparison and the result compiles: it type-checks, it reads
// like a match, and it goes on compiling after the condition is re-registered
// under a different code — at which point it silently stops matching. Nothing
// fails, nothing is logged, and the branch that was meant to recognise the
// condition simply never runs again. That is the failure this guard intercepts,
// and it is why the guard keys on the LITERAL rather than on the syntax around
// it: the shape of the expression is incidental, and matching a registered code
// is the whole signal.
//
// # What is outside the corpus, and why each exclusion is safe
//
// The package that declares the codes is excluded. A code literal there is the
// declaration itself, so reading it as a match against one would refuse every
// condition at the site that creates it.
//
// Generated files are excluded. Nobody edits one, so a finding in one is a
// finding about a generator and belongs where that generator is read; and the
// remedy this guard prints — reach for the accessor — is not available to a
// reader who cannot edit the file.
//
// A _test.go file is excluded, and this is the exclusion that carries an
// argument rather than a convenience. What this guard hunts is a match that
// stops matching SILENTLY: production code compiles across a re-registration,
// the branch stops running, and nothing fails and nothing is logged. In a test
// asserting the published wire value, the same re-registration makes the test
// FAIL — the literal is the expected value, so its failure IS the alarm this
// guard exists to substitute for. That is the opposite failure mode, and
// refusing it would refuse the one place where writing the code down is the
// point rather than a bypass.
//
// 🔴 The bound that exclusion leaves, recorded because it is not obvious and
// because a reader is otherwise entitled to read the exclusion as total: a test
// can carry the silent shape too, when the literal guards a FAILURE branch
// rather than standing as the asserted value — `if code == "x" { t.Fatal(…) }`
// stops matching after a re-registration and the test then passes vacuously.
// That shape is not covered here. Separating it from the asserted-value shape
// needs a heuristic about the test's intent, and a heuristic that refuses would
// reject legal input on a guess, which is what gets a guard switched off.
//
// A string that merely looks code-shaped is outside by construction rather than
// by exclusion. The guard's set is the registered codes and nothing else, so a
// shaped-but-unregistered string is not a member and is never reported. That
// matters more than it looks: a guard that fired on unrelated code would be
// switched off, and switching it off takes the real signal with it.
//
// # The population is derived, never listed here
//
// The set comes from the module's own identity index. Recognising the
// registration syntax a second time here would produce a reader that agrees with
// that index until the day a registration takes a shape only one of the two
// reads — and the agreement up to that day is exactly what would make the
// divergence invisible.

// codeLiteralGuardBlocks decides whether a finding fails this test or is only
// reported.
//
// It is true: the guard refuses a match. What made that possible is the
// corpus, not a cleanup, and the history is kept because the figures are what a
// reader would otherwise still be trusting.
//
// Criterion, unchanged throughout: a string literal equal to a registered code,
// appearing as an operand of == or !=, as a case expression, or as a map key.
//
//   - Measured 2026-08-08, scope every Go file this module holds outside the
//     declaring package and the generated files, test files INCLUDED, over the
//     1515 files that scope admitted: zero.
//   - Re-measured 2026-08-20 over the 1695 files it then admitted: six, every one
//     in the daemon's own diagnostic-code test.
//   - Re-measured 2026-09-10 at aggrega-cli 5ebaf069 over the 2198 files it then
//     admitted: SEVEN, and no longer confined to one file — six in the daemon's
//     diagnostic-code test and a seventh in the edit-operation refusal test.
//     Every one of the seven asserts that a published identifier carries the
//     code its condition is registered under.
//
// The population therefore grew, and its shape did not: each new site is the
// same asserted-wire-value shape, in which the literal is the expected value and
// a re-registration makes the test fail rather than pass quietly. Excluding
// test files is what the argument recorded at the top of this file always
// implied, and once they are excluded the refused population is empty —
// re-measured 2026-09-10 at the same revision over the 869 non-test files that
// scope admits: zero. So the guard blocks over a corpus it does not fail on,
// which is the only shape in which promoting it is an improvement rather than a
// gate somebody switches off.
//
// Why blocking rather than reporting, measured rather than assumed: a passing Go
// package prints nothing in a routine run — not t.Logf, not a write to standard
// error, not a write to standard output. All three were probed and all three
// were swallowed. A reporting guard's finding therefore reaches a reader only
// under `go test -v`, so the tier here is binary in a way it is not for a shell
// check, and there is no warn channel to settle for. Independently of this
// constant the instrument still blocks: the calibration and the index read below
// are both fatal, so a guard that has stopped working is loud either way.
const codeLiteralGuardBlocks = true

// declaringCodePackage is the package that registers the codes, relative to the
// module root. Its own files are outside the corpus.
const declaringCodePackage = "pkg/errors"

// identityIndexArgs invoke the module's identity index in its machine-readable
// form. The index is the one reader of the registrations in this module, and it
// publishes this form for consumers that need the set rather than the page.
var identityIndexArgs = []string{"run", "./internal/tools/errindex", "-index"}

// codeLiteralUse is one place a registered code appears as a literal, carrying
// the position, the code, and the shape it was written in. The shape is reported
// because it is what a reader needs to find the line, and never because the
// guard treats the shapes differently.
type codeLiteralUse struct {
	pos  token.Position
	code string
	form string
}

// TestNoGoSourceMatchesAgainstARegisteredCode finds every Go source match
// written against a registered code, so that the division between the Go handle
// and the registered code cannot decay into a convention nothing reads.
//
// Whether a finding fails this test or is only reported is
// [codeLiteralGuardBlocks], which is true, so a match is refused. The corpus it
// refuses over is the non-test files, for the reason recorded at the top of this
// file. The instrument blocks independently of that constant: the calibration
// and the index read below are fatal, so a guard that has stopped working is
// loud whatever tier the findings carry.
func TestNoGoSourceMatchesAgainstARegisteredCode(t *testing.T) {
	t.Parallel()

	registered := registeredCodes(t)
	calibrateCodeLiteralGuard(t, registered)

	// excludeTestFiles, for the reason recorded at the top of this file: a test
	// asserting a published wire value writes the literal deliberately, and a
	// re-registration makes such a test fail rather than stop matching in
	// silence. The bound that leaves is recorded there too.
	files, fset := collectGoFiles(t, []string{moduleRoot}, excludeTestFiles)

	var found []codeLiteralUse
	read := 0
	for path, file := range files {
		if outsideCodeLiteralCorpus(t, path) {
			continue
		}
		read++
		found = append(found, findCodeLiteralUses(fset, file, registered)...)
	}
	if read == 0 {
		t.Fatalf("no file was left in the corpus after the exclusions, so a clean result certifies nothing")
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].pos.Filename != found[j].pos.Filename {
			return found[i].pos.Filename < found[j].pos.Filename
		}
		return found[i].pos.Offset < found[j].pos.Offset
	})

	// The tier is named in the summary rather than left to the reader to infer
	// from whether the test went red. A line reading "0 matches" says nothing
	// about whether a match would have been refused, and those are the two facts
	// someone reading this output needs together.
	tier, report := "refuses a match", t.Errorf
	if !codeLiteralGuardBlocks {
		tier, report = "reports a match and does not refuse it", t.Logf
	}
	t.Logf("code-literal guard: %d registered code(s) read from the identity index; %d non-test file(s) "+
		"in the corpus, outside %s and the generated files; %d match(es) found. "+
		"This guard %s: see codeLiteralGuardBlocks.",
		len(registered), read, declaringCodePackage, len(found), tier)

	if len(found) == 0 {
		return
	}

	lines := make([]string, 0, len(found))
	for _, u := range found {
		lines = append(lines, u.pos.String()+": "+u.form+" against "+strconv.Quote(u.code))
	}
	report("%d Go source match(es) are written against a registered code rather than against the "+
		"accessor that publishes the condition.\n"+
		"CONSEQUENCE: the code is the identifier a consumer outside Go reads, and it is free to change "+
		"when a condition is re-registered. Such a match keeps compiling across that change and "+
		"silently stops matching — nothing fails and nothing is logged, so the branch simply stops "+
		"running.\n"+
		"REMEDY: match the condition with errors.Is against the accessor its declaring package exports. "+
		"Where the code itself is genuinely wanted, read it off the error rather than writing it back.\n"+
		"%s", len(found), strings.Join(lines, "\n"))
}

// registeredCodes returns every code a halting condition is registered under,
// read from the module's identity index.
//
// The index is invoked rather than reimplemented. It already reads the
// registrations for the published reference, and it offers a machine-readable
// form precisely so a consumer takes the set from the one place that extracts
// it. Its records carry a kind, and only the kind that declares a code is read:
// the other kind is a re-export, which forwards to a declared condition and
// registers no code of its own.
func registeredCodes(t *testing.T) map[string]bool {
	t.Helper()

	cmd := exec.Command("go", identityIndexArgs...)
	cmd.Dir = moduleRoot
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("read the identity index (go %s in %s): %v\n%s",
			strings.Join(identityIndexArgs, " "), moduleRoot, err, stderr.String())
	}

	// The width is pinned rather than read past. A record that gained or lost a
	// field is a record whose meaning may have moved, and reading field 3 out of
	// a shape this no longer recognises would silently match against the wrong
	// column. Pinning it means a widened index empties this set, which the
	// refusal below then reports as the instrument failure it is.
	const indexFields = 5

	codes := map[string]bool{}
	for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) != indexFields || fields[0] != "origin" || fields[3] == "" {
			continue
		}
		codes[fields[3]] = true
	}
	// An empty set would let every later assertion pass over nothing, and the
	// pass would be indistinguishable from a tree with no match in it. The index
	// changing its record shape is the realistic way that happens, and it is
	// silent from here, so it is refused rather than read as a result.
	if len(codes) == 0 {
		t.Fatalf("the identity index yielded no code, so the guard has nothing to match against and a "+
			"clean result would certify nothing; its record shape has moved. Output was:\n%s", out)
	}
	return codes
}

// calibrateCodeLiteralGuard proves the detector fires, and proves it stays
// silent on a string that is not registered, before either verdict is believed.
//
// It runs on every invocation rather than once at authoring time. A guard whose
// detector quietly stopped recognising a shape would report a clean tree, and a
// clean tree is what this guard reports when everything is well — the two are
// indistinguishable from the outside, which is the whole reason a zero here is a
// claim about the instrument until something makes it fire.
//
// The needle is built from a code taken from the derived set at run time and is
// never written out here. A real code spelled into this file would be a second
// copy of a declaration — the drift the guard exists to prevent — and it would
// then be a literal the guard had to exempt from its own corpus, which is how an
// exemption list starts.
func calibrateCodeLiteralGuard(t *testing.T, registered map[string]bool) {
	t.Helper()

	codes := make([]string, 0, len(registered))
	for code := range registered {
		codes = append(codes, code)
	}
	sort.Strings(codes)
	needle := strconv.Quote(codes[0])

	fires := "package p\n\nfunc f(code string, m map[string]int) bool {\n" +
		"\tif code == " + needle + " {\n\t\treturn true\n\t}\n" +
		"\tif code != " + needle + " {\n\t\treturn false\n\t}\n" +
		"\tswitch code {\n\tcase " + needle + ":\n\t\treturn true\n\t}\n" +
		"\t_ = map[string]int{" + needle + ": 1}\n" +
		"\treturn m[" + needle + "] > 0\n}\n"

	fset, parsed := parseCalibrationSource(t, fires)
	byForm := map[string]int{}
	for _, use := range findCodeLiteralUses(fset, parsed, registered) {
		byForm[use.form]++
	}
	for _, form := range []string{"comparison", "switch case", "map key"} {
		if byForm[form] == 0 {
			t.Fatalf("the detector does not report a %s written against a registered code, so it is not "+
				"a working instrument and its verdict over this module means nothing", form)
		}
	}

	// The other direction, and it is the one that keeps the guard usable. A
	// detector that reported ordinary strings would fire across the tree, and a
	// guard that fires on unrelated code is switched off — which takes the real
	// signal with it.
	clean := "package p\n\nfunc f(s string, m map[string]int) bool {\n" +
		"\tif s == \"a string no condition is registered under\" {\n\t\treturn true\n\t}\n" +
		"\tswitch s {\n\tcase \"another/one\":\n\t\treturn true\n\t}\n" +
		"\t_ = map[string]int{\"third/one\": 1}\n" +
		"\treturn m[\"fourth/one\"] > 0\n}\n"

	cleanFset, cleanParsed := parseCalibrationSource(t, clean)
	if uses := findCodeLiteralUses(cleanFset, cleanParsed, registered); len(uses) != 0 {
		t.Fatalf("the detector reported %d match(es) over source that names no registered code, so it "+
			"would fire across this module on strings that are not identities: %v", len(uses), uses)
	}
}

// parseCalibrationSource parses in-memory calibration source and returns it with
// its own file set, so a calibration position can never be confused with a
// position in the module.
func parseCalibrationSource(t *testing.T, source string) (*token.FileSet, *ast.File) {
	t.Helper()

	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "calibration.go", source, 0)
	if err != nil {
		t.Fatalf("parse the calibration source: %v", err)
	}
	return fset, parsed
}

// findCodeLiteralUses reports every string literal in file that equals a
// registered code and stands where the code is being matched: an operand of ==
// or !=, a case expression, or a map key — written as a key in a composite
// literal or as an index.
//
// The registered set is the only thing that decides membership: an unregistered
// string standing in any of those places is not a finding, whatever it looks
// like.
func findCodeLiteralUses(fset *token.FileSet, file *ast.File, registered map[string]bool) []codeLiteralUse {
	var found []codeLiteralUse

	record := func(form string, expr ast.Expr) {
		lit, ok := expr.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return
		}
		code, err := strconv.Unquote(lit.Value)
		if err != nil || !registered[code] {
			return
		}
		found = append(found, codeLiteralUse{pos: fset.Position(lit.Pos()), code: code, form: form})
	}

	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BinaryExpr:
			if x.Op == token.EQL || x.Op == token.NEQ {
				record("comparison", x.X)
				record("comparison", x.Y)
			}
		case *ast.CaseClause:
			for _, expr := range x.List {
				record("switch case", expr)
			}
		case *ast.IndexExpr:
			// A string-literal index is a map read. An array or slice index
			// cannot be a string, so no other construct reaches here.
			record("map key", x.Index)
		case *ast.CompositeLit:
			// A struct field key is an identifier, never a string literal, so a
			// string-literal key is a map entry without resolving any type.
			for _, elt := range x.Elts {
				if kv, ok := elt.(*ast.KeyValueExpr); ok {
					record("map key", kv.Key)
				}
			}
		}
		return true
	})
	return found
}

// outsideCodeLiteralCorpus reports whether path is excluded, for the two reasons
// recorded at the top of this file: it belongs to the package that declares the
// codes, or it is generated.
//
// Generated files are recognised by the name their generators give them. The
// header they carry is the more precise signal, but the shared walk parses
// without retaining comments, and re-reading every file to inspect a header
// would trade a whole second read of the tree for a distinction that no file in
// this module presents.
func outsideCodeLiteralCorpus(t *testing.T, path string) bool {
	t.Helper()

	rel, err := filepath.Rel(moduleRoot, path)
	if err != nil {
		t.Fatalf("locate %s within %s: %v", path, moduleRoot, err)
	}
	rel = filepath.ToSlash(rel)

	switch {
	case rel == declaringCodePackage, strings.HasPrefix(rel, declaringCodePackage+"/"):
		return true
	case strings.HasSuffix(rel, "_gen.go"):
		return true
	}
	return false
}
