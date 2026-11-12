// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package viewnarrowing

import (
	"fmt"
	"go/ast"
	"go/token"
	"slices"
	"strconv"
	"strings"
)

// Kind is what a finding says. Values are the identifiers a report prints and a
// driver matches on.
type Kind string

const (
	// OverDeclared is the defect this package exists to find: the check declares
	// a view later on the chain than the facts its body reads require, so it is
	// placed later in the compilation than it needs to be.
	OverDeclared Kind = "over-declared"

	// NoFactRead is a check whose body reads no fact from its view at all. Its
	// declaration then places it at nothing: any view satisfies an empty
	// requirement, so the view it names carries no information.
	NoFactRead Kind = "no-fact-read"

	// Unreadable is a check whose body cannot be fully read, so no narrowing
	// verdict is drawn for it. The view escapes to somewhere the reading cannot
	// follow, or the predicate is not written in place.
	Unreadable Kind = "unreadable"

	// UnknownView is a check declaring a view the chain does not carry. Nothing
	// places it, because the declaration names no stage.
	UnknownView Kind = "unknown-view"

	// UnknownFact is a check reading an accessor no view on the chain declares.
	// The narrowest sufficient view cannot be computed from a fact the chain
	// does not carry.
	UnknownFact Kind = "unknown-fact"

	// SubjectOnlyUnsupported is a check declaring that its input comes from its
	// subject where the body does not bear that declaration out: it reads a
	// view fact after all, it reads nothing from its subject either, or the
	// reason it states names nothing the predicate reads.
	//
	// The declaration is the one way a check reading no fact is cleared here,
	// so it is read back against the body rather than taken on trust. A
	// declaration nothing checks is one a later author applies by habit, and
	// then a check placed at nothing is indistinguishable from a check that
	// meant it.
	SubjectOnlyUnsupported Kind = "subject-only-unsupported"
)

// Finding is one thing read about one check.
//
// Kind is what the finding says, and File and Line are where the check is
// declared. Check is the name the check is declared under, empty for a check
// literal that is not bound to a name. DeclaredView is the view its declaration
// names. NarrowestView is the view that would suffice, set only where one could
// be computed — so an empty NarrowestView means no verdict was drawn rather than
// that the innermost stage was the answer. Facts are the accessor names its body
// was read to call, sorted. Detail carries whatever the kind needs beyond the two
// views: the facts no view declares, how the view left the body, or the way a
// subject-only declaration failed to hold against the body.
type Finding struct {
	Kind          Kind
	File          string
	Line          int
	Check         string
	DeclaredView  string
	NarrowestView string
	Facts         []string
	Detail        string
}

// String renders a finding as one line, and is what a report prints.
func (f Finding) String() string {
	subject := f.Check
	if subject == "" {
		subject = "the check declared here"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s:%d: %s: %s", f.File, f.Line, f.Kind, subject)
	switch f.Kind {
	case OverDeclared:
		fmt.Fprintf(&b, " declares %s but reads only facts complete at %s; declare %s",
			f.DeclaredView, f.NarrowestView, f.NarrowestView)
	case NoFactRead:
		fmt.Fprintf(&b, " declares %s and reads no fact from it, so its declaration places it at nothing; %s would do as well",
			f.DeclaredView, f.NarrowestView)
	case Unreadable:
		fmt.Fprintf(&b, " declares %s and %s, so it is neither cleared nor accused", f.DeclaredView, f.Detail)
	case UnknownView:
		fmt.Fprintf(&b, " declares %s, which is on no stage of the view chain", f.DeclaredView)
	case UnknownFact:
		fmt.Fprintf(&b, " declares %s and reads %s, which no view on the chain declares",
			f.DeclaredView, f.Detail)
	case SubjectOnlyUnsupported:
		fmt.Fprintf(&b, " declares that its input comes from its subject, but %s", f.Detail)
	}
	if len(f.Facts) > 0 {
		fmt.Fprintf(&b, " (reads: %s)", strings.Join(f.Facts, ", "))
	}
	return b.String()
}

// Report renders findings as text, one per line, in the order given.
func Report(findings []Finding) string {
	var b strings.Builder
	for _, f := range findings {
		b.WriteString(f.String())
		b.WriteByte('\n')
	}
	return b.String()
}

// Analyze reads every check declared in the non-test Go sources of each dir and
// reports what it finds against chain.
//
// It takes the chain as a value rather than reading it itself, so the corpus and
// the chain are independent inputs: the same chain reads a whole tree of check
// packages, and a caller holding one already-read chain pays for it once.
//
// Findings are ordered by file, then position, then name — an order that depends
// on the sources and not on a map's iteration, so the same input reports the
// same bytes.
func Analyze(chain Chain, dirs ...string) ([]Finding, error) {
	if len(chain.stages) == 0 {
		return nil, fmt.Errorf("the view chain is empty, so a clean result would certify nothing")
	}

	var findings []Finding
	for _, dir := range dirs {
		fset := token.NewFileSet()
		files, err := parsePackageDirWith(fset, dir)
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			findings = append(findings, analyzeFile(chain, fset, file)...)
		}
	}
	slices.SortStableFunc(findings, func(a, b Finding) int {
		if a.File != b.File {
			return strings.Compare(a.File, b.File)
		}
		if a.Line != b.Line {
			return a.Line - b.Line
		}
		return strings.Compare(a.Check, b.Check)
	})
	return findings, nil
}

func analyzeFile(chain Chain, fset *token.FileSet, file *ast.File) []Finding {
	names := literalNames(file)

	var findings []Finding
	ast.Inspect(file, func(node ast.Node) bool {
		lit, ok := node.(*ast.CompositeLit)
		if !ok || typeName(lit.Type) != checkTypeName {
			return true
		}
		if finding, report := analyzeCheck(chain, lit, names[lit]); report {
			position := fset.Position(lit.Pos())
			finding.File = position.Filename
			finding.Line = position.Line
			findings = append(findings, finding)
		}
		return true
	})
	return findings
}

// analyzeCheck reads one check literal. It reports nothing when the declared view
// is exactly the narrowest one that suffices, which is the whole point: a
// correctly-declaring check is silent here.
func analyzeCheck(chain Chain, lit *ast.CompositeLit, name string) (Finding, bool) {
	finding := Finding{Check: name}

	// A literal of this name that is not a generic instantiation is some other
	// type: the check type takes its view as a type argument, so a literal
	// without one cannot be a check.
	args, err := typeArgs(lit.Type)
	if err != nil {
		return Finding{}, false
	}
	finding.DeclaredView = typeName(args[0])
	if finding.DeclaredView == "" {
		finding.Kind = Unreadable
		finding.Detail = "declares a view that is not a named type"
		return finding, true
	}

	declaredAt, onChain := chain.Index(finding.DeclaredView)
	if !onChain {
		finding.Kind = UnknownView
		return finding, true
	}

	predicate, found := predicateLiteral(lit)
	if !found {
		finding.Kind = Unreadable
		finding.Detail = "carries no predicate written in place, so there is no body to read"
		return finding, true
	}

	subjectOnly, declaredSubjectOnly := subjectOnlyReason(lit)

	params := paramNames(predicate)
	viewIdent := paramAt(params, 0)

	// A predicate that cannot name its view reads no fact from it, so the reading
	// below is skipped rather than run against an identifier no body mentions.
	var facts []string
	var escape string
	if viewIdent != "" {
		facts, escape = readViewUse(predicate.Body, viewIdent)
	}
	finding.Facts = facts

	if declaredSubjectOnly {
		// The declaration is verified against the body rather than believed.
		// It clears exactly one shape — a predicate reading nothing from its
		// view and something from its subject — and it is never a way to
		// excuse a view wider than the facts a body does read.
		if escape != "" {
			finding.Kind = Unreadable
			finding.Detail = escape
			return finding, true
		}
		finding.Kind = SubjectOnlyUnsupported
		switch {
		case len(facts) > 0:
			finding.Detail = fmt.Sprintf("its predicate reads %s from %s, so its input is not its subject alone",
				strings.Join(facts, ", "), viewIdent)
			return finding, true
		case subjectOnly == "":
			finding.Detail = "it states no reason, and a declaration satisfiable by saying nothing says nothing"
			return finding, true
		}

		subjectIdent := paramAt(params, 1)
		if subjectIdent == "" {
			finding.Detail = "its predicate does not name its subject, so it reads nothing from one"
			return finding, true
		}
		reads := selectorsOn(predicate.Body, subjectIdent)
		if len(reads) == 0 {
			finding.Detail = fmt.Sprintf("its predicate reads nothing from %s either, so no input is accounted for", subjectIdent)
			return finding, true
		}
		if !namesOneOf(subjectOnly, reads) {
			finding.Detail = fmt.Sprintf("its reason names none of what the predicate reads from %s (%s), so it describes some other check",
				subjectIdent, strings.Join(reads, ", "))
			return finding, true
		}
		return Finding{}, false
	}

	if viewIdent == "" {
		// The predicate cannot name its view, so its body cannot read a fact
		// from it. That is the no-fact case rather than an unreadable one.
		finding.Kind = NoFactRead
		finding.NarrowestView = chain.stages[0].Name
		return finding, true
	}
	if escape != "" {
		finding.Kind = Unreadable
		finding.Detail = escape
		return finding, true
	}
	if len(facts) == 0 {
		finding.Kind = NoFactRead
		finding.NarrowestView = chain.stages[0].Name
		return finding, true
	}

	narrowestAt, unknown := chain.NarrowestSufficient(facts)
	if len(unknown) > 0 {
		finding.Kind = UnknownFact
		finding.Detail = strings.Join(unknown, ", ")
		return finding, true
	}
	if narrowestAt >= declaredAt {
		return Finding{}, false
	}
	finding.Kind = OverDeclared
	finding.NarrowestView = chain.stages[narrowestAt].Name
	return finding, true
}

// literalNames maps a composite literal to the name it is bound to, so a finding
// can name the check rather than only its position.
func literalNames(file *ast.File) map[ast.Expr]string {
	out := map[ast.Expr]string{}
	bind := func(target ast.Expr, value ast.Expr) {
		ident, ok := target.(*ast.Ident)
		if !ok {
			return
		}
		out[value] = ident.Name
		if unary, ok := value.(*ast.UnaryExpr); ok && unary.Op == token.AND {
			out[unary.X] = ident.Name
		}
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch decl := node.(type) {
		case *ast.ValueSpec:
			for i, value := range decl.Values {
				if i < len(decl.Names) {
					bind(decl.Names[i], value)
				}
			}
		case *ast.AssignStmt:
			for i, value := range decl.Rhs {
				if i < len(decl.Lhs) {
					bind(decl.Lhs[i], value)
				}
			}
		case *ast.KeyValueExpr:
			bind(decl.Key, decl.Value)
		}
		return true
	})
	return out
}

// predicateLiteral is the check's predicate, when it is written in place. A
// predicate supplied as a named function is deliberately not followed: reading it
// would mean resolving a reference across files and packages, and a reading that
// silently gave up would return a clean result over a body it never saw.
func predicateLiteral(lit *ast.CompositeLit) (*ast.FuncLit, bool) {
	for _, element := range lit.Elts {
		kv, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != predicateFieldName {
			continue
		}
		fn, ok := kv.Value.(*ast.FuncLit)
		return fn, ok
	}
	return nil, false
}

// predicateFieldName is the check field carrying the rule itself.
const predicateFieldName = "Run"

// subjectOnlyFieldName is the check field on which a predicate declares that its
// whole input comes from its subject, together with the reason.
const subjectOnlyFieldName = "SubjectOnly"

// subjectOnlyReason is the reason a check states for reading no view fact, and
// whether it declared one at all.
//
// A declaration written as anything other than a string literal in place reports
// as declared with an empty reason. Following a constant to its declaration would
// mean resolving a reference across files, and one reason shared by two checks is
// exactly the habitual application this field exists to prevent: the reason has
// to be about this check, so it is written at this check.
func subjectOnlyReason(lit *ast.CompositeLit) (reason string, declared bool) {
	for _, element := range lit.Elts {
		kv, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != subjectOnlyFieldName {
			continue
		}
		text, ok := stringLiteral(kv.Value)
		if !ok {
			return "", true
		}
		return text, true
	}
	return "", false
}

// stringLiteral is the text of a string literal, including one written as
// several literals concatenated, which is how a reason long enough to be one
// wraps across lines.
func stringLiteral(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		text, err := strconv.Unquote(e.Value)
		if err != nil {
			return "", false
		}
		return text, true
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		left, ok := stringLiteral(e.X)
		if !ok {
			return "", false
		}
		right, ok := stringLiteral(e.Y)
		if !ok {
			return "", false
		}
		return left + right, true
	case *ast.ParenExpr:
		return stringLiteral(e.X)
	}
	return "", false
}

// paramNames are the predicate's parameter names in order, with a blank or
// absent name reported as empty so a caller can tell a named parameter from one
// the body cannot reach.
func paramNames(fn *ast.FuncLit) []string {
	var out []string
	if fn.Type.Params == nil {
		return out
	}
	for _, field := range fn.Type.Params.List {
		// A field with no names is an unnamed parameter, which the body cannot
		// reach; several names share one type declaration.
		if len(field.Names) == 0 {
			out = append(out, "")
			continue
		}
		for _, name := range field.Names {
			if name.Name == "_" {
				out = append(out, "")
				continue
			}
			out = append(out, name.Name)
		}
	}
	return out
}

func paramAt(names []string, index int) string {
	if index >= len(names) {
		return ""
	}
	return names[index]
}

// selectorsOn collects the accessor names a body reads from one identifier,
// sorted and deduplicated.
//
// It differs from readViewUse in refusing to draw a conclusion from the
// identifier escaping: what a check does with its subject beyond reading from it
// — raising an objection through it, handing it on — is ordinary, and only the
// names read from it are wanted here.
func selectorsOn(body *ast.BlockStmt, ident string) []string {
	var out []string
	ast.Inspect(body, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if base, ok := selector.X.(*ast.Ident); ok && base.Name == ident {
			out = append(out, selector.Sel.Name)
		}
		return true
	})
	slices.Sort(out)
	return slices.Compact(out)
}

// namesOneOf reports whether text names at least one of the accessors, matched
// as a whole word so that a name is not satisfied by appearing inside a longer
// one.
func namesOneOf(text string, accessors []string) bool {
	for _, accessor := range accessors {
		for at := 0; ; {
			i := strings.Index(text[at:], accessor)
			if i < 0 {
				break
			}
			start := at + i
			end := start + len(accessor)
			if !isIdentRune(text, start-1) && !isIdentRune(text, end) {
				return true
			}
			at = start + 1
		}
	}
	return false
}

// isIdentRune reports whether the byte at index continues a Go identifier, and
// false where the index is outside the text.
func isIdentRune(text string, index int) bool {
	if index < 0 || index >= len(text) {
		return false
	}
	c := text[index]
	return c == '_' ||
		('0' <= c && c <= '9') ||
		('a' <= c && c <= 'z') ||
		('A' <= c && c <= 'Z')
}

// readViewUse collects the facts a body reads from the view, and reports the
// first use of the view that the reading cannot follow.
//
// Collection is over selector expressions rather than call expressions, so an
// accessor taken as a value — bound to a variable and called later, or passed on
// — counts as read exactly as a direct call does.
//
// escape is non-empty when the view itself is used as a value: handed to a
// function, assigned, returned, or captured. Such a use puts the facts the check
// reads outside this body, so the requirement set is incomplete and no verdict
// may be drawn from it.
func readViewUse(body *ast.BlockStmt, viewIdent string) (facts []string, escape string) {
	selectorBase := map[*ast.Ident]string{}
	var uses []*ast.Ident

	ast.Inspect(body, func(node ast.Node) bool {
		switch n := node.(type) {
		case *ast.SelectorExpr:
			if ident, ok := n.X.(*ast.Ident); ok && ident.Name == viewIdent {
				selectorBase[ident] = n.Sel.Name
			}
		case *ast.Ident:
			if n.Name == viewIdent {
				uses = append(uses, n)
			}
		}
		return true
	})

	for _, use := range uses {
		fact, isSelectorBase := selectorBase[use]
		if !isSelectorBase {
			if escape == "" {
				escape = fmt.Sprintf("uses %s as a value rather than reading a fact from it, so the facts it reads are not all in this body", viewIdent)
			}
			continue
		}
		facts = append(facts, fact)
	}
	slices.Sort(facts)
	return slices.Compact(facts), escape
}
