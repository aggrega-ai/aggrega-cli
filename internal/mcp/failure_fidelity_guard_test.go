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
// One class recurs across the entries below, and it is a refusal rather than a
// loss: a path argument naming a location outside the workspace a listener
// serves. That workspace is fixed when the server starts, and such a path is
// rejected at the argument — ahead of any resolution, compile, validation, lint,
// generation or render — so nothing has run and there is nothing in hand. A
// payload composed there would be invented rather than reported, which is the
// failure this guard exists to prevent rather than a form of it.
//
// The count does not track how many path arguments a tool resolves. Where the
// refusal is raised inside an operation that reports failure as an error, it is
// carried out and answered once, so several refusing arguments reach a single
// return.
//
// Every entry is a candidate for removal rather than a permanent fact. Reducing
// one means a failure return started carrying what its run had found.
var proseOnlyFailureReturns = map[string]int{
	// Two pre-flight refusals — a missing release argument and a workspace that
	// could not be resolved. Neither ran the compiler, so neither has findings.
	// The third refuses a Release path this session may not name, decided before
	// the dependency fill and the render. The render failure in this file carries
	// its payload.
	//
	// The fourth refuses a response format the tool does not declare. It is read
	// from the arguments alone, ahead of the dependency fill, so at that return
	// nothing has resolved and there is no render in hand to carry.
	"tool_build.go": 4,
	// The lint tool's argument refusals, and the refusal of a path this session
	// may not name or of a workspace that could not be resolved — raised before
	// the run lints anything and carried back out as an error so it can be
	// answered in the refusal's own words, since the label on the return beside it
	// says the run reported findings and nothing was linted. That post-run refusal
	// carries the findings the run collected.
	"tool_lint.go": 3,
	// The dependency-resolution tool's pre-flight refusals — a missing release
	// argument, an empty or blank upgrade target, the refused argument combination,
	// a workspace that could not be resolved, and a Release path this session may
	// not name. None of them ran the resolution, so none has findings. Its post-run
	// refusal carries the resolution the run had computed, which is what makes a
	// drift verdict readable field by field.
	"tool_pull.go": 6,
	// The entity author's pin step. Three of these are decided before the step is
	// entered: the refusal of a request naming no entity, a workspace that could
	// not be resolved, and an entity path this session may not name. At each of
	// them nothing has been read or resolved, so there is nothing in hand to carry.
	//
	// The fourth is the only post-run one, and that it loses nothing is established
	// rather than assumed: every failing return of the pin step's entry point hands
	// back the zero result. The step runs over the one entity the call names, so a
	// write that failed has no other entity's outcome to report, and a refusal of
	// an entity sharing its directory names every entity in the way in its own
	// words. What stopped the step reaches the caller as the prose, and the lock
	// beside the entity is unchanged.
	"tool_pin.go": 4,
	// The generated command-tool surface. Its post-run failure path holds both
	// captured streams and concatenates them into prose, so the bytes survive
	// and the separation between output and diagnostics does not.
	"commandtools.go": 2,
	// The two graph-node tools. Both run the preview with the render skipped and
	// refuse before any result is handed back, so at the failure return there is
	// nothing structured in hand. One entry each refuses a Release path this
	// session may not name, decided ahead of the preview.
	//
	// The attach candidates for a graph node refuse once more after the preview,
	// where the Release's reach is asked for the candidacy about to be drawn. The
	// preview has read that Release, so the reach refuses only a Release file that
	// went missing or became a directory between the two reads. The candidacy is
	// not drawn at that return, and the preview's result is a graph this answer
	// reads one node's labels from and reports nothing else of, so there is
	// nothing in hand to carry.
	//
	// One entry each refuses the lockfile beside that Release where it is a link
	// leading out of the root its write is held to. It is read ahead of the
	// preview, to build the view the preview resolves through, so no preview has
	// run and there is no graph in hand.
	"tool_candidates.go":           15,
	"tool_attachment_container.go": 8,
	// Validation and description. Their closure-resolution refusals discard the
	// stage attribution the resolution itself produced, and their payloads
	// promise a validity field a consumer reads across scopes.
	//
	// Beyond those, validation refuses four paths this session may not name — the
	// Release, the entity, and the plugin binary together with the entity it is
	// cross-checked against — and description refuses one. Each is decided before
	// the core behind it runs.
	//
	// One entry each is the refusal of a response format the tool does not
	// declare, read from the arguments alone and ahead of the scope the call goes
	// on to select, so neither has run anything whose findings it could carry.
	//
	// Description reads a registry reference as well, and that path adds three.
	// Two refuse before anything is fetched — a workspace that could not be
	// resolved, and a reference a listener does not read because its registry is
	// not one the served workspace is configured with — so neither has run anything.
	// The third is a read that failed: it hands back no partial entity, so the
	// one thing it has in hand is the condition that stopped it, and the prose
	// opens with that condition's short code.
	//
	// Description's lookup by name refuses one more before it looks anything up: a
	// catalog directory the name would be resolved through that cannot be walked.
	// It is asked ahead of the lookup, so there is no definition in hand to carry.
	//
	// Both tools take a Release beside a single subject — the name description
	// looks up, the entity validation checks — and each refuses two values of it
	// before the lookup or the check runs: a Release path this session may not
	// name, and a value naming no Release file. Description refuses a third from
	// the arguments alone, ahead of either: a registry reference sent with a
	// Release, which no Release's catalog directories reach.
	//
	// Description's lookup by name refuses a name that resolves to nothing, with
	// and without a Release, as two returns. The lookup ran and resolved no
	// definition, so there is none to carry; the account of where that Release
	// reads local entities from, which is all else the lookup has, is carried in
	// the refusal's own words.
	//
	// The Release scope of each refuses one more: the lockfile beside the Release
	// where it is a link leading out of the root its write is held to. It is read
	// to build the view the closure is resolved through, ahead of the resolution,
	// so nothing has been resolved, validated or described at that return.
	"tool_validate.go": 20,
	"tool_describe.go": 16,
	// Publishing. Whether the push returns a partially-populated result
	// alongside its error is not established, so whether these lose anything is
	// unmeasured rather than known.
	"tool_publish.go": 8,
	// Inventory. Its search reports which registries could not be consulted, and
	// the failure return does not. The fourth is the cache inventory's refusal,
	// and that one loses nothing: the shared enumeration withholds its report
	// entirely rather than answering with partial totals, because the totals are a
	// claim about the whole cache and a half-walked inventory understates them
	// while reading exactly like a small one. So at that failure return there is
	// no structured answer in hand to carry, by the operation's own design.
	//
	// The fifth is the cache inventory's refusal of a workspace whose configuration
	// is held. It is decided from the resolution alone, before any cache is walked,
	// so there is no inventory in hand to carry.
	"tool_inventory.go": 5,
	// The configuration reading. Both of these are pre-flight and neither loses
	// anything, but they are pre-flight in two different senses and the second is
	// the one worth stating.
	//
	// The first is a workspace that could not be resolved. Nothing has been read
	// at that point — there is no cascade, no layer and no setting — so there is
	// no reading in hand to carry.
	//
	// The second is the provenance replay itself failing. It walks the cascade one
	// layer at a time and reports a layer it cannot read or parse, and it hands
	// back nothing rather than a partial attribution: a settings list missing the
	// layers that could not be read reads exactly like a workspace those layers
	// never contributed to, which is a wrong answer indistinguishable from a right
	// one. So the whole reading is withheld by that layer's own design, and there
	// is no partially-populated result for this return to carry.
	"tool_configuration.go": 2,
	// The plugin reading. All four are pre-flight, and the reading has NO
	// post-run failure return at all — which is the fact worth recording here,
	// because it is the class this guard exists for. A plugin that will not
	// resolve or will not describe is carried on its own record with its failure
	// beside it and the listing goes on, so there is no point at which findings
	// exist and a prose-only return could discard them.
	//
	// Two of the four refuse an argument — a response format this tool does not
	// declare, and a window it does not serve — both read from the arguments
	// alone, ahead of any workspace. The third refuses a server started without
	// the enumeration this reading is composed from: nothing can be walked, and
	// answering with an empty listing would state that the workspace binds no
	// plugins, which is a different claim. The fourth is a workspace that could
	// not be resolved, at which point there is no configuration, no socket and no
	// binding in hand.
	"tool_plugins.go": 4,
	// Catalog generation. Its failure return drops the output accumulated before
	// the failure, which the generated command-tool surface beside it keeps.
	//
	// Four of these are not that loss. This tool now resolves a workspace to name
	// its paths against, which refuses when none resolves, and it refuses the
	// plugin, output-directory and overlay paths a session may not name. The first
	// of the four is a workspace refusal rather than a path one, and it is entered
	// here for the same reason: all four are decided before generation starts,
	// against an empty output buffer, so none of them drops anything.
	"tool_gencatalog.go": 9,
	// The document edit. Five of these are decided from the arguments alone,
	// ahead of anything the edit reads or composes: the refusal of a request
	// naming no document, the refusal of one naming no edit, the withdrawal a
	// caller signals before any resolution, a workspace that could not be
	// resolved, and the refusal of a document path this session may not name.
	//
	// The sixth is the only post-run one, and that it loses nothing is
	// established rather than assumed: every failing return of the shared edit
	// layer's entry point hands back the zero result, so no partially-populated
	// result exists for this return to carry. What the layer refused on reaches
	// the caller in the refusal's own words — opened by the registered short
	// code where the condition carries one, and carrying the validator's own
	// sentence where the refusal is the schema's.
	"tool_document_edit.go": 6,
	// The foreign-description import. Nine of these are decided before the
	// importer is reached at all: the refusal of a request naming no source, the
	// refusal of one naming a blank format, the withdrawal a caller signals
	// before any resolution, a session carrying no plugin resolution an importer
	// could be loaded through, a workspace that could not be resolved, a source
	// path this session may not name, a source that could not be read, a format
	// that could not be settled from the file's own shape, and inputs the
	// workspace's importer attachment declares that could not be read. At each of
	// them nothing has been translated and nothing has been written, so there is
	// nothing in hand to carry.
	//
	// The tenth is the only post-run one, and that it loses nothing is
	// established rather than assumed: every failing return of the shared import
	// layer's entry point hands back the zero result, so no partially-populated
	// result exists for it to carry. The one outcome that DOES have findings in
	// hand — an importer that could not translate the whole source — is not in
	// this count at all: it takes the structured route and carries the files that
	// reached disk, the drafts and every diagnostic.
	"tool_import.go": 10,
	// Position lookup, workspace description, the progressive surface, and the
	// shared argument-decode helper: argument and context refusals. Position
	// lookup carries one more, refusing a document path this session may not name
	// before the lookup reads anything.
	//
	// The second and third entries against the dispatch surface are the refusals of
	// a call that names a workspace the session does not let it name, and of a call
	// that selects a signing key held on this machine where the caller is not the
	// one it belongs to. Both are decided from the arguments alone, before the named
	// tool is entered, so no operation has run and there are no findings to carry.
	//
	// The progressive surface's three divide two ways, and only the first way is
	// argument refusal. Two of them are: an entity kind outside the vocabulary, and
	// the narrowing argument passed together with the naming one. Each is decided
	// from the arguments alone, ahead of any walk of the tool registries, so nothing
	// has run.
	//
	// The third is raised after that walk, and it withholds by the operation's own
	// design rather than by omission. A call naming tools that do not resolve is
	// refused whole: the descriptors that did resolve are a partial answer that
	// reads exactly like a complete one, which is the reading the whole-call refusal
	// exists to prevent, and the names that did not resolve are carried in the
	// refusal's own words, every one of them. So nothing the walk produced is
	// dropped there. Carrying the resolved half would not be fidelity; it would be
	// the loss under another name.
	//
	// The workspace description's last four are the two listings refusing a window
	// they do not serve — a page size outside the range, or a negative start — and
	// the same two listings refusing a response format they do not declare. All
	// four are read from the arguments alone and ahead of the workspace being
	// resolved, so at each of them nothing has been walked and no listing exists to
	// carry.
	//
	// The entity listing refuses three values of the Release it is asked to answer
	// in the terms of, all before the workspace is walked: a blank value, which
	// names none and is read from the arguments alone; a Release path this session
	// may not name; and a value naming no Release file. At none of them does a
	// listing exist to carry.
	//
	// It refuses one more before the walk: a workspace whose configuration is held,
	// asked for in a Release's terms or with catalog directories laid onto the
	// stack the held configuration decides. That is the hold's refusal every other
	// tool answers the same workspace with, decided from the arguments and the
	// resolution alone, so no listing exists to carry.
	"tool_insertion_point.go": 7,
	"tool_workspace.go":       10,
	"progressive.go":          3,
	"tools.go":                3,
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
