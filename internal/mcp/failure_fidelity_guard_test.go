// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The guard behind the fidelity rule stated in this package's documentation: a
// refusal carries whatever findings the operation had in hand, in the failing
// result's structured payload.
//
// Nothing reads a comment, so a rule that lives only in prose goes on reading as
// documentation of behaviour long after a site stops following it. This is what
// holds the sites to it.
//
// # Why a ledger rather than a prohibition
//
// A prose-only failure return is not wrong by itself. A refused argument, an
// unresolvable path, a workspace that could not be read — none of those ran an
// operation, so none of them has findings to carry, and the prose IS the whole
// answer. What the rule forbids is a failure return that discards findings the
// run already produced, and no syntactic property distinguishes the two.
//
// So the guard measures the property it CAN establish mechanically — which
// failure returns carry no structured payload — and pins that set against an
// explicit ledger. Judging which of them is a loss stays with the reader of the
// rule. What the ledger buys is that the set cannot grow, or shrink, without
// somebody editing this file and saying so.
//
// It fails in both directions deliberately. A new prose-only failure return is
// drift and fails here. A site that adopts the structured route and leaves its
// ledger entry standing also fails here, because the ledger's whole value is
// that its size is the honest count of the work remaining — a ledger nobody has
// to shrink stops being a measurement and becomes decoration.

// proseOnlyFailureReturns is the ledger: per file, the number of failure returns
// carrying no structured payload.
//
// CRITERION, stated here because a count without one cannot be re-derived: a
// call, in a non-test file of this package, to errorResult with any arguments, or
// to structuredErrorResult with a literal nil payload — excluding the bodies of
// those two functions themselves, since the delegation between them is the one
// construction site rather than a tool's failure return.
//
// Every entry is a candidate for removal rather than a permanent fact. Reducing
// one means a failure return started carrying what its run had found.
var proseOnlyFailureReturns = map[string]int{
	// Two pre-flight refusals — a missing release argument and a workspace that
	// could not be resolved. Neither ran the compiler, so neither has findings.
	// The render failure in this file carries its payload.
	"tool_build.go": 2,
	// The lint tool's argument refusals. Its post-run refusal carries the
	// findings the run collected.
	"tool_lint.go": 2,
	// The dependency-resolution tool's pre-flight refusals — a missing release
	// argument, an empty or blank upgrade target, the refused argument combination,
	// and a workspace that could not be resolved. None of them ran the resolution,
	// so none has findings. Its post-run refusal carries the resolution the run had
	// computed, which is what makes a drift verdict readable field by field.
	"tool_pull.go": 5,
	// The generated command-tool surface. Its post-run failure path holds both
	// captured streams and concatenates them into prose, so the bytes survive
	// and the separation between output and diagnostics does not.
	"commandtools.go": 2,
	// The two graph-node tools. Both run the preview with the render skipped and
	// refuse before any result is handed back, so at the failure return there is
	// nothing structured in hand.
	"tool_candidates.go":           12,
	"tool_attachment_container.go": 6,
	// Validation and description. Their closure-resolution refusals discard the
	// stage attribution the resolution itself produced, and their payloads
	// promise a validity field a consumer reads across scopes.
	"tool_validate.go": 13,
	"tool_describe.go": 6,
	// Publishing. Whether the push returns a partially-populated result
	// alongside its error is not established, so whether these lose anything is
	// unmeasured rather than known.
	"tool_publish.go": 8,
	// Inventory. Its search reports which registries could not be consulted, and
	// the failure return does not.
	"tool_inventory.go": 3,
	// Catalog generation. Its failure return drops the output accumulated before
	// the failure, which the generated command-tool surface beside it keeps.
	"tool_gencatalog.go": 5,
	// Position lookup, workspace description, the progressive surface, and the
	// shared argument-decode helper: argument and context refusals.
	"tool_insertion_point.go": 6,
	"tool_workspace.go":       2,
	"progressive.go":          1,
	"tools.go":                1,
}

// TestFailureReturnsWithoutStructuredContentMatchTheLedger is the guard.
func TestFailureReturnsWithoutStructuredContentMatchTheLedger(t *testing.T) {
	measured, files := censusProseOnlyFailureReturns(t)

	// Self-calibration, before any comparison. A census that parsed nothing, or
	// that failed to find the one call site this package is guaranteed to contain,
	// would report an empty set and agree with an empty ledger — passing while
	// having measured nothing at all.
	if files < 10 {
		t.Fatalf("the census parsed %d non-test file(s) in this package; it cannot have found them all, "+
			"so its zero rows would be a property of the census rather than of the package", files)
	}
	total := 0
	for _, n := range measured {
		total += n
	}
	if total == 0 {
		t.Fatalf("the census found no failure return carrying no structured payload across %d file(s). "+
			"Either every failure return in this package now carries a payload — in which case this guard's "+
			"ledger and this assertion both need retiring together — or the census stopped matching the "+
			"call shape it looks for", files)
	}

	for _, name := range sortedKeys(measured, proseOnlyFailureReturns) {
		got, inMeasured := measured[name]
		want, inLedger := proseOnlyFailureReturns[name]
		switch {
		case inMeasured && !inLedger:
			t.Errorf("%s has %d failure return(s) carrying no structured payload and no ledger entry. "+
				"A run that produced findings and then refused must carry them; if this one produced none, "+
				"add the file to the ledger in this file and the guard will hold it there", name, got)
		case inLedger && !inMeasured:
			t.Errorf("%s has a ledger entry for %d failure return(s) and now has none. Remove the entry: "+
				"the ledger's size is the count of the work remaining, and an entry nobody has to remove "+
				"stops measuring anything", name, want)
		case got > want:
			t.Errorf("%s has %d failure return(s) carrying no structured payload; the ledger records %d. "+
				"A new one was added. Carry the findings the run had in hand, or raise the entry and say "+
				"why the run had none", name, got, want)
		case got < want:
			t.Errorf("%s has %d failure return(s) carrying no structured payload; the ledger records %d. "+
				"Lower the entry to %d — a site started carrying its findings and the ledger has to shrink "+
				"with it", name, got, want, got)
		}
	}
}

// censusProseOnlyFailureReturns counts, per file, the failure returns in this
// package that carry no structured payload, and returns the number of non-test
// files it parsed so the caller can tell an empty result from an empty census.
//
// It reads the syntax tree rather than the text: a pattern over source would
// match the same call spelled in a comment and would miss one split across
// lines, and neither miss is visible from its own output.
func censusProseOnlyFailureReturns(t *testing.T) (map[string]int, int) {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	counts := map[string]int{}
	parsed := 0
	fset := token.NewFileSet()
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(".", name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		parsed++
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			// The two constructors delegate to each other, and that delegation is
			// the single construction site rather than any tool's failure return.
			if fn.Name.Name == "errorResult" || fn.Name.Name == "structuredErrorResult" {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok && isProseOnlyFailureReturn(call) {
					counts[name]++
				}
				return true
			})
		}
	}
	return counts, parsed
}

// isProseOnlyFailureReturn reports whether a call constructs a failure result
// with no structured payload.
func isProseOnlyFailureReturn(call *ast.CallExpr) bool {
	ident, ok := call.Fun.(*ast.Ident)
	if !ok {
		return false
	}
	switch ident.Name {
	case "errorResult":
		return true
	case "structuredErrorResult":
		// A literal nil payload omits structuredContent from the wire, so such a
		// call is prose-only however it is spelled. A payload expression that
		// evaluates to nil at run time is not distinguishable here and is not
		// treated as one.
		return len(call.Args) == 2 && isNilIdent(call.Args[1])
	}
	return false
}

// isNilIdent reports whether an expression is the literal nil.
func isNilIdent(e ast.Expr) bool {
	ident, ok := e.(*ast.Ident)
	return ok && ident.Name == "nil"
}

// sortedKeys returns the union of both maps' keys in a stable order, so a
// failure names every drifted file in the same sequence on every run.
func sortedKeys(a, b map[string]int) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(a)+len(b))
	for _, m := range []map[string]int{a, b} {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}
