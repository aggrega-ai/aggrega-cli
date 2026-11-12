// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Command haltidentity refuses a halt that composes a diagnostic head out of its
// own message text.
//
// # The defect, and why nothing sees it today
//
// One formatter composes the head a reader meets — the category in brackets, and
// beside it the short code of whatever registered condition the chain names. A
// halt that types a category into its own sentence instead is claiming that head
// while naming no condition: it looks to the reader exactly like a registered
// diagnostic, and it resolves to nothing they can quote, search or look up.
//
// The tooling beside this one reads the other direction. It checks the published
// pages against the declarations — that every declared condition has an entry,
// that every entry quotes the sentence its condition registers, that every
// reproduction of output is a transcript of something a build prints. None of it
// asks whether a halt reaching a reader carries a condition at all, which is how
// a whole parallel diagnostic system came to print its own bracketed head with
// nothing objecting.
//
// # What is a hit
//
// The message literal of a halt construction, at its HEAD, naming a declared
// category:
//
//	"OPS_ERROR: --baseline requires --pre-publish"
//	"[CATALOG_ERROR] recipe.yaml: ..."
//	"[TRANSLATION_ERROR E0231] ..."
//
// # The exempt classes, and why each is exempt BY CONSTRUCTION
//
// Requiring every failure to carry a registered condition would be as wrong as
// requiring none: a published short code is permanent, so registering is not
// free, and the formatter's own fallback is the documented presentation for a
// failure that crosses no categorised boundary. So the classes below are exempt,
// and each is exempt because of what this check READS rather than because of a
// list it consults — there is no exemption list here, and therefore nothing a
// later change can quietly add itself to.
//
//   - A HALT THAT NAMES NO CATEGORY. An internal invariant breach, a command
//     refusing its own arguments, a file it cannot read, a cancellation. It makes
//     no claim to be a categorised diagnostic, so there is no identity for it to
//     be missing, and forcing it through the framework would print an empty head
//     asserting a category that is absent — a different and wrong claim. Exempt
//     by construction: the oracle matches a declared category at the head of the
//     message, and such a halt has none anywhere.
//
//   - AN OUTPUT GRAMMAR, whose metavariables stand for whichever condition was
//     met. A help sentence teaching the shape of a head, a reference to a
//     category mid-sentence, a format string that composes a head from arguments.
//     None of them asserts a category for the failure they belong to. Exempt by
//     construction, twice over: the oracle reads the HEAD of the literal, so a
//     category named anywhere else is invisible, and a head assembled from a verb
//     carries no category token to match.
//
//   - A HALT WHOSE CATEGORY COMES FROM THE FRAMEWORK. Raise, Unidentified and
//     Wrap each take the category as a typed value beside the message, so the
//     head is composed once, by the formatter, from the declaration. Exempt by
//     construction: a Category value is an identifier rather than a string
//     literal, so it is never read as the message.
//
// # The remedy is always the same, and it always loses nothing
//
// Pass the category to the constructor instead of typing it into the sentence.
// Unidentified is the escape hatch for a halt that names no registered condition
// and still has a concern to route the reader to; Raise is for one that names a
// condition; Wrap re-categorises a chain and takes whatever identity the chain
// carries. All three render the head exactly once.
//
// Run it as:
//
//	go run ./internal/tools/haltidentity
//	go run ./internal/tools/haltidentity -source-root <dir>
//
// Exit codes: 0 clean, 1 a breach, 2 the verdict cannot be trusted.
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// minCategories is the plausibility floor on the derived offending side. It
// binds the DEFAULT root only, for the reason the sibling prefix gate records:
// a caller-chosen scratch root is a corpus whose scale the caller picked, so a
// floor over it could only refuse a legitimate self-test invocation.
//
// The floor sits far below the declared set and can only ever need LOWERING,
// which would itself mean the category set had collapsed — a finding in its own
// right. Emptiness refuses under either root and is checked separately, because
// an empty offending side makes this check VACUOUS and GREEN: it would report a
// clean tree while matching nothing at all.
const minCategories = 4

// minCorpusFiles is the same guard on the corpus side. A root that is present
// and holds almost no Go is an uninitialised checkout rather than a small
// module, and a walk that silently stopped matching reports clean over nothing.
const minCorpusFiles = 100

func main() {
	root := flag.String("source-root", "", "module root to walk; defaults to the working directory")
	report := flag.Bool("report", false, "print every hit with its constructor and full literal")
	flag.Parse()

	sourceRoot := *root
	if sourceRoot == "" {
		wd, err := os.Getwd()
		if err != nil {
			refuse("cannot resolve the working directory: %v", err)
		}
		sourceRoot = wd
	}
	abs, err := filepath.Abs(sourceRoot)
	if err != nil {
		refuse("cannot resolve %s: %v", sourceRoot, err)
	}
	sourceRoot = abs
	if info, err := os.Stat(sourceRoot); err != nil || !info.IsDir() {
		refuse("no such source root: %s\n  Refusing rather than walking an absent tree, which would report clean.", sourceRoot)
	}

	files, err := corpus(sourceRoot)
	if err != nil {
		refuse("walking %s: %v", sourceRoot, err)
	}
	if len(files) == 0 {
		refuse("the corpus is EMPTY — no eligible *.go under %s.\n"+
			"  The root is PRESENT, so a zero here is a claim about this walk rather than\n"+
			"  about the tree. Refusing rather than reporting clean over nothing.", sourceRoot)
	}

	parsed := make([]*file, 0, len(files))
	for _, rel := range files {
		f, err := parse(sourceRoot, rel)
		if err != nil {
			// A file that does not parse is a REFUSAL, never a file skipped. A
			// skipped file is a silently narrowed corpus, and the narrowing would
			// appear nowhere in the verdict.
			refuse("%s does not parse as Go: %v\n"+
				"  Refusing rather than SKIPPING it — a skipped file is a corpus this\n"+
				"  check narrowed without saying so.", rel, err)
		}
		parsed = append(parsed, f)
	}

	categories := declaredCategories(parsed)
	if len(categories) == 0 {
		refuse("the category derivation yielded NOTHING over %d corpus file(s).\n"+
			"  A zero there is a claim about the derivation, not about the tree: with an\n"+
			"  empty offending set this check is VACUOUS and GREEN — it would report a\n"+
			"  clean tree while matching nothing at all.", len(parsed))
	}

	if *root == "" {
		for _, floor := range []struct {
			label       string
			got, floor  int
			consequence string
		}{
			{"corpus file", len(parsed), minCorpusFiles,
				"an under-walked corpus makes this check quietly vacuous"},
			{"declared category", len(categories), minCategories,
				"an under-derived offending set makes this check quietly vacuous"},
		} {
			if floor.got < floor.floor {
				refuse("only %d %s(s) over the DEFAULT root, below the floor of %d.\n"+
					"  That is implausibly few for this module, so it reads as a BROKEN WALK\n"+
					"  rather than as a shrunken tree — and %s.\n"+
					"  Refusing. If the tree really did shrink this far, that is a finding in\n"+
					"  its own right; lower the floor in this file deliberately and say so.",
					floor.got, floor.label, floor.floor, floor.consequence)
			}
		}
	}

	literals, hits := scan(parsed, categories)

	fmt.Printf("haltidentity: %d corpus file(s) under %s (excluding _test.go, testdata/ and *_gen.go)\n",
		len(parsed), sourceRoot)
	fmt.Printf("haltidentity:   oracle: %d declared category value(s): %s\n",
		len(categories), strings.Join(categories, " "))
	fmt.Printf("haltidentity:   %d halt message literal(s) read; %d compose a head in their own text\n",
		literals, len(hits))

	if len(hits) == 0 {
		fmt.Println("haltidentity: no halt composes a diagnostic head out of its own message text")
		return
	}

	if *report {
		for _, h := range hits {
			fmt.Printf("haltidentity:     %s:%d  (%s)  %q\n", h.rel, h.line, h.ctor, h.message)
		}
	}

	fmt.Fprintln(os.Stderr)
	fmt.Fprintln(os.Stderr, "haltidentity: A HALT COMPOSES A DIAGNOSTIC HEAD OUT OF ITS OWN MESSAGE TEXT.")
	fmt.Fprintln(os.Stderr, "haltidentity: It claims the head a registered condition earns while naming no condition,")
	fmt.Fprintln(os.Stderr, "haltidentity: so a reader meets something that looks exactly like a diagnostic they can")
	fmt.Fprintln(os.Stderr, "haltidentity: quote, search and look up, and it resolves to nothing.")
	fmt.Fprintln(os.Stderr, "haltidentity:")
	for _, h := range hits {
		fmt.Fprintf(os.Stderr, "haltidentity:     %s:%d  (%s)\n", h.rel, h.line, h.ctor)
		fmt.Fprintf(os.Stderr, "haltidentity:       names %s at the head of %q\n", h.category, truncate(h.message))
	}
	fmt.Fprintln(os.Stderr, "haltidentity:")
	fmt.Fprintln(os.Stderr, "haltidentity:   HOW TO CLEAR EVERY ONE OF THESE: pass the category to the constructor")
	fmt.Fprintln(os.Stderr, "haltidentity:   instead of typing it into the sentence, and delete it from the sentence.")
	fmt.Fprintln(os.Stderr, "haltidentity:   Raise names a registered condition; Unidentified states that there is none")
	fmt.Fprintln(os.Stderr, "haltidentity:   and still routes the reader to a concern; Wrap re-categorises a chain and")
	fmt.Fprintln(os.Stderr, "haltidentity:   carries whatever identity that chain already names. All three render the")
	fmt.Fprintln(os.Stderr, "haltidentity:   head exactly once, from the declaration rather than from the text.")
	fmt.Fprintln(os.Stderr, "haltidentity:   Nothing is lost by the deletion: the head is what supplies the word.")
	os.Exit(1)
}

// file is one parsed corpus member, kept with what the scan below needs from it.
type file struct {
	rel     string
	fset    *token.FileSet
	syntax  *ast.File
	imports map[string]string // local name -> import path
}

// hit is one halt whose own message text opens with a declared category.
type hit struct {
	rel      string
	line     int
	ctor     string
	category string
	message  string
}

// corpus returns the eligible Go files under root, relative to it and sorted, so
// two runs over one tree read the same files in the same order.
//
// The exclusions are the ones the sibling prefix gate states, reached here for
// the same reasons: a test's error string reaches no user; a path under
// testdata/ is fixture data rather than this module's code, which is also what
// removes every nested fixture module from the walk; and a generated file's
// defect belongs to its generator, so flagging the output sends the reader to
// the wrong file.
func corpus(root string) ([]string, error) {
	var found []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") ||
			strings.HasSuffix(p, "_test.go") ||
			strings.HasSuffix(p, "_gen.go") {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		found = append(found, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(found)
	return found, nil
}

func parse(root, rel string) (*file, error) {
	fset := token.NewFileSet()
	syntax, err := parser.ParseFile(fset, filepath.Join(root, rel), nil, 0)
	if err != nil {
		return nil, err
	}
	imports := map[string]string{}
	for _, spec := range syntax.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		local := path[strings.LastIndex(path, "/")+1:]
		if spec.Name != nil {
			local = spec.Name.Name
		}
		imports[local] = path
	}
	return &file{rel: rel, fset: fset, syntax: syntax, imports: imports}, nil
}

// declaredCategories reads the offending side off the declarations rather than
// re-spelling it here: every constant whose declared type is Category and whose
// value is a string literal.
//
// Re-spelling is the failure this derivation exists to avoid, and it is not
// hypothetical — a guard elsewhere in this module enumerated six of the seven
// category spellings by hand and read as clean over the seventh. A value added
// beside the others is covered here with nobody editing anything.
//
// The type is matched by its identifier, bare or selected, so the declaring
// package and an importer of it are read alike. Reading a qualified form costs
// nothing and is what keeps a scratch corpus — which declares its own values in
// whichever shape it likes — derivable on its own terms.
func declaredCategories(files []*file) []string {
	seen := map[string]bool{}
	for _, f := range files {
		for _, decl := range f.syntax.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || !namesCategoryType(vs.Type) {
					continue
				}
				for _, v := range vs.Values {
					if lit, ok := v.(*ast.BasicLit); ok && lit.Kind == token.STRING {
						if s, err := strconv.Unquote(lit.Value); err == nil && s != "" {
							seen[s] = true
						}
					}
				}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func namesCategoryType(expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name == "Category"
	case *ast.SelectorExpr:
		return t.Sel.Name == "Category"
	}
	return false
}

// fmtComposers are the standard formatting verbs that can put a composed head in
// front of a reader: the one that builds an error, the ones that build a string
// another frame carries, and the ones that write straight to a stream.
//
// The write-side verbs are here rather than left out as "not error
// construction", and the reason is the defect this whole check answers: the
// parallel diagnostic system that printed its own bracketed head did it with a
// write verb, not with an error constructor. A set confined to error
// construction would have been blind to exactly the shape that motivated it.
//
// Including them costs nothing, because the oracle matches a declared category at
// the HEAD of the format. A stream write that names no category is invisible
// here, and one that composes its head from a verb — the output-grammar class —
// carries no category token to match.
var fmtComposers = map[string]bool{
	"Errorf":   true,
	"Sprintf":  true,
	"Sprint":   true,
	"Sprintln": true,
	"Fprintf":  true,
	"Fprint":   true,
	"Fprintln": true,
	"Printf":   true,
	"Print":    true,
	"Println":  true,
}

// constructorName returns the halt constructor the call names, resolved through
// the file's OWN import block rather than matched as a bare selector.
//
// The resolution is necessary rather than tidy: this module imports its own
// diagnostic package under several different local names, and a bare match on
// the selector also collects New calls from packages that carry no diagnostic at
// all. A selector whose qualifier resolves to no import of interest yields
// nothing, so a same-named call on an unrelated package cannot enter the corpus.
func (f *file) constructorName(call *ast.CallExpr) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	qualifier, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	path, ok := f.imports[qualifier.Name]
	if !ok {
		return "", false
	}
	name := sel.Sel.Name
	switch {
	case path == "fmt" && fmtComposers[name]:
	case path == "errors" && name == "New":
	case strings.HasSuffix(path, "/pkg/errors"):
		// The framework's own constructors are read too, and that is the half a
		// reader is most likely to think unnecessary. A category passed as a typed
		// value beside a message that ALSO opens with one prints the word twice on
		// one line — the very doubling the framework's head exists to prevent — so
		// the constructors that compose a head correctly are exactly the ones where
		// a hand-written head is worst.
		switch name {
		case "Raise", "Unidentified", "Wrap", "New", "ComposeMessage":
		default:
			return "", false
		}
	default:
		return "", false
	}
	return qualifier.Name + "." + name, true
}

// messageLiteral returns the first string literal at ARGUMENT DEPTH of call.
//
// Not "the first argument", because the message is not always first and a
// positional rule would read the wrong argument in much of the corpus:
// Raise(condition, category, source, message, context) carries it fourth,
// Wrap(err, category, source, message) fourth, Errorf(format, …) first — and in
// every one of those it is the first argument that IS a literal, because a
// condition, a category and a source location are not.
//
// Depth matters for the same reason: a literal inside a nested call or a
// composite literal belongs to that expression rather than to this one. A
// concatenation is descended into, since its first operand is the head of the
// message the call carries.
func messageLiteral(call *ast.CallExpr) (string, bool) {
	for _, arg := range call.Args {
		if s, ok := headLiteral(arg); ok {
			return s, true
		}
	}
	return "", false
}

func headLiteral(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(e.Value)
		if err != nil {
			return "", false
		}
		return s, true
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		return headLiteral(e.X)
	case *ast.ParenExpr:
		return headLiteral(e.X)
	}
	return "", false
}

// composedHead returns the declared category a message composes at its own head,
// and whether it composes one at all.
//
// Two shapes are read and no others, because those are the two the formatter
// itself produces: the bracketed head it renders, and the bare "CATEGORY: "
// prefix a hand-written message reaches for. A category named anywhere else in
// the sentence is left alone — see the exempt classes in this file's own
// documentation, and the same bound the sibling prefix gate states for itself.
//
// The bracketed form admits a second token after the category, because the head
// carries the short code there: "[CATALOG_ERROR E0209] …" is as much a composed
// head as "[CATALOG_ERROR] …" and is worse, since it also invents a code.
func composedHead(message string, categories []string) (string, bool) {
	for _, c := range categories {
		if strings.HasPrefix(message, c+":") {
			return c, true
		}
		if rest, ok := strings.CutPrefix(message, "["+c); ok {
			if rest == "" || strings.HasPrefix(rest, "]") || strings.HasPrefix(rest, " ") {
				return c, true
			}
		}
	}
	return "", false
}

// scan reads every halt construction in the corpus and returns how many message
// literals it read beside the hits among them.
//
// The size is returned with the verdict because a zero is a claim about this
// walk until the corpus it walked sits beside it: a run that read two thousand
// literals and a run that read none both print "no hit", and only the figure
// tells the two apart.
func scan(files []*file, categories []string) (int, []hit) {
	literals := 0
	var hits []hit
	for _, f := range files {
		ast.Inspect(f.syntax, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			ctor, ok := f.constructorName(call)
			if !ok {
				return true
			}
			message, ok := messageLiteral(call)
			if !ok {
				return true
			}
			literals++
			category, composed := composedHead(message, categories)
			if !composed {
				return true
			}
			hits = append(hits, hit{
				rel:      f.rel,
				line:     f.fset.Position(call.Pos()).Line,
				ctor:     ctor,
				category: category,
				message:  message,
			})
			return true
		})
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].rel != hits[j].rel {
			return hits[i].rel < hits[j].rel
		}
		return hits[i].line < hits[j].line
	})
	return literals, hits
}

func truncate(s string) string {
	if len(s) <= 100 {
		return s
	}
	return s[:97] + "..."
}

func refuse(format string, args ...any) {
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr, "haltidentity: "+format+"\n", args...)
	os.Exit(2)
}
